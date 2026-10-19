package network

import (
	"crypto/tls"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/makoto-kanamoto/etl/internal/blockchain"
	"github.com/makoto-kanamoto/etl/internal/blockchain/chaintest"
	"github.com/makoto-kanamoto/etl/internal/crypto"
)

// recorder : Handler de test qui compte les messages reçus et renvoie un verdict fixe.
type recorder struct {
	mu      sync.Mutex
	got     map[crypto.Hash]int
	verdict Verdict
}

func newRecorder(v Verdict) *recorder { return &recorder{got: map[crypto.Hash]int{}, verdict: v} }

func (r *recorder) Handle(m Message) Verdict {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got[m.ID()]++
	return r.verdict
}

func (r *recorder) count(id crypto.Hash) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.got[id]
}

type fixture struct {
	net *chaintest.Net
	cfg Config
}

func newFixture(t *testing.T) *fixture {
	net := chaintest.New(t, chaintest.Params())
	cfg := DefaultConfig(net.P, net.Genesis.Header.Hash())
	cfg.ListenAddr = "127.0.0.1:0"
	cfg.DialInterval = 50 * time.Millisecond
	cfg.HandshakeTimeout = 2 * time.Second
	return &fixture{net: net, cfg: cfg}
}

// host démarre un nœud ; en local tous les pairs ont la même IP : on bannit par adresse complète.
func (f *fixture) host(t *testing.T, h Handler, edit func(*Config)) *Host {
	t.Helper()
	cfg := f.cfg
	if edit != nil {
		edit(&cfg)
	}
	n, err := New(cfg, h)
	if err != nil {
		t.Fatal(err)
	}
	n.banKey = func(a net.Addr) string { return a.String() }
	if err := n.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(n.Close)
	return n
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("délai dépassé : %s", what)
}

// txMessage : une transaction valide encodée.
func (f *fixture) txMessage(t *testing.T, nonce uint64) Message {
	tx := f.net.Tx(t, f.net.Founders[0], f.net.Founders[1].Addr, 1000, nonce, chaintest.Day(1))
	return Message{Type: MsgTx, Payload: blockchain.EncodeTransaction(&tx)}
}

func TestHandshake(t *testing.T) {
	f := newFixture(t)
	a := f.host(t, newRecorder(Accept), nil)
	b := f.host(t, newRecorder(Accept), nil)
	if err := b.Connect(a.Addr().String()); err != nil {
		t.Fatal(err)
	}
	eventually(t, "connexion établie", func() bool {
		in, _ := a.PeerCount()
		_, out := b.PeerCount()
		return in == 1 && out == 1
	})
	// Connexion à soi-même : détectée et refusée.
	if err := a.Connect(a.Addr().String()); err != ErrSelfConnect {
		t.Fatalf("connexion à soi-même : err = %v", err)
	}
}

func TestWrongNetworkRefused(t *testing.T) {
	f := newFixture(t)
	a := f.host(t, newRecorder(Accept), nil)
	other := f.host(t, newRecorder(Accept), func(c *Config) { c.Params.ChainID = "ETL-DE" })
	if err := other.Connect(a.Addr().String()); err != ErrWrongNetwork {
		t.Fatalf("autre chain_id : err = %v", err)
	}
	fork := f.host(t, newRecorder(Accept), func(c *Config) { c.GenesisHash[0] ^= 1 })
	if err := fork.Connect(a.Addr().String()); err != ErrWrongNetwork {
		t.Fatalf("autre genesis : err = %v", err)
	}
}

// Une chaîne de 5 nœuds, refermée en anneau : le message atteint tout le monde, une seule fois chacun.
func TestGossipPropagation(t *testing.T) {
	f := newFixture(t)
	var nodes []*Host
	var recs []*recorder
	for i := 0; i < 5; i++ {
		r := newRecorder(Accept)
		recs = append(recs, r)
		nodes = append(nodes, f.host(t, r, func(c *Config) { c.MaxOutbound = 0 })) // topologie imposée
	}
	for i := 1; i < 5; i++ {
		if err := nodes[i].Connect(nodes[i-1].Addr().String()); err != nil {
			t.Fatal(err)
		}
	}
	if err := nodes[0].Connect(nodes[4].Addr().String()); err != nil { // anneau : deux chemins
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)

	m := f.txMessage(t, 0)
	start := time.Now()
	nodes[0].Broadcast(m)
	eventually(t, "propagation à tous les nœuds", func() bool {
		for _, r := range recs[1:] {
			if r.count(m.ID()) == 0 {
				return false
			}
		}
		return true
	})
	if d := time.Since(start); d > time.Second {
		t.Errorf("propagation en %v (la tolérance de 5 s doit couvrir le relais)", d)
	}
	time.Sleep(100 * time.Millisecond)
	for i, r := range recs[1:] {
		if c := r.count(m.ID()); c != 1 {
			t.Errorf("nœud %d : message traité %d fois", i+1, c)
		}
	}
	if recs[0].count(m.ID()) != 0 {
		t.Error("l'émetteur a retraité son propre message")
	}
}

