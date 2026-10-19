// Package storage conserve la chaîne ETL sur disque (bbolt) : en-têtes, blocs, index et état.
//
// Chaque bloc est écrit dans UNE transaction bbolt (bloc, index, changements d'état, tête,
// pruning) : une coupure en plein milieu ne laisse jamais une chaîne à moitié écrite.
// Seuls les blocs finalisés sont ajoutés : pas de réorganisation de chaîne.
//
// Pruning : les en-têtes sont gardés pour toujours ; le contenu des blocs au-delà des
// KeepBlocks derniers est supprimé (le genesis est toujours conservé). Mode archive : Pruning = false.
package storage

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/makoto-kanamoto/etl/internal/blockchain"
	"github.com/makoto-kanamoto/etl/internal/crypto"
)

var (
	ErrNotFound       = errors.New("storage: bloc introuvable")
	ErrPruned         = errors.New("storage: contenu du bloc élagué (pruning) — interroger un nœud archive")
	ErrWrongNetwork   = errors.New("storage: la base appartient à un autre réseau")
	ErrCorrupt        = errors.New("storage: base corrompue")
	ErrNotInitialized = errors.New("storage: base non initialisée (genesis absent)")
)

// DefaultKeepBlocks : contenu conservé par défaut, 90 blocs (~90 jours).
const DefaultKeepBlocks = 90

// Options règle la conservation des blocs. C'est un choix local à chaque nœud, pas une règle de consensus.
type Options struct {
	Pruning    bool   // false = nœud archive (explorateur, bootstrap)
	KeepBlocks uint64 // contenu conservé quand Pruning est actif
}

// DefaultOptions : pruning actif, 90 blocs conservés.
func DefaultOptions() Options {
	return Options{Pruning: true, KeepBlocks: DefaultKeepBlocks}
}

var (
	bucketMeta     = []byte("meta")
	bucketHeaders  = []byte("headers")  // hauteur → en-tête
	bucketBlocks   = []byte("blocks")   // hauteur → bloc complet (élagué au-delà de KeepBlocks)
	bucketHashes   = []byte("hashes")   // hash → hauteur
	bucketAccounts = []byte("accounts") // adresse → compte
	bucketNodes    = []byte("nodes")    // adresse → entrée du registre

	keyChainID = []byte("chain_id")
	keyGenesis = []byte("genesis")
	keyTip     = []byte("tip")
)

// Store est la chaîne locale. Ses méthodes sont sûres en accès concurrent.
type Store struct {
	mu    sync.RWMutex
	db    *bolt.DB
	p     blockchain.Params
	opt   Options
	state *blockchain.State
	tip   blockchain.Header
	ready bool

	failBeforeCommit func() error // tests : simule une panne au milieu de l'écriture
}

// Open ouvre (ou crée) la base au chemin donné. Répertoire en 0700, fichier en 0600.
// Si la base contient déjà une chaîne, l'état est rechargé et vérifié contre le StateRoot
// du dernier bloc : une base altérée est refusée (ErrCorrupt).
func Open(path string, p blockchain.Params, opt Options) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		return nil, err
	}
	s := &Store{db: db, p: p, opt: opt, state: blockchain.NewState()}
	if err := s.load(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close ferme la base.
func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) load() error {
	return s.db.Update(func(tx *bolt.Tx) error {
		for _, name := range [][]byte{bucketMeta, bucketHeaders, bucketBlocks, bucketHashes, bucketAccounts, bucketNodes} {
			if _, err := tx.CreateBucketIfNotExists(name); err != nil {
				return err
			}
		}
		meta := tx.Bucket(bucketMeta)
		tipBytes := meta.Get(keyTip)
		if tipBytes == nil {
			return nil // base neuve : Init attendu
		}
		if string(meta.Get(keyChainID)) != s.p.ChainID {
			return ErrWrongNetwork
		}
		if len(tipBytes) != 8 {
			return ErrCorrupt
		}
		tip, err := s.headerAt(tx, binary.BigEndian.Uint64(tipBytes))
		if err != nil {
			return err
		}

		accounts := map[[crypto.AddressHashSize]byte]blockchain.Account{}
		err = tx.Bucket(bucketAccounts).ForEach(func(k, v []byte) error {
			a, err := blockchain.DecodeAccount(v)
			if err != nil || len(k) != crypto.AddressHashSize {
				return ErrCorrupt
			}
			accounts[[crypto.AddressHashSize]byte(k)] = a
			return nil
		})
		if err != nil {
			return err
		}
		nodes := map[[crypto.AddressHashSize]byte]blockchain.Node{}
		err = tx.Bucket(bucketNodes).ForEach(func(k, v []byte) error {
			n, err := blockchain.DecodeNode(v)
			if err != nil || len(k) != crypto.AddressHashSize {
				return ErrCorrupt
			}
			nodes[[crypto.AddressHashSize]byte(k)] = n
			return nil
		})
		if err != nil {
			return err
		}
		state := blockchain.NewStateFrom(accounts, nodes)
		if state.Root() != tip.StateRoot {
			return ErrCorrupt
		}
		s.state, s.tip, s.ready = state, tip, true
		return nil
	})
}

