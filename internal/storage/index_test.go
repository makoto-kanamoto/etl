package storage

import (
	"bytes"
	"errors"
	"math"
	"slices"
	"testing"

	bolt "go.etcd.io/bbolt"

	"github.com/makoto-kanamoto/etl/internal/blockchain"
	"github.com/makoto-kanamoto/etl/internal/blockchain/chaintest"
	"github.com/makoto-kanamoto/etl/internal/crypto"
)

var archiveIndexed = Options{Index: true}

type finalized struct {
	b blockchain.Block
	c blockchain.Certificate
}

// indexScenario : récompenses (jour 1), transfert d'un fondateur au futur vendeur (jour 2),
// ouverture d'un échange (jour 3), libération par l'acheteur et l'arbitre (jour 4) ; jour 5 :
// transfert de l'acheteur à un tiers, échange sans arbitre du vendeur au tiers, exclusion d'un
// fondateur qui participe encore au bloc (non récompensé). Vendeur, acheteur et tiers ne sont pas
// des nœuds ; l'arbitre (nœud admis) ne participe pas aux blocs 3 et 4 : chaque entrée de l'index a
// une seule origine.
type indexScenario struct {
	net                              *chaintest.Net
	blocks                           []finalized // jours 1 à 5
	tx, tx2                          blockchain.Transaction
	terms, terms2                    blockchain.EscrowTerms
	seller, buyer, arbiter, outsider chaintest.Wallet
}

func newIndexScenario(t *testing.T) *indexScenario {
	t.Helper()
	net := chaintest.New(t, chaintest.Params())
	sc := &indexScenario{net: net, seller: chaintest.NewWallet(t, net.P), buyer: chaintest.NewWallet(t, net.P),
		arbiter: net.Founders[3], outsider: chaintest.NewWallet(t, net.P)}
	f := net.Founders
	step := func(d int, edit func(*blockchain.Block)) {
		b, c := net.Next(t, d, edit)
		sc.blocks = append(sc.blocks, finalized{b, c})
	}
	withoutArbiter := func(b *blockchain.Block) {
		var kept []blockchain.Participation
		for _, p := range b.Participants {
			if p.Miner != sc.arbiter.Addr {
				kept = append(kept, p)
			}
		}
		b.Participants = kept
		b.Header.Proposer = kept[0].Miner
	}
	step(1, nil)
	sc.tx = net.Tx(t, f[0], sc.seller.Addr, blockchain.UnitsPerETL/2, 0, chaintest.Day(1))
	step(2, func(b *blockchain.Block) { b.Txs = []blockchain.Transaction{sc.tx} })
	fee, _ := blockchain.RequiredFee(blockchain.UnitsPerETL/4, net.P)
	sc.terms = blockchain.EscrowTerms{Seller: sc.seller.Addr, Buyer: sc.buyer.Addr, Arbiter: sc.arbiter.Addr,
		Amount: blockchain.UnitsPerETL / 4, ArbiterFee: blockchain.UnitsPerETL / 100, Fee: fee, Days: 3, Timestamp: chaintest.Day(2)}
	open := blockchain.EscrowOp{Kind: blockchain.EscrowOpen, Terms: sc.terms}
	if err := open.Sign(sc.seller.Key, blockchain.SignerSeller, net.P); err != nil {
		t.Fatal(err)
	}
	if err := open.Sign(sc.arbiter.Key, blockchain.SignerArbiter, net.P); err != nil {
		t.Fatal(err)
	}
	step(3, func(b *blockchain.Block) { b.EscrowOps = []blockchain.EscrowOp{open}; withoutArbiter(b) })
	release := blockchain.EscrowOp{Kind: blockchain.EscrowRelease, Escrow: sc.terms.ID()}
	if err := release.Sign(sc.buyer.Key, blockchain.SignerBuyer, net.P); err != nil {
		t.Fatal(err)
	}
	if err := release.Sign(sc.arbiter.Key, blockchain.SignerArbiter, net.P); err != nil {
		t.Fatal(err)
	}
	step(4, func(b *blockchain.Block) { b.EscrowOps = []blockchain.EscrowOp{release}; withoutArbiter(b) })
	sc.tx2 = net.Tx(t, sc.buyer, sc.outsider.Addr, blockchain.UnitsPerETL/10, 0, chaintest.Day(4))
	fee2, _ := blockchain.RequiredFee(blockchain.UnitsPerETL/10, net.P)
	sc.terms2 = blockchain.EscrowTerms{Seller: sc.seller.Addr, Buyer: sc.outsider.Addr, Amount: blockchain.UnitsPerETL / 10,
		Fee: fee2, Days: 3, Timestamp: chaintest.Day(4)}
	open2 := blockchain.EscrowOp{Kind: blockchain.EscrowOpen, Terms: sc.terms2}
	if err := open2.Sign(sc.seller.Key, blockchain.SignerSeller, net.P); err != nil {
		t.Fatal(err)
	}
	ex := blockchain.Exclusion{Target: f[9].Addr}
	for _, w := range f[:8] {
		v, err := blockchain.SignExclusionVote(w.Key, f[9].Addr, chaintest.Day(5)/86400, net.P)
		if err != nil {
			t.Fatal(err)
		}
		ex.Votes = append(ex.Votes, v)
	}
	slices.SortFunc(ex.Votes, func(a, b blockchain.ExclusionVote) int { return bytes.Compare(a.Voter.Hash[:], b.Voter.Hash[:]) })
	step(5, func(b *blockchain.Block) {
		b.Txs = []blockchain.Transaction{sc.tx2}
		b.EscrowOps = []blockchain.EscrowOp{open2}
		b.Exclusions = []blockchain.Exclusion{ex}
	})
	if b := sc.blocks[4].b; !slices.ContainsFunc(b.Participants, func(p blockchain.Participation) bool { return p.Miner == f[9].Addr }) || b.Rewarded(f[9].Addr) {
		t.Fatal("scénario : le fondateur exclu doit participer au bloc 5 sans être récompensé")
	}
	return sc
}

