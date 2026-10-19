package network

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/makoto-kanamoto/etl/internal/blockchain"
	"github.com/makoto-kanamoto/etl/internal/crypto"
)

// Synchronisation : un nœud qui démarre ou revient rattrape la chaîne en téléchargeant les blocs
// finalisés et leurs certificats, depuis le genesis, auprès des nœuds archive. Chaque bloc est
// vérifié par Chain.Append (bloc, certificat, StateRoot) : un pair ne peut pas inventer de blocs.
//
// Deux défenses contre l'attaque « longue portée » (anciennes clés de fondateurs compromises) :
//   - points de contrôle embarqués dans chaque version du logiciel : toute chaîne qui les
//     contredit est refusée ;
//   - détection des conflits : deux certificats pour deux blocs différents à la même hauteur
//     prouvent que des votants ont voté deux fois. Le nœud s'ARRÊTE et alerte son opérateur
//     plutôt que de choisir — les doubles votes sont extraits comme preuves d'exclusion.

// Chain est la chaîne locale (storage.Store).
type Chain interface {
	Tip() blockchain.Header
	Append(b *blockchain.Block, c *blockchain.Certificate) error
	HeaderAt(height uint64) (blockchain.Header, error)
	BlockAt(height uint64) (blockchain.Block, error)
	CertificateAt(height uint64) (blockchain.Certificate, error)
}

// Checkpoint est un bloc connu, embarqué dans une version du logiciel.
type Checkpoint struct {
	Height uint64
	Hash   crypto.Hash
}

// Alert signale une rupture de sécurité : le nœud s'est arrêté.
type Alert struct {
	Height   uint64
	Ours     crypto.Hash
	Theirs   crypto.Hash
	Evidence []blockchain.VoteEquivocation // doubles votes prouvés (à inclure pour exclusion)
	Reason   string
}

var (
	ErrHalted      = errors.New("network: nœud arrêté après un conflit de finalité — intervention de l'opérateur requise")
	ErrNoPeer      = errors.New("network: aucun pair ne peut fournir les blocs manquants")
	errTimeout     = errors.New("network: délai de réponse dépassé")
	errUnavailable = errors.New("network: blocs non disponibles chez ce pair")
)

// Halted indique si le nœud s'est arrêté après un conflit de finalité.
func (h *Host) Halted() bool { return h.halted.Load() }

// Alerts renvoie les alertes émises.
func (h *Host) Alerts() []Alert {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]Alert(nil), h.alerts...)
}

func (h *Host) halt(a Alert) {
	h.mu.Lock()
	h.alerts = append(h.alerts, a)
	h.mu.Unlock()
	h.halted.Store(true)
	if h.cfg.OnAlert != nil {
		h.cfg.OnAlert(a)
	}
}

// checkLocalCheckpoints vérifie au démarrage que la chaîne locale ne contredit aucun point de contrôle.
func (h *Host) checkLocalCheckpoints() {
	if h.cfg.Chain == nil {
		return
	}
	for _, cp := range h.cfg.Checkpoints {
		hdr, err := h.cfg.Chain.HeaderAt(cp.Height)
		if err != nil {
			continue // pas encore atteint
		}
		if hash := hdr.Hash(); hash != cp.Hash {
			h.halt(Alert{Height: cp.Height, Ours: hash, Theirs: cp.Hash, Reason: "la chaîne locale contredit un point de contrôle"})
			return
		}
	}
}

// --- Requêtes ---

// request envoie une requête et attend la réponse portant le même identifiant.
func (h *Host) request(ctx context.Context, p *peer, t MsgType, payload func(req uint64) []byte) (Message, error) {
	req := h.reqSeq.Add(1)
	ch := make(chan Message, 1)
	p.mu.Lock()
	p.pending[req] = ch
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		delete(p.pending, req)
		p.mu.Unlock()
	}()
	p.send(Message{Type: t, Payload: payload(req)})
	timer := time.NewTimer(h.cfg.RequestTimeout)
	defer timer.Stop()
	select {
	case m := <-ch:
		if m.Type == MsgNotAvailable {
			return m, errUnavailable
		}
		return m, nil
	case <-timer.C:
		return Message{}, errTimeout
	case <-p.done:
		return Message{}, errTimeout
	case <-ctx.Done():
		return Message{}, ctx.Err()
	}
}