// Init inscrit le genesis dans une base neuve. Sur une base existante, vérifie seulement
// que son genesis est bien celui attendu (sinon ErrWrongNetwork).
func (s *Store) Init(genesis *blockchain.Block) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ready {
		var stored []byte
		_ = s.db.View(func(tx *bolt.Tx) error {
			stored = append(stored, tx.Bucket(bucketMeta).Get(keyGenesis)...)
			return nil
		})
		if h := genesis.Header.Hash(); string(stored) != string(h[:]) {
			return ErrWrongNetwork
		}
		return nil
	}
	if genesis.Header.Height != 0 {
		return ErrNotInitialized
	}
	c, err := s.state.Prepare(genesis, s.p)
	if err != nil {
		return err
	}
	err = s.db.Update(func(tx *bolt.Tx) error {
		h := genesis.Header.Hash()
		meta := tx.Bucket(bucketMeta)
		if err := meta.Put(keyChainID, []byte(s.p.ChainID)); err != nil {
			return err
		}
		if err := meta.Put(keyGenesis, h[:]); err != nil {
			return err
		}
		return s.write(tx, genesis, c)
	})
	if err != nil {
		return err
	}
	s.state.Commit(c)
	s.tip, s.ready = genesis.Header, true
	return nil
}

// Append valide un bloc finalisé, l'applique et l'écrit, le tout atomiquement :
// en cas d'erreur, ni la base ni l'état en mémoire ne changent.
func (s *Store) Append(b *blockchain.Block) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ready {
		return ErrNotInitialized
	}
	if err := blockchain.ValidateBlock(b, &s.tip, s.p, s.state.IsAdmitted); err != nil {
		return err
	}
	c, err := s.state.Prepare(b, s.p)
	if err != nil {
		return err
	}
	if err := s.db.Update(func(tx *bolt.Tx) error { return s.write(tx, b, c) }); err != nil {
		return err
	}
	s.state.Commit(c)
	s.tip = b.Header
	return nil
}

// write écrit un bloc et ses effets dans la transaction bbolt en cours.
func (s *Store) write(tx *bolt.Tx, b *blockchain.Block, c *blockchain.Changes) error {
	height := u64(b.Header.Height)
	hash := b.Header.Hash()
	puts := []struct {
		bucket, key, value []byte
	}{
		{bucketHeaders, height, b.Header.Encode()},
		{bucketBlocks, height, blockchain.EncodeBlock(b)},
		{bucketHashes, hash[:], height},
		{bucketMeta, keyTip, height},
	}
	for _, p := range puts {
		if err := tx.Bucket(p.bucket).Put(p.key, p.value); err != nil {
			return err
		}
	}
	accounts, nodes := tx.Bucket(bucketAccounts), tx.Bucket(bucketNodes)
	for k, a := range c.Accounts {
		if err := accounts.Put(k[:], blockchain.AppendAccount(nil, a)); err != nil {
			return err
		}
	}
	for k, n := range c.Nodes {
		if err := nodes.Put(k[:], blockchain.AppendNode(nil, n)); err != nil {
			return err
		}
	}
	for _, k := range c.DeletedNodes {
		if err := nodes.Delete(k[:]); err != nil {
			return err
		}
	}
	if err := s.prune(tx, b.Header.Height); err != nil {
		return err
	}
	if s.failBeforeCommit != nil {
		return s.failBeforeCommit()
	}
	return nil
}

