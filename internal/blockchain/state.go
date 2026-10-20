package blockchain

import (
	"bytes"
	"errors"
	"sort"

	"github.com/makoto-kanamoto/etl/internal/crypto"
)

var (
	ErrNonce             = errors.New("blockchain: nonce incorrect")
	ErrInsufficientFunds = errors.New("blockchain: solde insuffisant")
	ErrTxFuture          = errors.New("blockchain: transaction postérieure au bloc")
	ErrGenesisContent    = errors.New("blockchain: le genesis ne contient ni transaction ni participation")
	ErrStateRoot         = errors.New("blockchain: racine d'état incorrecte")
)

// Account est l'état d'un compte.
type Account struct {
	Balance uint64 // en unités
	Nonce   uint64 // nombre de transactions envoyées
}

type addrKey = [crypto.AddressHashSize]byte

// State est l'état on-chain : comptes (modèle Account), registre des nœuds et IPC.
type State struct {
	accounts map[addrKey]Account
	nodes    registry
	baseIPC  IPCRecord // IPC du genesis : 1 ETL = 1 unité de cet IPC (règle immuable n°1)
	gov      govState  // paramètres gouvernables en vigueur et changements en attente
	ipc      IPCRecord // dernier IPC validé par vote 2/3
}

// NewState renvoie un état vide.
func NewState() *State {
	return &State{accounts: map[addrKey]Account{}, nodes: registry{}}
}

// NewStateFrom reconstruit un état chargé depuis le stockage.
// L'appelant doit vérifier Root() contre le StateRoot du dernier bloc.
func NewStateFrom(accounts map[addrKey]Account, nodes map[addrKey]Node, baseIPC, ipc IPCRecord,
	params map[ParamID]uint64, pending map[ParamID]PendingParam) *State {
	if params == nil {
		params = map[ParamID]uint64{}
	}
	if pending == nil {
		pending = map[ParamID]PendingParam{}
	}
	return &State{accounts: accounts, nodes: registry(nodes), baseIPC: baseIPC, ipc: ipc,
		gov: govState{values: params, pending: pending}}
}

// IPC renvoie le dernier IPC validé ; BaseIPC celui du genesis.
// Taux affiché : 1 ETL = IPC().Value ÷ BaseIPC().Value euro.
func (s *State) IPC() IPCRecord     { return s.ipc }
func (s *State) BaseIPC() IPCRecord { return s.baseIPC }

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

// Root renvoie la racine de Merkle de l'état : comptes et registre, triés par adresse.
// Gravée dans chaque en-tête (StateRoot) et validée par le vote 2/3, elle permet à
// quiconque de vérifier un instantané de l'état sans rejouer toute la chaîne.
func (s *State) Root() crypto.Hash {
	return stateRoot(s.accounts, s.nodes, s.baseIPC, s.ipc, s.gov)
}

func stateRoot(accounts map[addrKey]Account, nodes registry, baseIPC, ipc IPCRecord, gov govState) crypto.Hash {
	accLeaves := make([]crypto.Hash, 0, len(accounts))
	for _, k := range sortedKeys(accounts) {
		leaf := append([]byte{'A'}, k[:]...)
		accLeaves = append(accLeaves, crypto.Sum(crypto.ContextState, AppendAccount(leaf, accounts[k])))
	}
	nodeLeaves := make([]crypto.Hash, 0, len(nodes))
	for _, k := range sortedKeys(nodes) {
		leaf := append([]byte{'N'}, k[:]...)
		nodeLeaves = append(nodeLeaves, crypto.Sum(crypto.ContextState, AppendNode(leaf, nodes[k])))
	}
	ipcLeaf := crypto.Sum(crypto.ContextState, appendIPCRecord(appendIPCRecord([]byte{'I'}, &baseIPC), &ipc))
	govLeaf := crypto.Sum(crypto.ContextState, append([]byte{'G'}, gov.encode()...))
	return merkleRoot([]crypto.Hash{merkleRoot(accLeaves), merkleRoot(nodeLeaves), ipcLeaf, govLeaf})
}

