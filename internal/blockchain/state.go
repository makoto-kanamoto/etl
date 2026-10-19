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

// State est l'état on-chain : comptes (modèle Account) et registre des nœuds.
type State struct {
	accounts map[addrKey]Account
	nodes    registry
}

// NewState renvoie un état vide.
func NewState() *State {
	return &State{accounts: map[addrKey]Account{}, nodes: registry{}}
}

// Account renvoie l'état d'un compte (zéro s'il n'existe pas).
func (s *State) Account(a crypto.Address) Account {
	return s.accounts[a.Hash]
}

// Node renvoie l'entrée d'un nœud dans le registre (StatusUnknown s'il n'y figure pas).
func (s *State) Node(a crypto.Address) Node {
	return s.nodes[a.Hash]
}

// IsAdmitted indique si un nœud est admis et actif : il peut participer, proposer et voter.
func (s *State) IsAdmitted(a crypto.Address) bool {
	return s.nodes[a.Hash].Status == StatusAdmitted
}

// Status renvoie le statut d'un nœud.
func (s *State) Status(a crypto.Address) NodeStatus {
	return s.nodes[a.Hash].Status
}

// ActiveCount renvoie le nombre de nœuds admis et actifs (dénominateur des votes 2/3).
func (s *State) ActiveCount() int {
	return s.nodes.activeCount()
}

// ApplyBlock applique un bloc à l'état. Tout ou rien : en cas d'erreur, l'état est inchangé.
// Le bloc doit avoir été vérifié avant (ValidateBlock).
func (s *State) ApplyBlock(b *Block, p Params) error {
	h := &b.Header
	day := dayOf(h.Timestamp)
	nodes := s.nodes.clone()

	if h.Height == 0 {
		if len(b.Txs) > 0 || len(b.Participants) > 0 || len(b.Registrations) > 0 ||
			len(b.Observations) > 0 || len(b.Equivocations) > 0 {
			return ErrGenesisContent
		}
		if err := nodes.applyFounders(b.Founders, day, p); err != nil {
			return err
		}
		s.nodes = nodes
		return nil
	}

	// Registre : le nombre de nœuds actifs est compté au début du bloc.
	activeBefore := nodes.activeCount()
	if err := nodes.applyEquivocations(b.Equivocations, h.PrevHash, h.Timestamp, p); err != nil {
		return err
	}
	if err := nodes.applyParticipants(b.Participants, day); err != nil {
		return err
	}
	if err := nodes.applyObservations(b.Observations, h.PrevHash, h.Timestamp, p); err != nil {
		return err
	}
	if err := nodes.applyRegistrations(b.Registrations, day, p); err != nil {
		return err
	}
	nodes.applyTransitions(day, p)

	// Comptes
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
		if tx.Timestamp > h.Timestamp {
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

	// Récompense du jour partagée entre tous les participants, sauf si moins de
	// MinActiveNodes nœuds sont actifs ; frais au proposant.
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
	var share uint64
	if activeBefore >= p.MinActiveNodes {
		share = RewardShare(h.Height, len(b.Participants), p)
	}
	for i := range b.Participants {
		if err := credit(b.Participants[i].Miner.Hash, share); err != nil {
			return err
		}
	}
	if err := credit(h.Proposer.Hash, fees); err != nil {
		return err
	}

	for k, a := range changes {
		s.accounts[k] = a
	}
	s.nodes = nodes
	return nil
}
