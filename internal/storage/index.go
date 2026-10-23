package storage

import (
	"encoding/binary"
	"errors"

	bolt "go.etcd.io/bbolt"

	"github.com/makoto-kanamoto/etl/internal/blockchain"
	"github.com/makoto-kanamoto/etl/internal/crypto"
)

// Index de l'explorateur (Options.Index, nœud archive) : hors racine d'état et hors consensus,
// reconstructible à partir des blocs. Il permet l'historique complet d'une adresse et la
// recherche d'une transaction ou d'un échange par identifiant.
//
//	addrindex   : hash d'adresse | hauteur → vide (blocs où l'adresse a un mouvement de solde)
//	txindex     : identifiant de transaction → hauteur
//	escrowindex : identifiant d'échange → hauteur d'ouverture | vendeur | acheteur | arbitre
//	meta/index_tip : dernière hauteur indexée
var (
	bucketAddrIndex   = []byte("addrindex")
	bucketTxIndex     = []byte("txindex")
	bucketEscrowIndex = []byte("escrowindex")

	keyIndexTip = []byte("index_tip")
)

// ErrIndexNeedsArchive : l'index se construit à partir de tous les blocs (nœud archive).
var ErrIndexNeedsArchive = errors.New("storage: l'index de l'explorateur exige un nœud archive (pruning = false)")

// ErrNoIndex : l'index de l'explorateur n'est pas activé sur ce nœud.
var ErrNoIndex = errors.New("storage: index de l'explorateur non activé")

const escrowIndexSize = 8 + 3*crypto.AddressHashSize

// indexBlock indexe le bloc b (part de récompense reward par participant) dans la transaction en cours.
func indexBlock(tx *bolt.Tx, b *blockchain.Block, reward uint64) error {
	height := u64(b.Header.Height)
	addrs, txs, escrows := tx.Bucket(bucketAddrIndex), tx.Bucket(bucketTxIndex), tx.Bucket(bucketEscrowIndex)
	seen := map[[crypto.AddressHashSize]byte]bool{}
	add := func(h [crypto.AddressHashSize]byte) error {
		if h == ([crypto.AddressHashSize]byte{}) || seen[h] {
			return nil
		}
		seen[h] = true
		return addrs.Put(append(h[:], height...), nil)
	}
	for i := range b.Txs {
		t := &b.Txs[i]
		if err := txs.Put(t.ID[:], height); err != nil {
			return err
		}
		if err := add(t.From.Hash); err != nil {
			return err
		}
		if err := add(t.To.Hash); err != nil {
			return err
		}
	}
	for i := range b.EscrowOps {
		op := &b.EscrowOps[i]
		var parties []byte
		if op.Kind == blockchain.EscrowOpen {
			t := &op.Terms
			id := t.ID()
			parties = append(append(append(append([]byte(nil), height...), t.Seller.Hash[:]...), t.Buyer.Hash[:]...), t.Arbiter.Hash[:]...)
			if err := escrows.Put(id[:], parties); err != nil {
				return err
			}
		} else {
			parties = escrows.Get(op.Escrow[:])
			if len(parties) != escrowIndexSize {
				return ErrCorrupt // échange jamais ouvert : impossible dans un bloc validé
			}
		}
		for p := 8; p < escrowIndexSize; p += crypto.AddressHashSize {
			if err := add([crypto.AddressHashSize]byte(parties[p : p+crypto.AddressHashSize])); err != nil {
				return err
			}
		}
	}
	if reward > 0 {
		for i := range b.Participants {
			if a := b.Participants[i].Miner; b.Rewarded(a) {
				if err := add(a.Hash); err != nil {
					return err
				}
			}
		}
	}
	return tx.Bucket(bucketMeta).Put(keyIndexTip, height)
}

// catchUpIndex indexe les blocs postérieurs à index_tip (index activé après coup, ou base neuve).
func (s *Store) catchUpIndex(tx *bolt.Tx, tip uint64) error {
	from := uint64(0)
	if v := tx.Bucket(bucketMeta).Get(keyIndexTip); v != nil {
		if len(v) != 8 {
			return ErrCorrupt
		}
		from = binary.BigEndian.Uint64(v) + 1
	}
	for h := from; h <= tip; h++ {
		v := tx.Bucket(bucketBlocks).Get(u64(h))
		if v == nil {
			return ErrIndexNeedsArchive
		}
		b, err := blockchain.DecodeBlock(v, s.p.AddressHRP)
		if err != nil {
			return ErrCorrupt
		}
		var reward uint64
		if r := tx.Bucket(bucketRewards).Get(u64(h)); len(r) == 8 {
			reward = binary.BigEndian.Uint64(r)
		}
		if err := indexBlock(tx, &b, reward); err != nil {
			return err
		}
	}
	return nil
}

// AddressHeights renvoie, de la plus haute à la plus basse, au plus limit hauteurs de blocs
// strictement inférieures à before où l'adresse a un mouvement de solde, et s'il en reste d'autres.
func (s *Store) AddressHeights(a crypto.Address, before uint64, limit int) (heights []uint64, more bool, err error) {
	if !s.opt.Index {
		return nil, false, ErrNoIndex
	}
	err = s.db.View(func(tx *bolt.Tx) error {
		c := tx.Bucket(bucketAddrIndex).Cursor()
		prefix := a.Hash[:]
		k, _ := c.Seek(append(append([]byte(nil), prefix...), u64(before)...))
		if k == nil {
			k, _ = c.Last()
		} else {
			k, _ = c.Prev()
		}
		for ; k != nil && len(k) == len(prefix)+8 && string(k[:len(prefix)]) == string(prefix); k, _ = c.Prev() {
			if len(heights) == limit {
				more = true
				break
			}
			heights = append(heights, binary.BigEndian.Uint64(k[len(prefix):]))
		}
		return nil
	})
	return heights, more, err
}

// TxHeight renvoie la hauteur du bloc qui contient la transaction id.
func (s *Store) TxHeight(id crypto.Hash) (height uint64, ok bool) {
	if !s.opt.Index {
		return 0, false
	}
	_ = s.db.View(func(tx *bolt.Tx) error {
		if v := tx.Bucket(bucketTxIndex).Get(id[:]); len(v) == 8 {
			height, ok = binary.BigEndian.Uint64(v), true
		}
		return nil
	})
	return height, ok
}

// EscrowOpenHeight renvoie la hauteur du bloc qui a ouvert l'échange id.
func (s *Store) EscrowOpenHeight(id crypto.Hash) (height uint64, ok bool) {
	if !s.opt.Index {
		return 0, false
	}
	_ = s.db.View(func(tx *bolt.Tx) error {
		if v := tx.Bucket(bucketEscrowIndex).Get(id[:]); len(v) == escrowIndexSize {
			height, ok = binary.BigEndian.Uint64(v[:8]), true
		}
		return nil
	})
	return height, ok
}