func sortedKeys[V any](m map[addrKey]V) []addrKey {
	keys := make([]addrKey, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return bytes.Compare(keys[i][:], keys[j][:]) < 0 })
	return keys
}

// Changes est l'effet d'un bloc sur l'état, calculé sans modifier l'état.
// Le stockage l'écrit sur disque, puis Commit l'applique en mémoire.
type Changes struct {
	Accounts     map[addrKey]Account // comptes modifiés
	Nodes        map[addrKey]Node    // entrées du registre créées ou modifiées
	DeletedNodes []addrKey           // entrées du registre supprimées (candidatures échouées)
	BaseIPC      IPCRecord           // IPC du genesis
	IPC          IPCRecord           // IPC courant après le bloc
	Gov          []byte              // paramètres gouvernables après le bloc (encodés, pour le stockage)
	Root         crypto.Hash         // racine de l'état après le bloc

	nodes registry // registre complet après le bloc
	gov   govState // paramètres après le bloc
}

// ApplyBlock applique un bloc à l'état. Tout ou rien : en cas d'erreur, l'état est inchangé.
// Le bloc doit avoir été vérifié avant (ValidateBlock).
func (s *State) ApplyBlock(b *Block, p Params) error {
	c, err := s.Prepare(b, p)
	if err != nil {
		return err
	}
	s.Commit(c)
	return nil
}

// Prepare calcule l'effet d'un bloc et vérifie qu'il aboutit au StateRoot annoncé.
func (s *State) Prepare(b *Block, p Params) (*Changes, error) {
	c, err := s.prepare(b, p)
	if err != nil {
		return nil, err
	}
	if c.Root != b.Header.StateRoot {
		return nil, ErrStateRoot
	}
	return c, nil
}

// PreviewRoot renvoie la racine d'état qu'aurait l'état après le bloc.
// Sert au proposant pour remplir StateRoot avant de signer.
func (s *State) PreviewRoot(b *Block, p Params) (crypto.Hash, error) {
	c, err := s.prepare(b, p)
	if err != nil {
		return crypto.Hash{}, err
	}
	return c.Root, nil
}

// Commit applique en mémoire des changements préparés par Prepare.
func (s *State) Commit(c *Changes) {
	for k, a := range c.Accounts {
		s.accounts[k] = a
	}
	s.nodes = c.nodes
	s.baseIPC, s.ipc = c.BaseIPC, c.IPC
	s.gov = c.gov
}

