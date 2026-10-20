package consensus

import (
	"math/rand"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/makoto-kanamoto/etl/internal/blockchain"
	"github.com/makoto-kanamoto/etl/internal/blockchain/chaintest"
	"github.com/makoto-kanamoto/etl/internal/crypto"
	"github.com/makoto-kanamoto/etl/internal/mempool"
	"github.com/makoto-kanamoto/etl/internal/network"
	"github.com/makoto-kanamoto/etl/internal/storage"
)

// Réseau simulé en mémoire : une horloge commune, un bus qui livre chaque message à chaque nœud
// joignable (même partition, en ligne). Le bus peut perdre et désordonner les messages (graine
// fixée : reproductible), et joue le rôle du réseau pour les blocs finalisés : il les ajoute à la
// chaîne du destinataire avant d'appeler Handle.

type simNode struct {
	e      *Engine
	store  *storage.Store
	pool   *mempool.Pool
	wallet chaintest.Wallet
	halted bool
	inbox  []network.Message
}

type sim struct {
	t     testing.TB
	p     blockchain.Params
	net   *chaintest.Net
	now   time.Time
	nodes []*simNode

	mu    sync.Mutex
	queue []delivery

	down        map[int]bool
	mute        map[int]bool // nœuds qui ne reçoivent rien et n'agissent pas seuls (malveillants pilotés à la main)
	group       map[int]int
	drop        func(d delivery) bool // perte d'un message (nil : aucune)
	byz         map[int]bool          // nœuds malveillants
	relay       bool                  // les nœuds relaient les votes acceptés (comme le vrai réseau)
	relayed     map[crypto.Hash]bool
	reproposals int                                          // re-propositions observées (tous nœuds)
	adversary   func(from int, m network.Message) []delivery // ce qu'ils diffusent réellement
	rng         *rand.Rand
	shuffle     bool
	verdict     map[network.MsgType]map[network.Verdict]int
}

type delivery struct {
	from, to int // to < 0 : tous
	m        network.Message
}

type port struct {
	s    *sim
	from int
}

func (pt port) Broadcast(m network.Message) { pt.s.send(pt.from, -1, m) }

