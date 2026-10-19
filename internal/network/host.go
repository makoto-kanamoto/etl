package network

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"math/big"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/makoto-kanamoto/etl/internal/blockchain"
	"github.com/makoto-kanamoto/etl/internal/crypto"
)

// Verdict est la décision de l'application sur un message diffusé.
type Verdict int

const (
	Accept Verdict = iota // valide : relayé à tous les autres pairs
	Ignore                // sans intérêt (périmé, hors fenêtre…) : ni relayé, ni sanctionné
	Reject                // invalide : l'émetteur est déconnecté et banni
)

// Handler valide les messages diffusés selon le contexte (état, jour, registre…).
// Il est appelé une seule fois par message (dédoublonnage), après les vérifications sans contexte.
type Handler interface {
	Handle(m Message) Verdict
}

// Config règle le nœud réseau.
type Config struct {
	Params      blockchain.Params
	GenesisHash crypto.Hash

	ListenAddr string   // "" : pas de connexions entrantes
	Bootstrap  []string // "hôte:port" (DNS ou IP)

	MaxOutbound    int           // 8
	MaxInbound     int           // 64
	MaxMessageSize int           // 32 Mo
	BanDuration    time.Duration // 24 h
	SeenTTL        time.Duration // durée de mémoire du dédoublonnage (1 h)
	RatePerSecond  float64       // messages par seconde et par pair (100)
	RateBurst      float64       // rafale tolérée (500)

	HandshakeTimeout time.Duration // 10 s
	IdleTimeout      time.Duration // 2 min sans rien recevoir → déconnexion
	PingInterval     time.Duration // 30 s
	DialInterval     time.Duration // 2 s entre deux tentatives de connexion sortante

	// Synchronisation
	Chain          Chain         // chaîne locale ; nil : ni service, ni rattrapage
	Archive        bool          // nœud archive : annoncé aux pairs (ServiceArchive)
	Checkpoints    []Checkpoint  // blocs connus embarqués dans cette version du logiciel
	OnAlert        func(Alert)   // appelé si le nœud s'arrête (conflit de finalité)
	SyncInterval   time.Duration // vérification périodique du retard (30 s)
	RequestTimeout time.Duration // délai de réponse avant de passer à un autre pair (30 s)
	SyncBatch      int           // blocs par lot (100)
	SyncBatchBytes int           // octets par lot (16 Mo)
	MaxServing     int           // demandes de synchronisation servies en parallèle (4)
}

// DefaultConfig renvoie la configuration par défaut.
func DefaultConfig(p blockchain.Params, genesis crypto.Hash) Config {
	return Config{
		Params: p, GenesisHash: genesis,
		MaxOutbound: 8, MaxInbound: 64, MaxMessageSize: 32 << 20,
		BanDuration: 24 * time.Hour, SeenTTL: time.Hour,
		RatePerSecond: 100, RateBurst: 500,
		HandshakeTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute,
		PingInterval: 30 * time.Second, DialInterval: 2 * time.Second,
		SyncInterval: 30 * time.Second, RequestTimeout: 30 * time.Second,
		SyncBatch: 100, SyncBatchBytes: 16 << 20, MaxServing: 4,
	}
}

// Host est un nœud réseau ETL.
type Host struct {
	cfg     Config
	handler Handler
	tlsCert tls.Certificate
	nonce   uint64

	mu       sync.Mutex
	peers    map[*peer]struct{}
	dialed   map[string]*peer     // connexions sortantes par adresse
	banned   map[string]time.Time // clé de bannissement → fin
	book     map[string]*net.TCPAddr
	self     map[string]bool // adresses qui se sont révélées être ce nœud lui-même
	listener net.Listener
	seen     *seenCache

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	reqSeq      atomic.Uint64
	serving     chan struct{} // sémaphore des demandes servies
	syncTrigger chan struct{}
	syncMu      sync.Mutex
	halted      atomic.Bool
	alerts      []Alert

	banKey func(net.Addr) string // l'IP en production ; l'adresse complète dans les tests locaux
}