func (s *State) prepare(b *Block, p Params) (*Changes, error) {
	h := &b.Header
	day := dayOf(h.Timestamp)
	nodes := s.nodes.clone()
	// Paramètres en vigueur ce jour-là : changements arrivés à échéance activés.
	gov := s.gov.clone()
	if h.Height == 0 {
		gov = newGov(p)
	} else if len(gov.values) > 0 {
		gov.activate(day)
		p = gov.apply(p)
	}
	changes := map[addrKey]Account{}

	if h.Height == 0 {
		if len(b.Txs) > 0 || len(b.Participants) > 0 || len(b.Registrations) > 0 ||
			len(b.Observations) > 0 || len(b.Equivocations) > 0 || len(b.VoteEquivocations) > 0 {
			return nil, ErrGenesisContent
		}
		if err := nodes.applyFounders(b.Founders, day, p); err != nil {
			return nil, err
		}
		if h.IPC == nil || h.IPC.Validate() != nil || len(b.IPCVotes) > 0 || len(b.Exclusions) > 0 {
			return nil, ErrGenesisIPC
		}
		if len(b.ParamChanges) > 0 {
			return nil, ErrGenesisContent
		}
		return s.changes(changes, nodes, *h.IPC, *h.IPC, gov), nil
	}

	// Registre : le nombre de nœuds actifs est compté au début du bloc.
	activeBefore := nodes.activeCount()
	// Nœuds exclus PAR ce bloc : ils peuvent figurer parmi ses participants (le rang du proposant
	// en dépend), mais ne touchent pas leur part et leur présence n'est pas comptée.
	excludedNow := excludedBy(b)
	// Participants vérifiés contre le registre AVANT le bloc.
	if err := nodes.applyParticipants(b.Participants, excludedNow, day); err != nil {
		return nil, err
	}
	if err := nodes.applyEquivocations(b.Equivocations, h.PrevHash, h.Timestamp, p); err != nil {
		return nil, err
	}
	if err := nodes.applyVoteEquivocations(b.VoteEquivocations, h.Height, p); err != nil {
		return nil, err
	}
	// Gouvernance : votants admis et seuil comptés sur le registre AVANT le bloc.
	if err := nodes.applyExclusions(b.Exclusions, s.nodes, day, p); err != nil {
		return nil, err
	}
	if err := gov.applyParamChanges(b.ParamChanges, s.nodes, day, p); err != nil {
		return nil, err
	}
	ipc, err := applyIPC(h, b.IPCVotes, s.ipc, s.nodes, p)
	if err != nil {
		return nil, err
	}
	if err := nodes.applyObservations(b.Observations, h.PrevHash, h.Timestamp, p); err != nil {
		return nil, err
	}
	if err := nodes.applyRegistrations(b.Registrations, day, p); err != nil {
		return nil, err
	}
	nodes.applyTransitions(day, p)

	// Comptes
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
			return nil, err
		}
		if tx.Timestamp > h.Timestamp {
			return nil, ErrTxFuture
		}
		from := get(tx.From.Hash)
		if tx.Nonce != from.Nonce {
			return nil, ErrNonce
		}
		cost, err := checkedAdd(tx.Amount, tx.Fee)
		if err != nil {
			return nil, err
		}
		if from.Balance < cost {
			return nil, ErrInsufficientFunds
		}
		from.Balance -= cost
		from.Nonce++
		changes[tx.From.Hash] = from

		to := get(tx.To.Hash)
		if to.Balance, err = checkedAdd(to.Balance, tx.Amount); err != nil {
			return nil, err
		}
		changes[tx.To.Hash] = to

		if fees, err = checkedAdd(fees, tx.Fee); err != nil {
			return nil, err
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
	rewarded := 0
	for i := range b.Participants {
		if !excludedNow[b.Participants[i].Miner.Hash] {
			rewarded++
		}
	}
	var share uint64
	if activeBefore >= p.MinActiveNodes {
		share = RewardShare(h.Height, rewarded, p)
	}
	for i := range b.Participants {
		if excludedNow[b.Participants[i].Miner.Hash] {
			continue
		}
		if err := credit(b.Participants[i].Miner.Hash, share); err != nil {
			return nil, err
		}
	}
	if err := credit(h.Proposer.Hash, fees); err != nil {
		return nil, err
	}
	return s.changes(changes, nodes, s.baseIPC, ipc, gov), nil
}

// changes assemble le résultat : différences du registre et racine de l'état futur.
func (s *State) changes(accounts map[addrKey]Account, nodes registry, baseIPC, ipc IPCRecord, gov govState) *Changes {
	c := &Changes{Accounts: accounts, Nodes: map[addrKey]Node{}, BaseIPC: baseIPC, IPC: ipc, nodes: nodes, gov: gov}
	for k, n := range nodes {
		if old, ok := s.nodes[k]; !ok || old != n {
			c.Nodes[k] = n
		}
	}
	for k := range s.nodes {
		if _, ok := nodes[k]; !ok {
			c.DeletedNodes = append(c.DeletedNodes, k)
		}
	}
	merged := make(map[addrKey]Account, len(s.accounts)+len(accounts))
	for k, a := range s.accounts {
		merged[k] = a
	}
	for k, a := range accounts {
		merged[k] = a
	}
	c.Root = stateRoot(merged, nodes, baseIPC, ipc, gov)
	c.Gov = gov.encode()
	return c
}