// deliver remet une réponse à la requête qui l'attend ; une réponse non sollicitée est ignorée.
func (p *peer) deliver(m Message) {
	req, ok := requestID(m.Payload)
	if !ok {
		return
	}
	p.mu.Lock()
	ch := p.pending[req]
	p.mu.Unlock()
	if ch != nil {
		select {
		case ch <- m:
		default:
		}
	}
}

// serve répond à une requête de synchronisation (au plus MaxServing à la fois).
func (h *Host) serve(p *peer, m Message) {
	req, _ := requestID(m.Payload)
	notAvailable := Message{Type: MsgNotAvailable, Payload: encodeTip(req, 0, crypto.Hash{})[:8]}
	select {
	case h.serving <- struct{}{}:
		defer func() { <-h.serving }()
	default:
		p.send(notAvailable) // déjà trop de demandes servies
		return
	}
	if h.cfg.Chain == nil {
		p.send(notAvailable)
		return
	}
	switch m.Type {
	case MsgGetTip:
		tip := h.cfg.Chain.Tip()
		p.send(Message{Type: MsgTip, Payload: encodeTip(req, tip.Height, tip.Hash())})
	case MsgGetBlocks:
		_, from, max, err := decodeGetBlocks(m.Payload)
		if err != nil {
			h.ban(p)
			return
		}
		max = min(max, uint16(h.cfg.SyncBatch))
		var items [][]byte
		size := 0
		for height := from; height < from+uint64(max) && height <= h.cfg.Chain.Tip().Height; height++ {
			b, err1 := h.cfg.Chain.BlockAt(height)
			c, err2 := h.cfg.Chain.CertificateAt(height)
			if err1 != nil || err2 != nil {
				break // élagué : on sert ce qu'on a
			}
			it := EncodeFinalized(&b, &c)
			if size+len(it) > h.cfg.SyncBatchBytes && len(items) > 0 {
				break
			}
			items = append(items, it)
			size += len(it)
		}
		if len(items) == 0 {
			p.send(notAvailable)
			return
		}
		p.send(Message{Type: MsgBlocks, Payload: encodeBlocks(req, items)})
	}
}

// --- Rattrapage ---

func (h *Host) syncLoop() {
	defer h.wg.Done()
	t := time.NewTicker(h.cfg.SyncInterval)
	defer t.Stop()
	for {
		select {
		case <-h.ctx.Done():
			return
		case <-t.C:
		case <-h.syncTrigger:
		}
		_ = h.SyncOnce(h.ctx)
	}
}

func (h *Host) triggerSync() {
	select {
	case h.syncTrigger <- struct{}{}:
	default:
	}
}

type peerTip struct {
	p      *peer
	height uint64
}

// SyncOnce rattrape la chaîne auprès des pairs : hauteurs annoncées, puis téléchargement
// séquentiel par lots, nœuds archive d'abord. Un pair qui fournit un bloc invalide est banni.
func (h *Host) SyncOnce(ctx context.Context) error {
	if h.Halted() {
		return ErrHalted
	}
	if h.cfg.Chain == nil {
		return nil
	}
	h.syncMu.Lock() // une seule synchronisation à la fois
	defer h.syncMu.Unlock()

	h.mu.Lock()
	peers := make([]*peer, 0, len(h.peers))
	for p := range h.peers {
		peers = append(peers, p)
	}
	h.mu.Unlock()

	var tips []peerTip
	for _, p := range peers {
		m, err := h.request(ctx, p, MsgGetTip, func(req uint64) []byte { return encodeTip(req, 0, crypto.Hash{})[:8] })
		if err != nil {
			continue
		}
		height, _, err := decodeTip(m.Payload)
		if err != nil {
			h.ban(p)
			continue
		}
		tips = append(tips, peerTip{p, height})
	}
	sort.SliceStable(tips, func(i, j int) bool {
		ai, aj := tips[i].p.services&ServiceArchive != 0, tips[j].p.services&ServiceArchive != 0
		if ai != aj {
			return ai
		}
		return tips[i].height > tips[j].height
	})

	for {
		ours := h.cfg.Chain.Tip().Height
		progressed, behind := false, false
		for _, t := range tips {
			if t.height <= ours || t.p.isClosed() {
				continue
			}
			behind = true
			m, err := h.request(ctx, t.p, MsgGetBlocks, func(req uint64) []byte {
				return encodeGetBlocks(req, ours+1, uint16(h.cfg.SyncBatch))
			})
			if err != nil {
				continue // élagué ou trop lent : pair suivant
			}
			items, err := decodeBlocks(m.Payload)
			if err != nil {
				h.ban(t.p)
				continue
			}
			for _, it := range items {
				b, c, err := DecodeFinalized(it, h.cfg.Params.AddressHRP)
				if err == nil {
					err = h.acceptFinalized(&b, &c)
				}
				if errors.Is(err, ErrHalted) {
					return ErrHalted
				}
				if err != nil {
					h.ban(t.p)
					break
				}
				progressed = true
			}
			if progressed {
				break
			}
		}
		if !behind {
			return nil
		}
		if !progressed {
			return ErrNoPeer
		}
	}
}