// Message invalide (signature fausse) : émetteur banni, message jamais relayé.
func TestInvalidMessageBansAndIsNotRelayed(t *testing.T) {
	f := newFixture(t)
	behind := newRecorder(Accept)
	victim := f.host(t, newRecorder(Accept), func(c *Config) { c.MaxOutbound = 0 })
	far := f.host(t, behind, func(c *Config) { c.MaxOutbound = 0 })
	attacker := f.host(t, newRecorder(Accept), func(c *Config) { c.MaxOutbound = 0 })
	_ = far.Connect(victim.Addr().String())
	_ = attacker.Connect(victim.Addr().String())
	eventually(t, "connexions", func() bool { in, _ := victim.PeerCount(); return in == 2 })

	m := f.txMessage(t, 0)
	m.Payload[len(m.Payload)-10] ^= 1 // signature altérée
	attacker.Broadcast(m)
	eventually(t, "attaquant déconnecté", func() bool { _, out := attacker.PeerCount(); return out == 0 })
	time.Sleep(100 * time.Millisecond)
	if behind.count(m.ID()) != 0 {
		t.Fatal("message invalide relayé")
	}
	if in, _ := victim.PeerCount(); in != 1 {
		t.Fatalf("la victime doit garder son pair honnête : %d pairs", in)
	}
}

func TestHandlerVerdicts(t *testing.T) {
	for name, c := range map[string]struct {
		verdict      Verdict
		relayed      bool
		senderBanned bool
	}{
		"Accept": {Accept, true, false},
		"Ignore": {Ignore, false, false},
		"Reject": {Reject, false, true},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			behind := newRecorder(Accept)
			mid := f.host(t, newRecorder(c.verdict), func(cfg *Config) { cfg.MaxOutbound = 0 })
			far := f.host(t, behind, func(cfg *Config) { cfg.MaxOutbound = 0 })
			src := f.host(t, newRecorder(Accept), func(cfg *Config) { cfg.MaxOutbound = 0 })
			_ = far.Connect(mid.Addr().String())
			_ = src.Connect(mid.Addr().String())
			eventually(t, "connexions", func() bool { in, _ := mid.PeerCount(); return in == 2 })
			m := f.txMessage(t, 0)
			src.Broadcast(m)
			time.Sleep(200 * time.Millisecond)
			if got := behind.count(m.ID()) == 1; got != c.relayed {
				t.Errorf("relayé = %v, attendu %v", got, c.relayed)
			}
			_, out := src.PeerCount()
			if banned := out == 0; banned != c.senderBanned {
				t.Errorf("émetteur banni = %v, attendu %v", banned, c.senderBanned)
			}
		})
	}
}

// rawClient fait la poignée de main à la main pour envoyer ensuite n'importe quoi.
func rawClient(t *testing.T, f *fixture, addr string) net.Conn {
	t.Helper()
	conn, err := tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS13})
	if err != nil {
		t.Fatal(err)
	}
	h := hello{Version: ProtocolVersion, ChainID: f.cfg.Params.ChainID, Genesis: f.cfg.GenesisHash, Nonce: 42}
	if err := writeFrame(conn, Message{Type: MsgHello, Payload: h.encode()}); err != nil {
		t.Fatal(err)
	}
	if _, err := readFrame(conn, 1024); err != nil {
		t.Fatal(err)
	}
	return conn
}

func waitClosed(t *testing.T, conn net.Conn) {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 1024)
	for {
		if _, err := conn.Read(buf); err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				t.Fatal("connexion toujours ouverte")
			}
			return
		}
	}
}

func TestOversizeFrameBanned(t *testing.T) {
	f := newFixture(t)
	victim := f.host(t, newRecorder(Accept), func(c *Config) { c.MaxMessageSize = 1024 })
	conn := rawClient(t, f, victim.Addr().String())
	defer conn.Close()
	// Annonce 1 Mo : refusé avant toute allocation.
	_, _ = conn.Write([]byte{byte(MsgTx), 0, 0x10, 0, 0})
	waitClosed(t, conn)
	if !victim.IsBanned(conn.LocalAddr()) {
		t.Fatal("émetteur non banni")
	}
}

func TestUnknownTypeBanned(t *testing.T) {
	f := newFixture(t)
	victim := f.host(t, newRecorder(Accept), nil)
	conn := rawClient(t, f, victim.Addr().String())
	defer conn.Close()
	_ = writeFrame(conn, Message{Type: 200})
	waitClosed(t, conn)
	if !victim.IsBanned(conn.LocalAddr()) {
		t.Fatal("émetteur non banni")
	}
}