// prune supprime le contenu des blocs 1 … height-KeepBlocks. En-têtes et genesis sont conservés.
func (s *Store) prune(tx *bolt.Tx, height uint64) error {
	if !s.opt.Pruning || height <= s.opt.KeepBlocks {
		return nil
	}
	cutoff := height - s.opt.KeepBlocks
	blocks := tx.Bucket(bucketBlocks)
	// Clés collectées d'abord : supprimer pendant le parcours d'un curseur bbolt peut sauter des clés.
	var old [][]byte
	c := blocks.Cursor()
	for k, _ := c.Seek(u64(1)); k != nil && binary.BigEndian.Uint64(k) <= cutoff; k, _ = c.Next() {
		old = append(old, append([]byte(nil), k...))
	}
	for _, k := range old {
		if err := blocks.Delete(k); err != nil {
			return err
		}
	}
	return nil
}

// Tip renvoie l'en-tête du dernier bloc.
func (s *Store) Tip() blockchain.Header {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.tip
}

// Account renvoie l'état d'un compte.
func (s *Store) Account(a crypto.Address) blockchain.Account {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state.Account(a)
}

// Node renvoie l'entrée d'un nœud dans le registre.
func (s *Store) Node(a crypto.Address) blockchain.Node {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state.Node(a)
}

// ReadState donne un accès en lecture à l'état courant, protégé contre les écritures concurrentes.
// fn ne doit ni modifier l'état ni le conserver après son retour.
func (s *Store) ReadState(fn func(*blockchain.State)) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	fn(s.state)
}

// HeaderAt renvoie l'en-tête à une hauteur donnée (toujours disponible, même élagué).
func (s *Store) HeaderAt(height uint64) (h blockchain.Header, err error) {
	err = s.db.View(func(tx *bolt.Tx) error {
		h, err = s.headerAt(tx, height)
		return err
	})
	return h, err
}

func (s *Store) headerAt(tx *bolt.Tx, height uint64) (blockchain.Header, error) {
	v := tx.Bucket(bucketHeaders).Get(u64(height))
	if v == nil {
		return blockchain.Header{}, ErrNotFound
	}
	h, err := blockchain.DecodeHeader(v, s.p.AddressHRP)
	if err != nil {
		return blockchain.Header{}, ErrCorrupt
	}
	return h, nil
}

// BlockAt renvoie le bloc complet à une hauteur donnée, ou ErrPruned s'il a été élagué.
// Le contenu lu est revérifié contre l'en-tête : une altération du disque est détectée.
func (s *Store) BlockAt(height uint64) (b blockchain.Block, err error) {
	err = s.db.View(func(tx *bolt.Tx) error {
		h, err := s.headerAt(tx, height)
		if err != nil {
			return err
		}
		v := tx.Bucket(bucketBlocks).Get(u64(height))
		if v == nil {
			return ErrPruned
		}
		if b, err = blockchain.DecodeBlock(v, s.p.AddressHRP); err != nil {
			return ErrCorrupt
		}
		if b.Header.Hash() != h.Hash() ||
			b.Header.TxRoot != blockchain.TxRoot(b.Txs) ||
			b.Header.ParticipantsRoot != blockchain.ParticipantsRoot(b.Participants) ||
			b.Header.RegistryRoot != blockchain.RegistryRoot(b.Registrations, b.Observations, b.Equivocations) {
			return ErrCorrupt
		}
		return nil
	})
	return b, err
}

// HeightOf renvoie la hauteur d'un bloc à partir de son hash.
func (s *Store) HeightOf(hash crypto.Hash) (height uint64, err error) {
	err = s.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(bucketHashes).Get(hash[:])
		if v == nil {
			return ErrNotFound
		}
		if len(v) != 8 {
			return ErrCorrupt
		}
		height = binary.BigEndian.Uint64(v)
		return nil
	})
	return height, err
}

func u64(v uint64) []byte { return binary.BigEndian.AppendUint64(nil, v) }