// New crée un nœud réseau avec une identité TLS jetable (sans lien avec l'adresse ETL).
func New(cfg Config, handler Handler) (*Host, error) {
	cert, err := ephemeralCert()
	if err != nil {
		return nil, err
	}
	var n [8]byte
	if _, err := rand.Read(n[:]); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Host{
		cfg: cfg, handler: handler, tlsCert: cert, nonce: binary.BigEndian.Uint64(n[:]),
		peers: map[*peer]struct{}{}, dialed: map[string]*peer{}, banned: map[string]time.Time{},
		book: map[string]*net.TCPAddr{}, self: map[string]bool{}, seen: newSeenCache(cfg.SeenTTL),
		ctx: ctx, cancel: cancel, banKey: ipOf,
		serving: make(chan struct{}, max(cfg.MaxServing, 1)), syncTrigger: make(chan struct{}, 1),
	}, nil
}

// ephemeralCert crée un certificat auto-signé jetable : il sert au chiffrement, pas à l'identité.
func ephemeralCert() (tls.Certificate, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(10 * 365 * 24 * time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, priv)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: priv}, nil
}

func ipOf(a net.Addr) string {
	if t, ok := a.(*net.TCPAddr); ok {
		return t.IP.String()
	}
	host, _, _ := net.SplitHostPort(a.String())
	return host
}

// Start ouvre l'écoute (si configurée) et lance la recherche de pairs.
func (h *Host) Start() error {
	if h.cfg.ListenAddr != "" {
		tlsCfg := &tls.Config{Certificates: []tls.Certificate{h.tlsCert}, MinVersion: tls.VersionTLS13}
		l, err := tls.Listen("tcp", h.cfg.ListenAddr, tlsCfg)
		if err != nil {
			return err
		}
		h.listener = l
		h.wg.Add(1)
		go h.acceptLoop()
	}
	h.checkLocalCheckpoints()
	h.wg.Add(3)
	go h.dialLoop()
	go h.sweepLoop()
	go h.syncLoop()
	return nil
}

// Addr renvoie l'adresse d'écoute (nil sans écoute).
func (h *Host) Addr() net.Addr {
	if h.listener == nil {
		return nil
	}
	return h.listener.Addr()
}

// Close arrête le nœud et ferme toutes les connexions.
func (h *Host) Close() {
	h.cancel()
	if h.listener != nil {
		h.listener.Close()
	}
	h.mu.Lock()
	all := make([]*peer, 0, len(h.peers))
	for p := range h.peers {
		all = append(all, p)
	}
	h.mu.Unlock()
	for _, p := range all { // hors verrou : fermer un pair reprend le verrou de l'hôte
		p.close()
	}
	h.wg.Wait()
}

// Broadcast diffuse un message émis par ce nœud à tous ses pairs.
func (h *Host) Broadcast(m Message) {
	h.seen.add(m.ID())
	h.relay(m, nil)
}

// PeerCount renvoie le nombre de pairs entrants et sortants.
func (h *Host) PeerCount() (in, out int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for p := range h.peers {
		if p.inbound {
			in++
		} else {
			out++
		}
	}
	return in, out
}

// IsBanned indique si une adresse est bannie.
func (h *Host) IsBanned(a net.Addr) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return time.Now().Before(h.banned[h.banKey(a)])
}