func TestFloodBanned(t *testing.T) {
	f := newFixture(t)
	victim := f.host(t, newRecorder(Accept), func(c *Config) { c.RatePerSecond, c.RateBurst = 10, 20 })
	conn := rawClient(t, f, victim.Addr().String())
	defer conn.Close()
	go func() {
		for i := 0; i < 200; i++ {
			if writeFrame(conn, Message{Type: MsgPing}) != nil {
				return
			}
		}
	}()
	waitClosed(t, conn)
	if !victim.IsBanned(conn.LocalAddr()) {
		t.Fatal("inondation non sanctionnée")
	}
}

// C ne connaît que A ; il apprend l'adresse de B par A et s'y connecte de lui-même.
func TestAddressExchange(t *testing.T) {
	f := newFixture(t)
	a := f.host(t, newRecorder(Accept), func(c *Config) { c.MaxOutbound = 0 })
	b := f.host(t, newRecorder(Accept), func(c *Config) { c.MaxOutbound = 0 })
	_ = b.Connect(a.Addr().String())
	eventually(t, "B connecté à A", func() bool { in, _ := a.PeerCount(); return in == 1 })
	c := f.host(t, newRecorder(Accept), func(c *Config) { c.Bootstrap = []string{a.Addr().String()} })
	eventually(t, "C connecté à A et B", func() bool { _, out := c.PeerCount(); return out == 2 })
	if in, _ := b.PeerCount(); in != 1 {
		t.Fatalf("B : %d pairs entrants", in)
	}
}

func TestMaxInbound(t *testing.T) {
	f := newFixture(t)
	a := f.host(t, newRecorder(Accept), func(c *Config) { c.MaxInbound = 2 })
	for i := 0; i < 3; i++ {
		_ = f.host(t, newRecorder(Accept), func(c *Config) { c.MaxOutbound = 0 }).Connect(a.Addr().String())
	}
	time.Sleep(200 * time.Millisecond)
	if in, _ := a.PeerCount(); in != 2 {
		t.Fatalf("%d pairs entrants, maximum 2", in)
	}
}

func TestStatelessChecks(t *testing.T) {
	f := newFixture(t)
	p := f.net.P
	b, cert := f.net.Next(t, 1, nil)
	other, _ := f.net.Next(t, 2, nil)
	w := f.net.Founders[0]
	vote, _ := blockchain.SignVote(w.Key, &b.Header, p)
	ipc, _ := blockchain.SignIPCVote(w.Key, blockchain.IPCRecord{Value: 1, Period: "2026-Q4"}, p)
	ex, _ := blockchain.SignExclusionVote(w.Key, f.net.Founders[1].Addr, 1, p)
	pt := b.Participants[0]

	tamper := func(bs []byte) []byte { c := append([]byte(nil), bs...); c[len(c)-5] ^= 1; return c }
	badProposal := b
	badProposal.Signature[3] ^= 1
	for name, c := range map[string]struct {
		m  Message
		ok bool
	}{
		"transaction":            {f.txMessage(t, 0), true},
		"transaction falsifiée":  {Message{MsgTx, tamper(f.txMessage(t, 0).Payload)}, false},
		"participation":          {Message{MsgParticipation, blockchain.EncodeParticipation(&pt)}, true},
		"participation tronquée": {Message{MsgParticipation, blockchain.EncodeParticipation(&pt)[:10]}, false},
		"vote":                   {Message{MsgVote, blockchain.EncodeVote(&vote)}, true},
		"vote falsifié":          {Message{MsgVote, tamper(blockchain.EncodeVote(&vote))}, false},
		"vote IPC":               {Message{MsgIPCVote, blockchain.EncodeIPCVote(&ipc)}, true},
		"vote d'exclusion":       {Message{MsgExclusionVote, blockchain.EncodeExclusionVote(&ex)}, true},
		"proposition":            {Message{MsgProposal, blockchain.EncodeBlock(&b)}, true},
		"proposition mal signée": {Message{MsgProposal, blockchain.EncodeBlock(&badProposal)}, false},
		"bloc finalisé":          {Message{MsgFinalized, EncodeFinalized(&b, &cert)}, true},
		"certificat d'un autre":  {Message{MsgFinalized, EncodeFinalized(&other, &cert)}, false},
	} {
		if err := checkStateless(c.m, p); (err == nil) != c.ok {
			t.Errorf("%s : err = %v", name, err)
		}
	}
}

func FuzzDecoders(f *testing.F) {
	f.Add([]byte{0, 1, 6, 'E', 'T', 'L', '-', 'F', 'R'})
	f.Add(encodeAddrs([]*net.TCPAddr{{IP: net.ParseIP("10.0.0.1"), Port: 3000}}))
	f.Fuzz(func(t *testing.T, in []byte) {
		_, _ = decodeHello(in)
		_, _ = decodeAddrs(in)
		_, _, _ = DecodeFinalized(in, "etl")
		if h, err := decodeHello(in); err == nil && string(h.encode()) != string(in) {
			t.Fatal("hello non canonique")
		}
	})
}