// acceptFinalized applique un bloc finalisé reçu (synchronisation ou diffusion).
//   - déjà connu à cette hauteur avec le même hash : rien à faire ;
//   - connu avec un AUTRE hash : conflit de finalité → recherche de doubles votes → arrêt ;
//   - en avance de plusieurs blocs : déclenche la synchronisation ;
//   - bloc suivant : point de contrôle, puis vérification complète et ajout.
//
// Une erreur autre que ErrHalted signifie que l'émetteur a fourni des données invalides.
func (h *Host) acceptFinalized(b *blockchain.Block, c *blockchain.Certificate) error {
	if h.Halted() {
		return ErrHalted
	}
	tip := h.cfg.Chain.Tip()
	height := b.Header.Height
	switch {
	case height <= tip.Height:
		ours, err := h.cfg.Chain.HeaderAt(height)
		if err != nil {
			return errInvalid
		}
		if ours.Hash() == b.Header.Hash() {
			return nil
		}
		return h.conflict(height, ours.Hash(), b, c)
	case height > tip.Height+1:
		h.triggerSync()
		return nil
	}
	for _, cp := range h.cfg.Checkpoints {
		if cp.Height == height && cp.Hash != b.Header.Hash() {
			return errInvalid
		}
	}
	if err := h.cfg.Chain.Append(b, c); err != nil {
		return errInvalid
	}
	return nil
}

// conflict compare notre certificat et le leur pour la même hauteur. Deux certificats de plus de
// 2/3 du même registre ont forcément des votants en commun : leurs doubles votes sont la preuve
// d'une rupture de sécurité. Sans votant commun, leur certificat ne vient pas de notre registre :
// simple donnée invalide.
func (h *Host) conflict(height uint64, ours crypto.Hash, b *blockchain.Block, theirs *blockchain.Certificate) error {
	if theirs.Height != height || theirs.BlockHash != b.Header.Hash() {
		return errInvalid
	}
	mine, err := h.cfg.Chain.CertificateAt(height)
	if err != nil {
		return errInvalid // notre certificat est élagué : impossible de prouver quoi que ce soit
	}
	byVoter := map[[crypto.AddressHashSize]byte]blockchain.Vote{}
	for _, v := range mine.Votes {
		byVoter[v.Voter.Hash] = v
	}
	var evidence []blockchain.VoteEquivocation
	for _, v := range theirs.Votes {
		if m, ok := byVoter[v.Voter.Hash]; ok && v.Verify(h.cfg.Params) == nil && m.Verify(h.cfg.Params) == nil {
			evidence = append(evidence, blockchain.NewVoteEquivocation(m, v))
		}
	}
	if len(evidence) == 0 {
		return errInvalid
	}
	h.halt(Alert{Height: height, Ours: ours, Theirs: b.Header.Hash(), Evidence: evidence,
		Reason: "deux blocs finalisés à la même hauteur : des votants ont voté deux fois"})
	return ErrHalted
}