// Connect ouvre immédiatement une connexion sortante vers addr ("hôte:port").
func (h *Host) Connect(addr string) error {
	h.mu.Lock()
	if _, ok := h.dialed[addr]; ok {
		h.mu.Unlock()
		return nil
	}
	h.mu.Unlock()
	d := &net.Dialer{Timeout: h.cfg.HandshakeTimeout}
	conn, err := tls.DialWithDialer(d, "tcp", addr, &tls.Config{
		Certificates:       []tls.Certificate{h.tlsCert},
		MinVersion:         tls.VersionTLS13,
		InsecureSkipVerify: true, // chiffrement seul : l'identité de transport est volontairement anonyme
	})
	if err != nil {
		return err
	}
	if h.IsBanned(conn.RemoteAddr()) {
		conn.Close()
		return nil
	}
	err = h.setup(conn, false, addr)
	if err == ErrSelfConnect { // notre propre adresse, apprise par un pair : ne plus jamais la composer
		h.mu.Lock()
		delete(h.book, addr)
		h.self[addr] = true
		h.mu.Unlock()
	}
	return err
}

func (h *Host) acceptLoop() {
	defer h.wg.Done()
	for {
		conn, err := h.listener.Accept()
		if err != nil {
			return
		}
		in, _ := h.PeerCount()
		if in >= h.cfg.MaxInbound || h.IsBanned(conn.RemoteAddr()) {
			conn.Close()
			continue
		}
		go func() {
			if err := h.setup(conn, true, ""); err != nil {
				conn.Close()
			}
		}()
	}
}

func (h *Host) dialLoop() {
	defer h.wg.Done()
	t := time.NewTicker(h.cfg.DialInterval)
	defer t.Stop()
	for {
		h.dialMore()
		select {
		case <-h.ctx.Done():
			return
		case <-t.C:
		}
	}
}

// dialMore complète les connexions sortantes jusqu'à MaxOutbound : bootstrap d'abord, puis carnet d'adresses.
func (h *Host) dialMore() {
	_, out := h.PeerCount()
	if out >= h.cfg.MaxOutbound {
		return
	}
	var candidates []string
	h.mu.Lock()
	seenAddr := map[string]bool{}
	for _, a := range h.cfg.Bootstrap {
		seenAddr[a] = true
		candidates = append(candidates, a)
	}
	for k, a := range h.book {
		if !seenAddr[k] && !time.Now().Before(h.banned[h.banKey(a)]) {
			candidates = append(candidates, k)
		}
	}
	h.mu.Unlock()
	for _, a := range candidates {
		if _, out := h.PeerCount(); out >= h.cfg.MaxOutbound || h.ctx.Err() != nil {
			return
		}
		h.mu.Lock()
		_, connected := h.dialed[a]
		connected = connected || h.self[a]
		h.mu.Unlock()
		if !connected && (h.listener == nil || a != h.listener.Addr().String()) {
			_ = h.Connect(a)
		}
	}
}

// setup fait la poignée de main puis lance le pair.
func (h *Host) setup(conn net.Conn, inbound bool, dialAddr string) error {
	conn.SetDeadline(time.Now().Add(h.cfg.HandshakeTimeout))
	var listenPort uint16
	if h.listener != nil {
		listenPort = uint16(h.listener.Addr().(*net.TCPAddr).Port)
	}
	mine := hello{Version: ProtocolVersion, ChainID: h.cfg.Params.ChainID, Genesis: h.cfg.GenesisHash, ListenPort: listenPort, Nonce: h.nonce}
	if h.cfg.Archive {
		mine.Services |= ServiceArchive
	}
	if err := writeFrame(conn, Message{Type: MsgHello, Payload: mine.encode()}); err != nil {
		conn.Close()
		return err
	}
	m, err := readFrame(conn, 1024)
	if err != nil || m.Type != MsgHello {
		conn.Close()
		return ErrHello
	}
	theirs, err := decodeHello(m.Payload)
	switch {
	case err != nil || theirs.Version != ProtocolVersion:
		conn.Close()
		return ErrHello
	case theirs.ChainID != h.cfg.Params.ChainID || theirs.Genesis != h.cfg.GenesisHash:
		conn.Close()
		return ErrWrongNetwork
	case theirs.Nonce == h.nonce:
		conn.Close()
		return ErrSelfConnect
	}
	conn.SetDeadline(time.Time{})

	p := newPeer(h, conn, inbound)
	p.services = theirs.Services
	h.mu.Lock()
	if h.ctx.Err() != nil {
		h.mu.Unlock()
		conn.Close()
		return context.Canceled
	}
	h.peers[p] = struct{}{}
	if !inbound {
		p.dialAddr = dialAddr
		h.dialed[dialAddr] = p
	}
	if theirs.ListenPort != 0 { // adresse joignable du pair, pour le carnet d'adresses
		if ra, ok := conn.RemoteAddr().(*net.TCPAddr); ok {
			a := &net.TCPAddr{IP: ra.IP, Port: int(theirs.ListenPort)}
			h.book[a.String()] = a
		}
	}
	h.mu.Unlock()
	p.start()
	if !inbound {
		p.send(Message{Type: MsgGetAddrs})
	}
	return nil
}

