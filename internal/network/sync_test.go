package network

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/makoto-kanamoto/etl/internal/blockchain"
	"github.com/makoto-kanamoto/etl/internal/blockchain/chaintest"
	"github.com/makoto-kanamoto/etl/internal/crypto"
	"github.com/makoto-kanamoto/etl/internal/storage"
)

// history : une chaîne de n blocs finalisés, construite une fois, réutilisable par plusieurs nœuds.
type history struct {
	net    *chaintest.Net
	blocks []blockchain.Block
	certs  []blockchain.Certificate
}

func buildHistory(t *testing.T, net *chaintest.Net, n int) *history {
	h := &history{net: net}
	for d := 1; d <= n; d++ {
		b, c := net.Next(t, d, nil)
		h.blocks, h.certs = append(h.blocks, b), append(h.certs, c)
	}
	return h
}

// store crée une base initialisée au genesis et y ajoute les upto premiers blocs.
func (h *history) store(t *testing.T, opt storage.Options, upto int) *storage.Store {
	t.Helper()
	s, err := storage.Open(filepath.Join(t.TempDir(), "chain.db"), h.net.P, opt)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Init(&h.net.Genesis); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < upto; i++ {
		if err := s.Append(&h.blocks[i], &h.certs[i]); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

// syncHost : nœud réseau adossé à une chaîne ; synchronisation pilotée à la main par les tests.
func (f *fixture) syncHost(t *testing.T, chain Chain, edit func(*Config)) *Host {
	return f.host(t, newRecorder(Accept), func(c *Config) {
		c.Chain = chain
		c.MaxOutbound = 0
		c.SyncInterval = time.Hour
		c.RequestTimeout = 2 * time.Second
		if edit != nil {
			edit(c)
		}
	})
}

func connect(t *testing.T, from, to *Host) {
	t.Helper()
	if err := from.Connect(to.Addr().String()); err != nil {
		t.Fatal(err)
	}
}

func TestSyncFromGenesis(t *testing.T) {
	f := newFixture(t)
	h := buildHistory(t, f.net, 250)
	archive := f.syncHost(t, h.store(t, storage.Options{Pruning: false}, 250), func(c *Config) { c.Archive = true })
	fresh := h.store(t, storage.DefaultOptions(), 0)
	node := f.syncHost(t, fresh, nil)
	connect(t, node, archive)

	if err := node.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fresh.Tip().Height != 250 {
		t.Fatalf("hauteur %d, attendu 250", fresh.Tip().Height)
	}
	var root crypto.Hash
	fresh.ReadState(func(st *blockchain.State) { root = st.Root() })
	if root != f.net.State.Root() {
		t.Fatal("état synchronisé différent")
	}
}

// Un pair élagué ne peut pas servir les anciens blocs ; un nœud archive, si.
func TestSyncNeedsArchive(t *testing.T) {
	f := newFixture(t)
	h := buildHistory(t, f.net, 30)
	pruned := f.syncHost(t, h.store(t, storage.Options{Pruning: true, KeepBlocks: 5}, 30), nil)
	fresh := h.store(t, storage.DefaultOptions(), 0)
	node := f.syncHost(t, fresh, nil)
	connect(t, node, pruned)
	if err := node.SyncOnce(context.Background()); !errors.Is(err, ErrNoPeer) {
		t.Fatalf("seul un pair élagué : err = %v", err)
	}
	if fresh.Tip().Height != 0 {
		t.Fatal("blocs obtenus d'un pair élagué")
	}
	archive := f.syncHost(t, h.store(t, storage.Options{Pruning: false}, 30), func(c *Config) { c.Archive = true })
	connect(t, node, archive)
	if err := node.SyncOnce(context.Background()); err != nil || fresh.Tip().Height != 30 {
		t.Fatalf("avec un nœud archive : hauteur %d, err = %v", fresh.Tip().Height, err)
	}
}

// tamperChain sert des blocs altérés à partir de la hauteur from.
type tamperChain struct {
	*storage.Store
	from uint64
}

func (c tamperChain) BlockAt(height uint64) (blockchain.Block, error) {
	b, err := c.Store.BlockAt(height)
	if err == nil && height >= c.from {
		b.Header.Proposer = b.Participants[len(b.Participants)-1].Miner // en-tête falsifié
	}
	return b, err
}

func TestSyncMaliciousPeerBanned(t *testing.T) {
	f := newFixture(t)
	h := buildHistory(t, f.net, 20)
	liar := f.syncHost(t, tamperChain{h.store(t, storage.Options{Pruning: false}, 20), 10}, func(c *Config) { c.Archive = true })
	honest := f.syncHost(t, h.store(t, storage.Options{Pruning: false}, 20), func(c *Config) { c.Archive = true })
	fresh := h.store(t, storage.DefaultOptions(), 0)
	node := f.syncHost(t, fresh, nil)
	connect(t, node, liar)
	connect(t, node, honest)
	if err := node.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fresh.Tip().Height != 20 {
		t.Fatalf("hauteur %d, attendu 20", fresh.Tip().Height)
	}
	eventually(t, "menteur déconnecté", func() bool { in, _ := liar.PeerCount(); return in == 0 })
}

func TestCheckpoints(t *testing.T) {
	f := newFixture(t)
	h := buildHistory(t, f.net, 10)
	archive := f.syncHost(t, h.store(t, storage.Options{Pruning: false}, 10), func(c *Config) { c.Archive = true })
	wrong := Checkpoint{Height: 5, Hash: crypto.Hash{0xde, 0xad}}

	// En synchronisation : la chaîne qui contredit le point de contrôle est refusée.
	fresh := h.store(t, storage.DefaultOptions(), 0)
	node := f.syncHost(t, fresh, func(c *Config) { c.Checkpoints = []Checkpoint{wrong} })
	connect(t, node, archive)
	_ = node.SyncOnce(context.Background())
	if fresh.Tip().Height != 4 {
		t.Fatalf("hauteur %d, attendu 4 (arrêt avant le point de contrôle)", fresh.Tip().Height)
	}

	// Au démarrage : une chaîne locale qui contredit un point de contrôle arrête le nœud.
	var alerted sync.WaitGroup
	alerted.Add(1)
	local := f.syncHost(t, h.store(t, storage.DefaultOptions(), 10), func(c *Config) {
		c.Checkpoints = []Checkpoint{wrong}
		c.OnAlert = func(Alert) { alerted.Done() }
	})
	alerted.Wait()
	if !local.Halted() {
		t.Fatal("nœud non arrêté")
	}
	// Le bon point de contrôle passe.
	good := Checkpoint{Height: 5, Hash: h.blocks[4].Header.Hash()}
	ok := f.syncHost(t, h.store(t, storage.DefaultOptions(), 10), func(c *Config) { c.Checkpoints = []Checkpoint{good} })
	if ok.Halted() {
		t.Fatal("bon point de contrôle refusé")
	}
}

// Deux blocs finalisés à la même hauteur : le nœud s'arrête, alerte et extrait les doubles votes.
func TestConflictHalts(t *testing.T) {
	f := newFixture(t)
	other := chaintest.NewWithFounders(t, f.net.P, f.net.Founders) // même genesis
	b1, c1 := f.net.Next(t, 1, nil)
	b1bis, c1bis := other.Next(t, 1, func(b *blockchain.Block) { b.Participants = b.Participants[:9] })
	if b1.Header.Hash() == b1bis.Header.Hash() {
		t.Fatal("prémisse : deux blocs différents")
	}

	h := &history{net: f.net, blocks: []blockchain.Block{b1}, certs: []blockchain.Certificate{c1}}
	chain := h.store(t, storage.DefaultOptions(), 1)
	alerts := make(chan Alert, 1)
	node := f.syncHost(t, chain, func(c *Config) { c.OnAlert = func(a Alert) { alerts <- a } })
	behind := newRecorder(Accept)
	downstream := f.host(t, behind, func(c *Config) { c.MaxOutbound = 0 })
	connect(t, downstream, node)
	attacker := f.syncHost(t, nil, nil)
	connect(t, attacker, node)
	eventually(t, "connexions", func() bool { in, _ := node.PeerCount(); return in == 2 })

	m := Message{Type: MsgFinalized, Payload: EncodeFinalized(&b1bis, &c1bis)}
	attacker.Broadcast(m)
	var got Alert
	select {
	case got = <-alerts:
	case <-time.After(3 * time.Second):
		t.Fatal("aucune alerte")
	}
	if !node.Halted() {
		t.Fatal("nœud non arrêté")
	}
	if got.Height != 1 || len(got.Evidence) < blockchain.Quorum(10)*2-10 {
		t.Fatalf("alerte : hauteur %d, %d preuves", got.Height, len(got.Evidence))
	}
	for _, e := range got.Evidence {
		if e.A.Voter != e.B.Voter || e.A.BlockHash == e.B.BlockHash {
			t.Fatal("preuve de double vote incohérente")
		}
	}
	time.Sleep(100 * time.Millisecond)
	if behind.count(m.ID()) != 0 {
		t.Fatal("bloc conflictuel relayé")
	}
	if err := node.SyncOnce(context.Background()); !errors.Is(err, ErrHalted) {
		t.Fatalf("synchronisation après arrêt : err = %v", err)
	}
	if tip := chain.Tip(); tip.Hash() != b1.Header.Hash() {
		t.Fatal("chaîne locale modifiée")
	}
}

// Un bloc finalisé diffusé avec plusieurs hauteurs d'avance déclenche la synchronisation.
func TestGossipAheadTriggersSync(t *testing.T) {
	f := newFixture(t)
	h := buildHistory(t, f.net, 5)
	archive := f.syncHost(t, h.store(t, storage.Options{Pruning: false}, 5), func(c *Config) { c.Archive = true })
	fresh := h.store(t, storage.DefaultOptions(), 0)
	node := f.syncHost(t, fresh, nil)
	connect(t, node, archive)
	eventually(t, "connexion", func() bool { _, out := node.PeerCount(); return out == 1 })
	archive.Broadcast(Message{Type: MsgFinalized, Payload: EncodeFinalized(&h.blocks[4], &h.certs[4])})
	eventually(t, "rattrapage déclenché", func() bool { return fresh.Tip().Height == 5 })
}

// Le bloc suivant, reçu par diffusion, est appliqué directement.
func TestGossipNextBlockApplied(t *testing.T) {
	f := newFixture(t)
	h := buildHistory(t, f.net, 2)
	src := f.syncHost(t, h.store(t, storage.DefaultOptions(), 2), nil)
	chain := h.store(t, storage.DefaultOptions(), 1)
	node := f.syncHost(t, chain, nil)
	connect(t, node, src)
	eventually(t, "connexion", func() bool { _, out := node.PeerCount(); return out == 1 })
	src.Broadcast(Message{Type: MsgFinalized, Payload: EncodeFinalized(&h.blocks[1], &h.certs[1])})
	eventually(t, "bloc 2 appliqué", func() bool { return chain.Tip().Height == 2 })
}

func TestSyncWire(t *testing.T) {
	req, from, max, err := decodeGetBlocks(encodeGetBlocks(7, 42, 100))
	if err != nil || req != 7 || from != 42 || max != 100 {
		t.Fatal("GetBlocks")
	}
	hash := crypto.Hash{1, 2, 3}
	if height, hh, err := decodeTip(encodeTip(7, 99, hash)); err != nil || height != 99 || hh != hash {
		t.Fatal("Tip")
	}
	items := [][]byte{{1, 2}, {}, {3}}
	got, err := decodeBlocks(encodeBlocks(7, items))
	if err != nil || len(got) != 3 || string(got[0]) != "\x01\x02" || len(got[1]) != 0 {
		t.Fatal("Blocks")
	}
	enc := encodeBlocks(7, items)
	for i := 0; i < len(enc); i++ {
		if _, err := decodeBlocks(enc[:i]); err == nil {
			t.Fatalf("lot tronqué à %d octets accepté", i)
		}
	}
	h := hello{Version: 1, ChainID: "ETL-FR", Nonce: 5, ListenPort: 3000, Services: ServiceArchive}
	if got, err := decodeHello(h.encode()); err != nil || got != h {
		t.Fatal("hello")
	}
}

func FuzzDecodeBlocks(f *testing.F) {
	f.Add(encodeBlocks(1, [][]byte{{1}, {2, 3}}))
	f.Fuzz(func(t *testing.T, in []byte) { _, _ = decodeBlocks(in) })
}