func (s *sim) send(from, to int, m network.Message) {
	if m.Type == network.MsgProposal && !s.byz[from] {
		if pr, err := blockchain.DecodeProposal(m.Payload, s.p.AddressHRP); err == nil && pr.POL != nil {
			s.reproposals++
		}
	}
	var ds []delivery
	if s.byz[from] && s.adversary != nil && to < 0 {
		ds = s.adversary(from, m)
	} else {
		ds = []delivery{{from, to, m}}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queue = append(s.queue, ds...)
}

func newSim(t testing.TB) *sim {
	return newSimSeed(t, 1)
}

func newSimSeed(t testing.TB, seed int64) *sim {
	p := chaintest.Params()
	s := &sim{t: t, p: p, net: chaintest.New(t, p), down: map[int]bool{}, mute: map[int]bool{}, group: map[int]int{},
		rng: rand.New(rand.NewSource(seed)), verdict: map[network.MsgType]map[network.Verdict]int{}, relayed: map[crypto.Hash]bool{}}
	s.now = time.Unix(chaintest.Day(1), 0).Add(-time.Hour)
	for _, w := range s.net.Founders {
		s.addNode(w, true)
	}
	return s
}

// addNode ajoute un nœud (avec ou sans clé) synchronisé sur le premier nœud.
func (s *sim) addNode(w chaintest.Wallet, withKey bool) *simNode {
	st := s.openStore()
	if len(s.nodes) > 0 {
		for h := uint64(1); h <= s.nodes[0].store.Tip().Height; h++ {
			b, _ := s.nodes[0].store.BlockAt(h)
			c, _ := s.nodes[0].store.CertificateAt(h)
			if err := st.Append(&b, &c); err != nil {
				s.t.Fatal(err)
			}
		}
	}
	n := &simNode{store: st, pool: mempool.New(s.p, mempool.DefaultOptions()), wallet: w}
	s.nodes = append(s.nodes, n)
	s.restart(len(s.nodes)-1, withKey)
	return n
}

func (s *sim) openStore() *storage.Store {
	st, err := storage.Open(filepath.Join(s.t.TempDir(), "chain.db"), s.p, storage.DefaultOptions())
	if err != nil {
		s.t.Fatal(err)
	}
	s.t.Cleanup(func() { st.Close() })
	if err := st.Init(&s.net.Genesis); err != nil {
		s.t.Fatal(err)
	}
	return st
}

// restart recrée le moteur d'un nœud sur le même stockage (redémarrage : mémoire perdue, disque gardé).
func (s *sim) restart(i int, withKey bool) {
	n := s.nodes[i]
	cfg := Config{Params: s.p, Now: func() time.Time { return s.now }, Halted: func() bool { return n.halted }}
	if withKey {
		cfg.Key = n.wallet.Key
	}
	n.e = New(cfg, n.store, n.pool, port{s, i})
}

func (s *sim) reachable(from, to int) bool {
	return from != to && !s.down[to] && !s.down[from] && s.group[from] == s.group[to]
}

// flush livre tous les messages en attente (y compris ceux produits pendant la livraison),
// dans le désordre si shuffle est actif.
func (s *sim) flush() {
	for {
		s.mu.Lock()
		if len(s.queue) == 0 {
			s.mu.Unlock()
			return
		}
		i := 0
		if s.shuffle {
			i = s.rng.Intn(len(s.queue))
		}
		d := s.queue[i]
		s.queue = append(s.queue[:i], s.queue[i+1:]...)
		s.mu.Unlock()
		for j, n := range s.nodes {
			if (d.to >= 0 && d.to != j) || !s.reachable(d.from, j) || s.mute[j] {
				continue
			}
			if s.drop != nil && s.drop(delivery{d.from, j, d.m}) {
				continue
			}
			if d.m.Type == network.MsgFinalized { // ce que ferait network.Host
				b, c, err := network.DecodeFinalized(d.m.Payload, s.p.AddressHRP)
				if err != nil || b.Header.Height != n.store.Tip().Height+1 || n.store.Append(&b, &c) != nil {
					continue
				}
			}
			n.inbox = append(n.inbox, d.m)
			v := n.e.Handle(d.m)
			// Relais (comme network.Host) : un vote accepté repart vers tous, une seule fois par nœud.
			if s.relay && v == network.Accept && d.m.Type == network.MsgVote {
				id := d.m.ID()
				key := crypto.Sum(crypto.ContextMessage, append(id[:], byte(j)))
				if !s.relayed[key] {
					s.relayed[key] = true
					s.mu.Lock()
					s.queue = append(s.queue, delivery{j, -1, d.m})
					s.mu.Unlock()
				}
			}
			if s.verdict[d.m.Type] == nil {
				s.verdict[d.m.Type] = map[network.Verdict]int{}
			}
			s.verdict[d.m.Type][v]++
		}
	}
}

// at fait avancer l'horloge, appelle Tick sur chaque nœud en ligne, puis livre les messages.
func (s *sim) at(now time.Time) {
	s.now = now
	for i, n := range s.nodes {
		if !s.down[i] && !s.mute[i] {
			n.e.Tick(now)
			s.flush()
		}
	}
	s.flush()
}

// runDay déroule une journée complète : participation, 5 tours, fin de journée.
func (s *sim) runDay(d int) {
	bt := chaintest.Day(d)
	s.at(time.Unix(bt, 0))
	for r := 0; r < s.p.MaxRounds; r++ {
		s.at(blockchain.RoundStart(bt, uint8(r), s.p).Add(time.Second))
		s.at(blockchain.RoundStart(bt, uint8(r), s.p).Add(time.Minute)) // seconde chance dans le tour
	}
	s.at(blockchain.RoundStart(bt, uint8(s.p.MaxRounds), s.p).Add(time.Second))
}

func (s *sim) heights() map[uint64]int {
	out := map[uint64]int{}
	for i, n := range s.nodes {
		if !s.down[i] {
			out[n.store.Tip().Height]++
		}
	}
	return out
}

func (s *sim) requireHeight(h uint64) {
	s.t.Helper()
	tips := map[crypto.Hash]bool{}
	for i, n := range s.nodes {
		if s.down[i] {
			continue
		}
		tip := n.store.Tip()
		if tip.Height != h {
			s.t.Fatalf("nœud %d à la hauteur %d, attendu %d (hauteurs : %v)", i, tip.Height, h, s.heights())
		}
		tips[tip.Hash()] = true
	}
	if len(tips) != 1 {
		s.t.Fatalf("%d têtes de chaîne différentes", len(tips))
	}
}

// safety vérifie l'invariant de sécurité : à chaque hauteur, au plus un bloc finalisé parmi les nœuds donnés.
func (s *sim) safety(honest []int) {
	s.t.Helper()
	byHeight := map[uint64]crypto.Hash{}
	for _, i := range honest {
		st := s.nodes[i].store
		for h := uint64(1); h <= st.Tip().Height; h++ {
			hdr, err := st.HeaderAt(h)
			if err != nil {
				s.t.Fatal(err)
			}
			if prev, ok := byHeight[h]; ok && prev != hdr.Hash() {
				s.t.Fatalf("SÉCURITÉ VIOLÉE : deux blocs finalisés à la hauteur %d", h)
			}
			byHeight[h] = hdr.Hash()
		}
	}
}