// store ouvre une base neuve et y ajoute les blocs des jours 1 … upTo.
func (sc *indexScenario) store(t *testing.T, path string, opt Options, upTo int) *Store {
	t.Helper()
	s := open(t, path, sc.net.P, opt)
	if err := s.Init(&sc.net.Genesis); err != nil {
		t.Fatal(err)
	}
	sc.append(t, s, 1, upTo)
	return s
}

func (sc *indexScenario) append(t *testing.T, s *Store, from, to int) {
	t.Helper()
	for d := from; d <= to; d++ {
		f := sc.blocks[d-1]
		if err := s.Append(&f.b, &f.c); err != nil {
			t.Fatalf("jour %d : %v", d, err)
		}
	}
}

func heights(t *testing.T, s *Store, a crypto.Address, before uint64, limit int) ([]uint64, bool) {
	t.Helper()
	h, more, err := s.AddressHeights(a, before, limit)
	if err != nil {
		t.Fatal(err)
	}
	return h, more
}

func TestIndexContent(t *testing.T) {
	sc := newIndexScenario(t)
	s := sc.store(t, dbPath(t), archiveIndexed, 5)
	defer s.Close()
	f := sc.net.Founders
	all := uint64(math.MaxUint64)
	for name, c := range map[string]struct {
		a    crypto.Address
		want []uint64
	}{
		"fondateur (récompensé chaque jour)":                        {f[0].Addr, []uint64{5, 4, 3, 2, 1}},
		"vendeur (reçu 2, ouverture 3, libération 4, 2e échange 5)": {sc.seller.Addr, []uint64{5, 4, 3, 2}},
		"exclu au bloc 5":                               {f[9].Addr, []uint64{4, 3, 2, 1}},
		"adresse nulle (échange sans arbitre)":          {crypto.Address{}, nil},
		"acheteur (ouverture 3, libération 4, envoi 5)": {sc.buyer.Addr, []uint64{5, 4, 3}},
		"arbitre (récompenses 1, 2, 5 ; échange 3, 4)":  {sc.arbiter.Addr, []uint64{5, 4, 3, 2, 1}},
		"tiers (reçu 5)":                                {sc.outsider.Addr, []uint64{5}},
		"inconnu":                                       {chaintest.NewWallet(t, sc.net.P).Addr, nil},
	} {
		if got, more := heights(t, s, c.a, all, 100); !slices.Equal(got, c.want) || more {
			t.Errorf("%s : %v (encore : %v), attendu %v", name, got, more, c.want)
		}
	}
	// Pagination : before exclu, plus petite hauteur reçue comme curseur suivant.
	var pages [][]uint64
	for before := all; len(pages) < 10; {
		got, more := heights(t, s, f[0].Addr, before, 2)
		pages = append(pages, got)
		if !more {
			break
		}
		before = got[len(got)-1]
	}
	if want := [][]uint64{{5, 4}, {3, 2}, {1}}; !slices.EqualFunc(pages, want, slices.Equal) {
		t.Fatalf("pages : %v", pages)
	}
	if got, more := heights(t, s, f[0].Addr, 1, 10); len(got) != 0 || more {
		t.Fatalf("avant le bloc 1 : %v", got)
	}
	if got, _ := heights(t, s, f[0].Addr, 3, 10); !slices.Equal(got, []uint64{2, 1}) {
		t.Fatalf("avant le bloc 3 : %v", got)
	}
	if h, ok := s.TxHeight(sc.tx.ID); !ok || h != 2 {
		t.Fatalf("transaction : %d %v", h, ok)
	}
	if h, ok := s.TxHeight(sc.tx2.ID); !ok || h != 5 {
		t.Fatalf("2e transaction : %d %v", h, ok)
	}
	if _, ok := s.TxHeight(crypto.Hash{1}); ok {
		t.Fatal("transaction inconnue trouvée")
	}
	if h, ok := s.EscrowOpenHeight(sc.terms.ID()); !ok || h != 3 {
		t.Fatalf("échange : %d %v", h, ok)
	}
	if _, ok := s.EscrowOpenHeight(crypto.Hash{2}); ok {
		t.Fatal("échange inconnu trouvé")
	}
}

