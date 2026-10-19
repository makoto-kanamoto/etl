package storage

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	bolt "go.etcd.io/bbolt"

	"github.com/makoto-kanamoto/etl/internal/blockchain"
	"github.com/makoto-kanamoto/etl/internal/blockchain/chaintest"
)

func dbPath(t *testing.T) string {
	return filepath.Join(t.TempDir(), "etl", "chain.db")
}

func open(t *testing.T, path string, p blockchain.Params, opt Options) *Store {
	t.Helper()
	s, err := Open(path, p, opt)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// newChain crée une base initialisée et une chaîne de test à laquelle elle est synchronisée.
func newChain(t *testing.T, opt Options) (*Store, *chaintest.Net, string) {
	t.Helper()
	net := chaintest.New(t, chaintest.Params())
	path := dbPath(t)
	s := open(t, path, net.P, opt)
	if err := s.Init(&net.Genesis); err != nil {
		t.Fatal(err)
	}
	return s, net, path
}

// grow ajoute les blocs des jours from … to.
func grow(t *testing.T, s *Store, net *chaintest.Net, from, to int) {
	t.Helper()
	for d := from; d <= to; d++ {
		b, c := net.Next(t, d, nil)
		if err := s.Append(&b, &c); err != nil {
			t.Fatalf("jour %d : %v", d, err)
		}
	}
}

func TestPersistence(t *testing.T) {
	s, net, path := newChain(t, DefaultOptions())
	grow(t, s, net, 1, 5)
	tx := net.Tx(t, net.Founders[0], net.Founders[1].Addr, blockchain.UnitsPerETL/2, 0, chaintest.Day(5))
	b, c := net.Next(t, 6, func(b *blockchain.Block) { b.Txs = []blockchain.Transaction{tx} })
	if err := s.Append(&b, &c); err != nil {
		t.Fatal(err)
	}
	tip := s.Tip()
	s.Close()

	s = open(t, path, net.P, DefaultOptions())
	defer s.Close()
	if err := s.Init(&net.Genesis); err != nil {
		t.Fatalf("réouverture avec le même genesis : %v", err)
	}
	if now := s.Tip(); now.Hash() != tip.Hash() || now.Height != 6 {
		t.Fatal("tête perdue à la réouverture")
	}
	var root [32]byte
	s.ReadState(func(st *blockchain.State) { root = st.Root() })
	if root != net.State.Root() || root != tip.StateRoot {
		t.Fatal("état rechargé différent")
	}
	for _, w := range net.Founders {
		if s.Account(w.Addr) != net.State.Account(w.Addr) || s.Node(w.Addr) != net.State.Node(w.Addr) {
			t.Fatal("compte ou nœud rechargé différent")
		}
	}
	got, err := s.BlockAt(6)
	if err != nil || len(got.Txs) != 1 || got.Txs[0].ID != tx.ID {
		t.Fatalf("bloc 6 relu : %v", err)
	}
	if h, err := s.HeightOf(b.Header.Hash()); err != nil || h != 6 {
		t.Fatalf("index par hash : %d, %v", h, err)
	}
	// La chaîne continue après réouverture.
	grow(t, s, net, 7, 8)
}

func TestWrongNetwork(t *testing.T) {
	s, net, path := newChain(t, DefaultOptions())
	other := chaintest.New(t, net.P) // autre genesis (autres fondateurs)
	if err := s.Init(&other.Genesis); !errors.Is(err, ErrWrongNetwork) {
		t.Fatalf("autre genesis : err = %v", err)
	}
	s.Close()
	p := net.P
	p.ChainID = "ETL-DE"
	if _, err := Open(path, p, DefaultOptions()); !errors.Is(err, ErrWrongNetwork) {
		t.Fatalf("autre chain_id : err = %v", err)
	}
}

func TestNotInitialized(t *testing.T) {
	net := chaintest.New(t, chaintest.Params())
	s := open(t, dbPath(t), net.P, DefaultOptions())
	defer s.Close()
	b, c := net.Next(t, 1, nil)
	if err := s.Append(&b, &c); !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("err = %v", err)
	}
}

// Un bloc refusé ne laisse aucune trace, ni en mémoire ni sur disque.
func TestInvalidBlockLeavesNoTrace(t *testing.T) {
	s, net, path := newChain(t, DefaultOptions())
	grow(t, s, net, 1, 2)
	bad := net.Build(t, 3, nil)
	bad.Header.StateRoot[0] ^= 1
	badCert := net.Certify(t, &bad.Header, 10)
	if err := s.Append(&bad, &badCert); err == nil {
		t.Fatal("bloc invalide accepté")
	}
	if s.Tip().Height != 2 {
		t.Fatal("tête modifiée")
	}
	s.Close()
	s = open(t, path, net.P, DefaultOptions())
	defer s.Close()
	if s.Tip().Height != 2 {
		t.Fatal("bloc invalide écrit sur disque")
	}
	if _, err := s.BlockAt(3); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bloc 3 : err = %v", err)
	}
}