func (h *Host) removePeer(p *peer) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.peers, p)
	if p.dialAddr != "" && h.dialed[p.dialAddr] == p {
		delete(h.dialed, p.dialAddr)
	}
}

// ban déconnecte et bannit l'émetteur d'un message invalide (et toute connexion de même clé).
func (h *Host) ban(p *peer) {
	key := h.banKey(p.conn.RemoteAddr())
	h.mu.Lock()
	h.banned[key] = time.Now().Add(h.cfg.BanDuration)
	var victims []*peer
	for q := range h.peers {
		if h.banKey(q.conn.RemoteAddr()) == key {
			victims = append(victims, q)
		}
	}
	h.mu.Unlock()
	for _, q := range victims {
		q.close()
	}
}

// onGossip traite un message diffusé reçu d'un pair :
// dédoublonnage → vérifications sans contexte → Handler → relais.
func (h *Host) onGossip(from *peer, m Message) {
	if !h.seen.add(m.ID()) {
		return // déjà vu : ni traité, ni relayé
	}
	if checkStateless(m, h.cfg.Params) != nil {
		h.ban(from)
		return
	}
	// Bloc finalisé : appliqué (ou conflit détecté) par le réseau lui-même, avant le Handler.
	if m.Type == MsgFinalized && h.cfg.Chain != nil {
		b, c, _ := DecodeFinalized(m.Payload, h.cfg.Params.AddressHRP)
		switch err := h.acceptFinalized(&b, &c); {
		case errors.Is(err, ErrHalted):
			return // rupture de sécurité : on ne relaie plus rien de ce bloc
		case err != nil:
			h.ban(from)
			return
		}
	}
	switch h.handler.Handle(m) {
	case Accept:
		h.relay(m, from)
	case Reject:
		h.ban(from)
	}
}

func (h *Host) relay(m Message, except *peer) {
	h.mu.Lock()
	targets := make([]*peer, 0, len(h.peers))
	for p := range h.peers {
		if p != except {
			targets = append(targets, p)
		}
	}
	h.mu.Unlock()
	for _, p := range targets {
		p.send(m)
	}
}

func (h *Host) addrsForPeer() []*net.TCPAddr {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]*net.TCPAddr, 0, MaxAddrs)
	for _, a := range h.book {
		if len(out) == MaxAddrs {
			break
		}
		out = append(out, a)
	}
	return out
}

func (h *Host) learnAddrs(addrs []*net.TCPAddr) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, a := range addrs {
		if len(h.book) >= 1000 {
			return
		}
		if a.Port != 0 && !a.IP.IsUnspecified() && !h.self[a.String()] {
			h.book[a.String()] = a
		}
	}
}

func (h *Host) sweepLoop() {
	defer h.wg.Done()
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-h.ctx.Done():
			return
		case <-t.C:
			h.seen.sweep()
			h.mu.Lock()
			now := time.Now()
			for k, until := range h.banned {
				if now.After(until) {
					delete(h.banned, k)
				}
			}
			h.mu.Unlock()
		}
	}
}