// dumpIndex renvoie le contenu des tables de l'index et sa hauteur.
func dumpIndex(t *testing.T, path string) map[string][][2][]byte {
	t.Helper()
	db, err := bolt.Open(path, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	out := map[string][][2][]byte{}
	_ = db.View(func(tx *bolt.Tx) error {
		for _, name := range [][]byte{bucketAddrIndex, bucketTxIndex, bucketEscrowIndex} {
			_ = tx.Bucket(name).ForEach(func(k, v []byte) error {
				out[string(name)] = append(out[string(name)], [2][]byte{append([]byte(nil), k...), append([]byte(nil), v...)})
				return nil
			})
		}
		out["tip"] = [][2][]byte{{tx.Bucket(bucketMeta).Get(keyIndexTip)}}
		return nil
	})
	return out
}

func sameIndex(a, b map[string][][2][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for k, rows := range a {
		if !slices.EqualFunc(rows, b[k], func(x, y [2][]byte) bool { return bytes.Equal(x[0], y[0]) && bytes.Equal(x[1], y[1]) }) {
			return false
		}
	}
	return true
}

// L'index reconstruit à partir des blocs (activé après coup, ou après une interruption) est
// identique, octet pour octet, à l'index tenu bloc par bloc.
func TestIndexRebuildEqualsIncremental(t *testing.T) {
	sc := newIndexScenario(t)
	ref := dbPath(t)
	sc.store(t, ref, archiveIndexed, 5).Close()
	want := dumpIndex(t, ref)
	if len(want["addrindex"]) == 0 || len(want["txindex"]) != 2 || len(want["escrowindex"]) != 2 {
		t.Fatalf("index de référence incomplet : %v", want)
	}

	// Activé après coup sur un nœud archive.
	late := dbPath(t)
	sc.store(t, late, Options{}, 5).Close()
	if got := dumpIndex(t, late); len(got["addrindex"]) != 0 || got["tip"][0][0] != nil {
		t.Fatal("index tenu sans être activé")
	}
	open(t, late, sc.net.P, archiveIndexed).Close()
	if !sameIndex(dumpIndex(t, late), want) {
		t.Fatal("index reconstruit différent")
	}

	// Activé, désactivé pendant deux blocs, réactivé : rattrapage depuis la dernière hauteur indexée.
	gap := dbPath(t)
	sc.store(t, gap, archiveIndexed, 3).Close()
	s := open(t, gap, sc.net.P, Options{})
	sc.append(t, s, 4, 5)
	s.Close()
	s = open(t, gap, sc.net.P, archiveIndexed)
	if h, _ := heights(t, s, sc.buyer.Addr, math.MaxUint64, 10); !slices.Equal(h, []uint64{5, 4, 3}) {
		t.Fatalf("après rattrapage : %v", h)
	}
	s.Close()
	if !sameIndex(dumpIndex(t, gap), want) {
		t.Fatal("index rattrapé différent")
	}
}

func TestIndexRequiresArchive(t *testing.T) {
	sc := newIndexScenario(t)
	if _, err := Open(dbPath(t), sc.net.P, Options{Index: true, Pruning: true, KeepBlocks: 90}); !errors.Is(err, ErrIndexNeedsArchive) {
		t.Fatalf("index sur nœud élagué : %v", err)
	}
	// Base déjà élaguée, rouverte en archive avec l'index : les blocs manquent.
	path := dbPath(t)
	sc.store(t, path, Options{Pruning: true, KeepBlocks: 2}, 5).Close()
	if _, err := Open(path, sc.net.P, archiveIndexed); !errors.Is(err, ErrIndexNeedsArchive) {
		t.Fatalf("base élaguée : %v", err)
	}
	// Sans index : aucune réponse.
	s := sc.store(t, dbPath(t), Options{}, 2)
	defer s.Close()
	if _, _, err := s.AddressHeights(sc.net.Founders[0].Addr, math.MaxUint64, 10); !errors.Is(err, ErrNoIndex) {
		t.Fatalf("sans index : %v", err)
	}
	if _, ok := s.TxHeight(sc.tx.ID); ok {
		t.Fatal("transaction trouvée sans index")
	}
	if _, ok := s.EscrowOpenHeight(sc.terms.ID()); ok {
		t.Fatal("échange trouvé sans index")
	}
}

// Un échange dont l'ouverture n'est pas indexée (base altérée) : le bloc n'est pas indexé à moitié.
func TestIndexCorruptEscrowEntry(t *testing.T) {
	sc := newIndexScenario(t)
	path := dbPath(t)
	s := sc.store(t, path, archiveIndexed, 3)
	s.Close()
	id := sc.terms.ID()
	tamper(t, path, bucketEscrowIndex, id[:], func(v []byte) []byte { return v[:8] })
	s = open(t, path, sc.net.P, archiveIndexed)
	defer s.Close()
	f := sc.blocks[3]
	if err := s.Append(&f.b, &f.c); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("entrée d'échange tronquée : %v", err)
	}
	if s.Tip().Height != 3 {
		t.Fatal("bloc ajouté malgré l'erreur d'index")
	}
}