// Panne au milieu de l'écriture : la transaction bbolt est annulée en entier.
func TestCrashDuringWriteIsAtomic(t *testing.T) {
	s, net, path := newChain(t, DefaultOptions())
	grow(t, s, net, 1, 2)
	before := s.Account(net.Founders[0].Addr)

	b, c := net.Next(t, 3, nil)
	s.failBeforeCommit = func() error { return errors.New("coupure simulée") }
	if err := s.Append(&b, &c); err == nil {
		t.Fatal("panne non remontée")
	}
	s.failBeforeCommit = nil
	if s.Tip().Height != 2 || s.Account(net.Founders[0].Addr) != before {
		t.Fatal("état en mémoire modifié malgré la panne")
	}
	s.Close()

	s = open(t, path, net.P, DefaultOptions())
	defer s.Close()
	if s.Tip().Height != 2 || s.Account(net.Founders[0].Addr) != before {
		t.Fatal("écriture partielle sur disque")
	}
	// Le même bloc passe une fois la panne réparée.
	if err := s.Append(&b, &c); err != nil {
		t.Fatal(err)
	}
}

func TestPruning(t *testing.T) {
	opt := Options{Pruning: true, KeepBlocks: 5}
	s, net, _ := newChain(t, opt)
	defer s.Close()
	grow(t, s, net, 1, 12)

	for h := uint64(0); h <= 12; h++ {
		if _, err := s.HeaderAt(h); err != nil {
			t.Fatalf("en-tête %d perdu : %v", h, err)
		}
		_, err := s.BlockAt(h)
		switch {
		case h == 0 && err != nil:
			t.Fatalf("genesis élagué : %v", err)
		case h >= 1 && h <= 7 && !errors.Is(err, ErrPruned):
			t.Fatalf("bloc %d : err = %v, attendu ErrPruned", h, err)
		case h >= 8 && err != nil:
			t.Fatalf("bloc %d récent perdu : %v", h, err)
		}
	}
	if _, err := s.BlockAt(13); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bloc futur : err = %v", err)
	}
}

// Réduire KeepBlocks élague d'un coup tout le contenu devenu trop ancien.
func TestPruningAfterKeepReduced(t *testing.T) {
	s, net, path := newChain(t, Options{Pruning: false})
	grow(t, s, net, 1, 10)
	s.Close()
	s = open(t, path, net.P, Options{Pruning: true, KeepBlocks: 2})
	defer s.Close()
	grow(t, s, net, 11, 11)
	for h := uint64(1); h <= 9; h++ {
		if _, err := s.BlockAt(h); !errors.Is(err, ErrPruned) {
			t.Fatalf("bloc %d : err = %v", h, err)
		}
	}
	for h := uint64(10); h <= 11; h++ {
		if _, err := s.BlockAt(h); err != nil {
			t.Fatalf("bloc %d : %v", h, err)
		}
	}
}

func TestArchiveKeepsEverything(t *testing.T) {
	s, net, _ := newChain(t, Options{Pruning: false})
	defer s.Close()
	grow(t, s, net, 1, 12)
	for h := uint64(0); h <= 12; h++ {
		if _, err := s.BlockAt(h); err != nil {
			t.Fatalf("archive : bloc %d : %v", h, err)
		}
	}
}

// tamper modifie directement la base bbolt, comme le ferait un disque défaillant ou un attaquant.
func tamper(t *testing.T, path string, bucket []byte, key []byte, f func([]byte) []byte) {
	t.Helper()
	db, err := bolt.Open(path, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	err = db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucket)
		return b.Put(key, f(append([]byte(nil), b.Get(key)...)))
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTamperedStateRejected(t *testing.T) {
	s, net, path := newChain(t, DefaultOptions())
	grow(t, s, net, 1, 3)
	s.Close()
	rich := net.Founders[0].Addr.Hash[:]
	tamper(t, path, bucketAccounts, rich, func(v []byte) []byte {
		binary.BigEndian.PutUint64(v, binary.BigEndian.Uint64(v)+1_000_000*blockchain.UnitsPerETL)
		return v
	})
	if _, err := Open(path, net.P, DefaultOptions()); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("solde gonflé à la main : err = %v", err)
	}
}

