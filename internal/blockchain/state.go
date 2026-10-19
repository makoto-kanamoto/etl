package blockchain

import (
	"errors"

	"github.com/makoto-kanamoto/etl/internal/crypto"
)

var (
	ErrNonce             = errors.New("blockchain: nonce incorrect")
	ErrInsufficientFunds = errors.New("blockchain: solde insuffisant")
	ErrTxFuture          = errors.New("blockchain: transaction postérieure au bloc")
	ErrGenesisContent    = errors.New("blockchain: le genesis ne contient ni transaction ni participation")
)

// Account est l'état d'un compte.
type Account struct {
	Balance uint64 // en unités
	Nonce   uint64 // nombre de transactions envoyées
}

type addrKey = [crypto.AddressHashSize]byte

// State est l'état de tous les comptes (modèle Account).
type State struct {
	accounts map[addrKey]Account
}

// NewState renvoie un état vide.
func NewState() *State {
	return &State{accounts: map[addrKey]Account{}}
}

// Account renvoie l'état d'un compte (zéro s'il n'existe pas).
func (s *State) Account(a crypto.Address) Account {
	return s.accounts[a.Hash]
}

// ApplyBlock applique un bloc à l'état. Tout ou rien : en cas d'erreur, l'état est inchangé.
// Le bloc doit avoir été vérifié avant (ValidateBlock).
func (s *State) ApplyBlock(b *Block, p Params) error {
	if b.Header.Height == 0 && (len(b.Txs) > 0 || len(b.Participants) > 0) {
		return ErrGenesisContent
	}
	changes := map[addrKey]Account{}
	get := func(k addrKey) Account {
		if a, ok := changes[k]; ok {
			return a
		}
		return s.accounts[k]
	}

	var fees uint64
	for i := range b.Txs {
		tx := &b.Txs[i]
		if err := tx.Verify(p); err != nil {
			return err
		}
		if tx.Timestamp > b.Header.Timestamp {
			return ErrTxFuture
		}
		from := get(tx.From.Hash)
		if tx.Nonce != from.Nonce {
			return ErrNonce
		}
		cost, err := checkedAdd(tx.Amount, tx.Fee)
		if err != nil {
			return err
		}
		if from.Balance < cost {
			return ErrInsufficientFunds
		}
		from.Balance -= cost
		from.Nonce++
		changes[tx.From.Hash] = from

		to := get(tx.To.Hash)
		if to.Balance, err = checkedAdd(to.Balance, tx.Amount); err != nil {
			return err
		}
		changes[tx.To.Hash] = to

		if fees, err = checkedAdd(fees, tx.Fee); err != nil {
			return err
		}
	}

	// Récompense du jour partagée entre tous les participants ; frais au proposant.
	credit := func(k addrKey, amount uint64) error {
		if amount == 0 {
			return nil
		}
		a := get(k)
		var err error
		if a.Balance, err = checkedAdd(a.Balance, amount); err != nil {
			return err
		}
		changes[k] = a
		return nil
	}
	share := RewardShare(b.Header.Height, len(b.Participants), p)
	for i := range b.Participants {
		if err := credit(b.Participants[i].Miner.Hash, share); err != nil {
			return err
		}
	}
	if err := credit(b.Header.Proposer.Hash, fees); err != nil {
		return err
	}

	for k, a := range changes {
		s.accounts[k] = a
	}
	return nil
}