func TestTamperedBlockDetected(t *testing.T) {
	s, net, path := newChain(t, DefaultOptions())
	tx := net.Tx(t, net.Founders[0], net.Founders[1].Addr, 1000, 0, chaintest.Day(0))
	grow(t, s, net, 1, 1)
	b, c := net.Next(t, 2, func(b *blockchain.Block) { b.Txs = []blockchain.Transaction{tx} })
	if err := s.Append(&b, &c); err != nil {
		t.Fatal(err)
	}
	s.Close()
	// Bloc 2 : montant de la transaction modifié, ré-encodé proprement (seule la racine trahit la fraude).
	tamper(t, path, bucketBlocks, u64(2), func(v []byte) []byte {
		blk, err := blockchain.DecodeBlock(v, net.P.AddressHRP)
		if err != nil {
			t.Fatal(err)
		}
		blk.Txs[0].Amount *= 1000
		return blockchain.EncodeBlock(&blk)
	})
	// Bloc 1 : octet perdu.
	tamper(t, path, bucketBlocks, u64(1), func(v []byte) []byte { return v[:len(v)-1] })
	s = open(t, path, net.P, DefaultOptions())
	defer s.Close()
	for _, h := range []uint64{1, 2} {
		if _, err := s.BlockAt(h); !errors.Is(err, ErrCorrupt) {
			t.Errorf("bloc %d altéré : err = %v", h, err)
		}
	}
}

func TestFilePermissions(t *testing.T) {
	s, _, path := newChain(t, DefaultOptions())
	s.Close()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("fichier : %v, attendu 0600", fi.Mode().Perm())
	}
	di, _ := os.Stat(filepath.Dir(path))
	if di.Mode().Perm() != 0o700 {
		t.Errorf("répertoire : %v, attendu 0700", di.Mode().Perm())
	}
}

// Lectures concurrentes pendant les écritures (lancer avec -race).
func TestConcurrentReads(t *testing.T) {
	s, net, _ := newChain(t, DefaultOptions())
	defer s.Close()
	blocks := make([]blockchain.Block, 0, 10)
	certs := make([]blockchain.Certificate, 0, 10)
	for d := 1; d <= 10; d++ {
		b, c := net.Next(t, d, nil)
		blocks, certs = append(blocks, b), append(certs, c)
	}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = s.Account(net.Founders[0].Addr)
					s.ReadState(func(st *blockchain.State) { _ = st.Root() })
					_, _ = s.BlockAt(s.Tip().Height)
				}
			}
		}()
	}
	for i := range blocks {
		if err := s.Append(&blocks[i], &certs[i]); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
}

// Un en-tête altéré à l'intérieur du bloc stocké est détecté.
func TestTamperedEmbeddedHeaderDetected(t *testing.T) {
	s, net, path := newChain(t, DefaultOptions())
	grow(t, s, net, 1, 1)
	s.Close()
	tamper(t, path, bucketBlocks, u64(1), func(v []byte) []byte {
		blk, err := blockchain.DecodeBlock(v, net.P.AddressHRP)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range net.Founders { // un autre fondateur que le vrai proposant
			if f.Addr != blk.Header.Proposer {
				blk.Header.Proposer = f.Addr
				break
			}
		}
		return blockchain.EncodeBlock(&blk)
	})
	s = open(t, path, net.P, DefaultOptions())
	defer s.Close()
	if _, err := s.BlockAt(1); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("en-tête altéré : err = %v", err)
	}
}

// Une candidature échouée est retirée du registre : la suppression doit être écrite sur disque,
// sinon l'état rechargé ne correspondrait plus au StateRoot.
func TestRegistryDeletionPersisted(t *testing.T) {
	s, net, path := newChain(t, DefaultOptions())
	cand := chaintest.NewWallet(t, net.P)
	b, c := net.Next(t, 1, func(b *blockchain.Block) {
		b.Registrations = []blockchain.Registration{net.Register(t, cand, net.Founders[0], 1)}
	})
	if err := s.Append(&b, &c); err != nil {
		t.Fatal(err)
	}
	if s.Node(cand.Addr).Status != blockchain.StatusCandidate {
		t.Fatal("prémisse : candidat inscrit")
	}
	grow(t, s, net, 8, 8) // fin d'observation sans présence : candidature retirée
	if s.Node(cand.Addr).Status != blockchain.StatusUnknown {
		t.Fatal("prémisse : candidature retirée")
	}
	s.Close()
	s = open(t, path, net.P, DefaultOptions())
	defer s.Close()
	if s.Node(cand.Addr).Status != blockchain.StatusUnknown {
		t.Fatal("candidature retirée réapparue après réouverture")
	}
}

func TestAppendRequiresCertificate(t *testing.T) {
	s, net, _ := newChain(t, DefaultOptions())
	defer s.Close()
	b := net.Build(t, 1, nil)
	six := net.Certify(t, &b.Header, 6) // 6 sur 10 : pas plus de 2/3
	if err := s.Append(&b, &six); !errors.Is(err, blockchain.ErrQuorum) {
		t.Fatalf("6 votes sur 10 : err = %v", err)
	}
	other := net.Build(t, 1, func(b *blockchain.Block) { b.Participants = b.Participants[:9] })
	otherCert := net.Certify(t, &other.Header, 10)
	if err := s.Append(&b, &otherCert); !errors.Is(err, blockchain.ErrCertificateTarget) {
		t.Fatalf("certificat d'un autre bloc : err = %v", err)
	}
	if s.Tip().Height != 0 {
		t.Fatal("bloc non finalisé écrit")
	}
	seven := net.Certify(t, &b.Header, 7)
	if err := s.Append(&b, &seven); err != nil {
		t.Fatalf("7 votes sur 10 : %v", err)
	}
}

func TestCertificatesStoredAndPruned(t *testing.T) {
	s, net, path := newChain(t, Options{Pruning: true, KeepBlocks: 3})
	grow(t, s, net, 1, 6)
	if _, err := s.CertificateAt(0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("certificat du genesis : err = %v", err)
	}
	for h := uint64(1); h <= 3; h++ {
		if _, err := s.CertificateAt(h); !errors.Is(err, ErrPruned) {
			t.Fatalf("certificat %d : err = %v, attendu ErrPruned", h, err)
		}
	}
	s.Close()
	s = open(t, path, net.P, Options{Pruning: true, KeepBlocks: 3})
	defer s.Close()
	for h := uint64(4); h <= 6; h++ {
		c, err := s.CertificateAt(h)
		if err != nil {
			t.Fatalf("certificat %d : %v", h, err)
		}
		hdr, _ := s.HeaderAt(h)
		if c.BlockHash != hdr.Hash() || len(c.Votes) < blockchain.Quorum(10) {
			t.Fatalf("certificat %d incohérent", h)
		}
	}
}

// Même avec KeepBlocks = 0, le dernier bloc et son certificat restent disponibles.
func TestLatestCertificateAlwaysKept(t *testing.T) {
	s, net, _ := newChain(t, Options{Pruning: true, KeepBlocks: 0})
	defer s.Close()
	grow(t, s, net, 1, 4)
	if _, err := s.CertificateAt(4); err != nil {
		t.Fatalf("dernier certificat : %v", err)
	}
	if _, err := s.BlockAt(4); err != nil {
		t.Fatalf("dernier bloc : %v", err)
	}
}

func TestRecordVote(t *testing.T) {
	s, net, path := newChain(t, DefaultOptions())
	b1 := net.Build(t, 1, nil)
	alt := net.Build(t, 1, func(b *blockchain.Block) { b.Participants = b.Participants[:9] })
	h1, hAlt := b1.Header.Hash(), alt.Header.Hash()

	if err := s.RecordVote(1, h1); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordVote(1, h1); err != nil {
		t.Fatalf("rediffusion du même vote : %v", err)
	}
	if err := s.RecordVote(1, hAlt); !errors.Is(err, ErrAlreadyVoted) {
		t.Fatalf("second vote à la même hauteur : err = %v", err)
	}
	s.Close()
	// Après redémarrage, la protection tient toujours.
	s = open(t, path, net.P, DefaultOptions())
	defer s.Close()
	if err := s.RecordVote(1, hAlt); !errors.Is(err, ErrAlreadyVoted) {
		t.Fatalf("après redémarrage : err = %v", err)
	}
	if err := s.RecordVote(2, hAlt); err != nil {
		t.Fatalf("hauteur suivante : %v", err)
	}
	if err := s.RecordVote(1, h1); !errors.Is(err, ErrAlreadyVoted) {
		t.Fatalf("vote pour une hauteur passée : err = %v", err)
	}
}

func TestIPCPersisted(t *testing.T) {
	s, net, path := newChain(t, DefaultOptions())
	q4 := blockchain.IPCRecord{Value: 103500, Period: "2026-Q4", SourceSHA256: [32]byte{1}}
	b, c := net.Next(t, 1, func(b *blockchain.Block) {
		r := q4
		b.Header.IPC, b.IPCVotes = &r, net.IPCVotes(t, q4, 7)
	})
	if err := s.Append(&b, &c); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s = open(t, path, net.P, DefaultOptions())
	var ipc, base blockchain.IPCRecord
	s.ReadState(func(st *blockchain.State) { ipc, base = st.IPC(), st.BaseIPC() })
	if ipc != q4 || base != *net.Genesis.Header.IPC {
		t.Fatalf("IPC rechargé : courant %+v, base %+v", ipc, base)
	}
	s.Close()
	// IPC modifié à la main dans la base : refusé au démarrage.
	tamper(t, path, bucketMeta, keyIPC, func(v []byte) []byte {
		r, _ := blockchain.DecodeIPCRecord(v)
		r.Value *= 2
		return blockchain.AppendIPCRecord(nil, r)
	})
	if _, err := Open(path, net.P, DefaultOptions()); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("IPC falsifié : err = %v", err)
	}
}
