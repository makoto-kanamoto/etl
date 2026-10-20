// Package consensus est le chef d'orchestre d'un nœud ETL : il relie les règles du protocole
// (blockchain), le stockage, le mempool et le réseau, et déroule le vote en deux temps avec
// verrouillage (Tendermint adapté au rythme quotidien).
//
//	23:00:00 → :30      miner sa participation, la diffuser ; collecter celles des autres
//	tour (jour, r)      le r-ième ticket propose : son bloc verrouillé/valide avec sa preuve,
//	                    sinon un nouveau bloc. Tours de 2 min à partir de 23:00:30, 5 par jour,
//	                    qui CONTINUENT le lendemain tant que la hauteur n'est pas finalisée
//	proposition valide  pré-voter (sauf si verrouillé sur un autre bloc sans preuve plus récente)
//	> 2/3 pré-votes     dans le tour en cours : se verrouiller (sur disque), pré-engager
//	> 2/3 pré-engag.    au même tour : certificat de finalité, stockage, diffusion, purge du mempool
//
// Le moteur avance par Tick(maintenant) : une boucle l'appelle toutes les 100 ms en production,
// une horloge simulée le pilote dans les tests. Il ne garde jamais son verrou interne pendant
// qu'il diffuse. Sans clé, ou si le nœud n'est pas admis, il est passif. Après un arrêt sur conflit
// de finalité (réseau), il cesse de voter et de proposer.
package consensus

import (
	"bytes"
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/makoto-kanamoto/etl/internal/blockchain"
	"github.com/makoto-kanamoto/etl/internal/crypto"
	"github.com/makoto-kanamoto/etl/internal/mempool"
	"github.com/makoto-kanamoto/etl/internal/network"
)

// Chain est la chaîne locale (storage.Store).
type Chain interface {
	Tip() blockchain.Header
	Append(b *blockchain.Block, c *blockchain.Certificate) error
	ReadState(fn func(*blockchain.State))
	Account(a crypto.Address) blockchain.Account
	RecordVote(t blockchain.VoteType, height uint64, r blockchain.RoundID, hash crypto.Hash) error
	SaveLock(b *blockchain.Block, pol *blockchain.Certificate) error
	Lock() (blockchain.Block, blockchain.Certificate, bool)
}

// Broadcaster diffuse un message à tout le réseau (network.Host).
type Broadcaster interface {
	Broadcast(m network.Message)
}

// Config règle le moteur.
type Config struct {
	Params blockchain.Params
	Key    *crypto.PrivateKey // nil : nœud passif (explorateur, archive)
	Now    func() time.Time   // horloge ; time.Now en production
	Halted func() bool        // arrêt sur conflit de finalité (network.Host.Halted) ; nil : jamais
}

var ErrPassive = errors.New("consensus: nœud passif (sans clé)")

type addrKey = [crypto.AddressHashSize]byte

// Engine est le moteur de consensus d'un nœud.
type Engine struct {
	cfg   Config
	chain Chain
	pool  *mempool.Pool
	net   Broadcaster
	me    crypto.Address

	mu   sync.Mutex
	hs   *heightState
	ipc  map[blockchain.IPCRecord]map[addrKey]blockchain.IPCVote
	ex   map[addrKey]map[addrKey]blockchain.ExclusionVote // cible → votant → vote
	regs map[addrKey]blockchain.Registration
	veqs map[addrKey]blockchain.VoteEquivocation       // preuves de double vote en attente d'inclusion
	pv   map[paramKey]map[addrKey]blockchain.ParamVote // votes de paramètres, par (paramètre, valeur)
}

// heightState est l'état de la hauteur en cours de finalisation (tous jours et tours confondus).
type heightState struct {
	height uint64
	prev   blockchain.Header
	days   map[int64]*dayState
	rounds map[blockchain.RoundID]*roundState
	blocks map[crypto.Hash]*blockchain.Block // blocs proposés et objectivement valides
	first  map[voteKey]blockchain.Vote       // premier vote de chaque (votant, type, tour)

	locked *lockInfo // verrou de ce nœud (persisté)
	valid  *lockInfo // dernier bloc ayant recueilli > 2/3 de pré-votes (à re-proposer)

	committed bool
}

type lockInfo struct {
	round blockchain.RoundID
	block *blockchain.Block
	pol   blockchain.Certificate
}

type dayState struct {
	mined bool
	recv  []blockchain.Received
}

type roundState struct {
	proposal     *blockchain.Proposal
	proposed     bool
	prevoted     bool
	precommitted bool
	prevotes     map[crypto.Hash]map[addrKey]blockchain.Vote
	precommits   map[crypto.Hash]map[addrKey]blockchain.Vote
}

type paramKey struct {
	id    blockchain.ParamID
	value uint64
}

// paramsAt (verrou d'état non tenu) renvoie les paramètres en vigueur à la date blockTime.
func (e *Engine) paramsAt(blockTime int64) blockchain.Params {
	var p blockchain.Params
	e.chain.ReadState(func(st *blockchain.State) { p = st.ParamsAt(e.cfg.Params, blockTime) })
	return p
}

// nextBlockTime renvoie la date du prochain bloc à l'heure now : le 23:00 du jour, ou du lendemain
// si celui du jour est déjà finalisé ou passé.
func (e *Engine) nextBlockTime(now time.Time) int64 {
	t := e.dayOf(now)
	if !now.Before(blockchain.RoundStart(t, uint8(e.cfg.Params.MaxRounds), e.cfg.Params)) {
		t += 86400
	}
	if tip := e.chain.Tip(); t <= tip.Timestamp {
		t = tip.Timestamp + 86400
	}
	return t
}

// refreshPoolParams donne au mempool les paramètres (frais) en vigueur pour le prochain bloc.
func (e *Engine) refreshPoolParams(now time.Time) {
	e.pool.SetParams(e.paramsAt(e.nextBlockTime(now)))
}

type voteKey struct {
	voter addrKey
	t     blockchain.VoteType
	round blockchain.RoundID
}

// New crée un moteur.
func New(cfg Config, chain Chain, pool *mempool.Pool, net Broadcaster) *Engine {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	e := &Engine{cfg: cfg, chain: chain, pool: pool, net: net,
		ipc: map[blockchain.IPCRecord]map[addrKey]blockchain.IPCVote{}, ex: map[addrKey]map[addrKey]blockchain.ExclusionVote{},
		regs: map[addrKey]blockchain.Registration{}, veqs: map[addrKey]blockchain.VoteEquivocation{},
		pv: map[paramKey]map[addrKey]blockchain.ParamVote{}}
	if cfg.Key != nil {
		e.me = cfg.Key.PublicKey().Address(cfg.Params.AddressHRP)
	}
	return e
}

// Run appelle Tick toutes les 100 ms jusqu'à l'annulation du contexte.
func (e *Engine) Run(ctx context.Context) {
	t := time.NewTicker(100 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			e.Tick(e.cfg.Now())
		}
	}
}

func (e *Engine) halted() bool { return e.cfg.Halted != nil && e.cfg.Halted() }

// --- Calendrier ---

// dayOf renvoie le 23:00:00 UTC du jour de now.
func (e *Engine) dayOf(now time.Time) int64 {
	const daySec = 86400
	return (now.Unix()/daySec)*daySec + int64(e.cfg.Params.BlockTimeOfDay.Seconds())
}

// currentRound renvoie le tour en cours à l'heure now (faux hors des tours de proposition).
func (e *Engine) currentRound(now time.Time) (blockchain.RoundID, bool) {
	p := e.cfg.Params
	day := e.dayOf(now)
	start := blockchain.RoundStart(day, 0, p)
	if now.Before(start) || !now.Before(blockchain.RoundStart(day, uint8(p.MaxRounds), p)) {
		return blockchain.RoundID{}, false
	}
	return blockchain.RoundID{Day: day, Round: uint8(now.Sub(start) / p.RoundDuration)}, true
}

// ensureHeight (verrou tenu) prépare l'état de la hauteur en cours ; recharge le verrou persisté.
func (e *Engine) ensureHeight() *heightState {
	tip := e.chain.Tip()
	if e.hs != nil && e.hs.height == tip.Height+1 {
		return e.hs
	}
	hs := &heightState{height: tip.Height + 1, prev: tip, days: map[int64]*dayState{},
		rounds: map[blockchain.RoundID]*roundState{}, blocks: map[crypto.Hash]*blockchain.Block{},
		first: map[voteKey]blockchain.Vote{}}
	if b, pol, ok := e.chain.Lock(); ok {
		blk := b
		hs.locked = &lockInfo{round: pol.Round, block: &blk, pol: pol}
		hs.valid = hs.locked
		hs.blocks[blk.Header.Hash()] = &blk
	}
	e.hs = hs
	return hs
}

func (hs *heightState) day(d int64) *dayState {
	if hs.days[d] == nil {
		hs.days[d] = &dayState{}
	}
	return hs.days[d]
}

func (hs *heightState) round(r blockchain.RoundID) *roundState {
	if hs.rounds[r] == nil {
		hs.rounds[r] = &roundState{prevotes: map[crypto.Hash]map[addrKey]blockchain.Vote{},
			precommits: map[crypto.Hash]map[addrKey]blockchain.Vote{}}
	}
	return hs.rounds[r]
}

func (e *Engine) roleOf(st *blockchain.State, a crypto.Address, day int64) blockchain.Role {
	return st.Role(a, day, e.cfg.Params)
}

// --- Tick ---

// Tick fait avancer le cycle à l'heure now : minage de la participation, puis proposition.
func (e *Engine) Tick(now time.Time) {
	if e.halted() || e.cfg.Key == nil {
		return
	}
	e.maybeMine(now)
	e.maybePropose(now)
	e.tickVotes(now)
}

// maybeMine mine la participation du jour (verrou relâché pendant le minage).
func (e *Engine) maybeMine(now time.Time) {
	p := e.cfg.Params
	e.mu.Lock()
	hs := e.ensureHeight()
	day := e.dayOf(now)
	start := time.Unix(day, 0)
	ds := hs.day(day)
	if ds.mined || day <= hs.prev.Timestamp || hs.committed || now.Before(start) || !now.Before(start.Add(p.SubmissionWindow)) {
		e.mu.Unlock()
		return
	}
	var role blockchain.Role
	e.chain.ReadState(func(st *blockchain.State) { role = e.roleOf(st, e.me, day) })
	if role == blockchain.RoleNone {
		e.mu.Unlock()
		return
	}
	ds.mined = true
	prevHash := hs.prev.Hash()
	pDay := e.paramsAt(day) // difficulté en vigueur ce jour-là
	e.mu.Unlock()

	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(start.Add(p.SubmissionWindow).Sub(now)))
	defer cancel()
	pt, err := blockchain.MineParticipation(ctx, e.cfg.Key, prevHash, day, pDay)
	if err != nil {
		return
	}
	e.mu.Lock()
	if e.hs == hs {
		ds.recv = append(ds.recv, blockchain.Received{Participation: pt, ReceivedAt: e.cfg.Now()})
	}
	e.mu.Unlock()
	e.net.Broadcast(network.Message{Type: network.MsgParticipation, Payload: blockchain.EncodeParticipation(&pt)})
}

// maybePropose propose au tour en cours si ce nœud en est le proposant légitime.
func (e *Engine) maybePropose(now time.Time) {
	e.mu.Lock()
	hs := e.ensureHeight()
	r, ok := e.currentRound(now)
	if !ok || hs.committed || r.Day <= hs.prev.Timestamp || hs.round(r).proposed {
		e.mu.Unlock()
		return
	}
	hs.round(r).proposed = true
	// Frais en vigueur ce jour-là : le mempool écarte les transactions devenues invalides.
	e.pool.SetParams(e.paramsAt(r.Day))
	e.pool.Update(e.chain, now)
	var pr *blockchain.Proposal
	e.chain.ReadState(func(st *blockchain.State) { pr = e.buildProposal(st, hs, r) })
	e.mu.Unlock()
	if pr == nil {
		return
	}
	m := network.Message{Type: network.MsgProposal, Payload: blockchain.EncodeProposal(pr)}
	e.net.Broadcast(m)
	e.Handle(m) // le proposant traite sa propre proposition : pré-vote, etc.
}

// buildProposal (verrou tenu) : re-propose le bloc valide (avec sa preuve) s'il y en a un,
// sinon construit un nouveau bloc. Renvoie nil si ce nœud n'est pas le proposant du tour.
func (e *Engine) buildProposal(st *blockchain.State, hs *heightState, r blockchain.RoundID) *blockchain.Proposal {
	p := e.cfg.Params
	pDay := st.ParamsAt(p, r.Day)
	dp := blockchain.Eligible(hs.day(r.Day).recv, &hs.prev, r.Day, pDay, func(a crypto.Address) blockchain.Role { return e.roleOf(st, a, r.Day) })
	if int(r.Round) >= len(dp.Participants) || dp.Participants[r.Round].Miner != e.me {
		return nil
	}
	pr := &blockchain.Proposal{Round: r, Nomination: dp.Participants}
	if hs.valid != nil {
		pol := hs.valid.pol
		pr.Block, pr.POL = *hs.valid.block, &pol
	} else {
		b := e.buildBlock(st, hs, r, dp, pDay)
		if b == nil {
			return nil
		}
		pr.Block = *b
	}
	if pr.Sign(e.cfg.Key, p) != nil {
		return nil
	}
	return pr
}

// buildBlock (verrou tenu) construit un nouveau bloc pour le tour r.
func (e *Engine) buildBlock(st *blockchain.State, hs *heightState, r blockchain.RoundID, dp blockchain.DayParticipations, pDay blockchain.Params) *blockchain.Block {
	p := e.cfg.Params // base : State.PreviewRoot recalcule les paramètres en vigueur
	b := &blockchain.Block{
		Header:        blockchain.Header{Height: hs.height, PrevHash: hs.prev.Hash(), Timestamp: r.Day, Round: r.Round, Proposer: e.me},
		Participants:  dp.Participants,
		Observations:  dp.Observations,
		Equivocations: dp.Equivocations,
		Txs:           e.pool.Select(st, r.Day, pDay.MaxBlockTxs),
	}
	ok := func() bool { _, err := st.PreviewRoot(withRoots(b), p); return err == nil }
	if !ok() { // ne devrait pas arriver : la sélection du mempool est simulée
		b.Txs = nil
		if !ok() {
			return nil
		}
	}
	// Éléments facultatifs : chacun n'est gardé que si le bloc reste valide.
	try := func(add, undo func()) {
		add()
		if !ok() {
			undo()
		}
	}
	for _, reg := range sortedRegs(e.regs) {
		reg := reg
		try(func() { b.Registrations = insertSorted(b.Registrations, reg) },
			func() { b.Registrations = removeReg(b.Registrations, reg) })
	}
	for _, v := range sortedVeqs(e.veqs) {
		v := v
		try(func() { b.VoteEquivocations = append(b.VoteEquivocations, v) },
			func() { b.VoteEquivocations = b.VoteEquivocations[:len(b.VoteEquivocations)-1] })
	}
	if rec, votes := e.ipcQuorum(st); votes != nil {
		try(func() { rr := rec; b.Header.IPC, b.IPCVotes = &rr, votes },
			func() { b.Header.IPC, b.IPCVotes = nil, nil })
	}
	for _, ch := range e.paramQuorums(st, r.Day) {
		ch := ch
		try(func() { b.ParamChanges = append(b.ParamChanges, ch) },
			func() { b.ParamChanges = b.ParamChanges[:len(b.ParamChanges)-1] })
	}
	for _, ex := range e.exclusionQuorums(st, r.Day) {
		ex := ex
		try(func() { b.Exclusions = append(b.Exclusions, ex) },
			func() { b.Exclusions = b.Exclusions[:len(b.Exclusions)-1] })
	}
	root, err := st.PreviewRoot(withRoots(b), p)
	if err != nil {
		return nil
	}
	b.Header.StateRoot = root
	if b.Sign(e.cfg.Key, p.ChainID) != nil {
		return nil
	}
	return b
}

func withRoots(b *blockchain.Block) *blockchain.Block {
	b.Header.TxRoot = blockchain.TxRoot(b.Txs)
	b.Header.ParticipantsRoot = blockchain.ParticipantsRoot(b.Participants)
	b.Header.RegistryRoot = blockchain.RegistryRoot(b.Registrations, b.Observations, b.Equivocations, b.VoteEquivocations)
	b.Header.VotesRoot = blockchain.VotesRoot(b.IPCVotes, b.Exclusions, b.ParamChanges)
	return b
}

// ipcQuorum renvoie un triplet IPC qui a atteint le seuil, avec ses votes triés.
func (e *Engine) ipcQuorum(st *blockchain.State) (blockchain.IPCRecord, []blockchain.IPCVote) {
	need := blockchain.Quorum(st.ActiveCount())
	var recs []blockchain.IPCRecord
	for rec := range e.ipc {
		recs = append(recs, rec)
	}
	sort.Slice(recs, func(i, j int) bool { return recs[i].Period < recs[j].Period })
	for _, rec := range recs {
		if rec.Period <= st.IPC().Period {
			continue
		}
		var votes []blockchain.IPCVote
		for _, v := range e.ipc[rec] {
			if st.IsAdmitted(v.Voter) {
				votes = append(votes, v)
			}
		}
		if len(votes) >= need {
			sort.Slice(votes, func(i, j int) bool { return bytes.Compare(votes[i].Voter.Hash[:], votes[j].Voter.Hash[:]) < 0 })
			return rec, votes
		}
	}
	return blockchain.IPCRecord{}, nil
}

// exclusionQuorums renvoie les exclusions qui ont atteint le seuil, triées par cible.
func (e *Engine) exclusionQuorums(st *blockchain.State, blockTime int64) []blockchain.Exclusion {
	need := blockchain.Quorum(st.ActiveCount())
	today := blockTime / 86400
	var out []blockchain.Exclusion
	for target, byVoter := range e.ex {
		ex := blockchain.Exclusion{}
		for _, v := range byVoter {
			if st.IsAdmitted(v.Voter) && v.Day <= today && today-v.Day < blockchain.ExclusionVoteDays {
				ex.Target, ex.Votes = v.Target, append(ex.Votes, v)
			}
		}
		if len(ex.Votes) >= need && ex.Target.Hash == target && st.Status(ex.Target) != blockchain.StatusExcluded {
			sort.Slice(ex.Votes, func(i, j int) bool { return bytes.Compare(ex.Votes[i].Voter.Hash[:], ex.Votes[j].Voter.Hash[:]) < 0 })
			out = append(out, ex)
		}
	}
	sort.Slice(out, func(i, j int) bool { return bytes.Compare(out[i].Target.Hash[:], out[j].Target.Hash[:]) < 0 })
	return out
}

func sortedRegs(m map[addrKey]blockchain.Registration) []blockchain.Registration {
	out := make([]blockchain.Registration, 0, len(m))
	for _, r := range m {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return bytes.Compare(out[i].Candidate.Hash[:], out[j].Candidate.Hash[:]) < 0 })
	return out
}

func insertSorted(list []blockchain.Registration, r blockchain.Registration) []blockchain.Registration {
	out := append(append([]blockchain.Registration(nil), list...), r)
	sort.Slice(out, func(i, j int) bool { return bytes.Compare(out[i].Candidate.Hash[:], out[j].Candidate.Hash[:]) < 0 })
	return out
}

func removeReg(list []blockchain.Registration, r blockchain.Registration) []blockchain.Registration {
	out := list[:0:0]
	for _, x := range list {
		if x.Candidate != r.Candidate {
			out = append(out, x)
		}
	}
	return out
}

func sortedVeqs(m map[addrKey]blockchain.VoteEquivocation) []blockchain.VoteEquivocation {
	out := make([]blockchain.VoteEquivocation, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return bytes.Compare(out[i].A.Voter.Hash[:], out[j].A.Voter.Hash[:]) < 0 })
	return out
}

// paramQuorums renvoie, par paramètre croissant, une valeur ayant atteint le seuil
// (paramètres sans changement déjà en attente uniquement).
func (e *Engine) paramQuorums(st *blockchain.State, blockTime int64) []blockchain.ParamChange {
	need := blockchain.Quorum(st.ActiveCount())
	today := blockTime / 86400
	pending := st.PendingParams()
	best := map[blockchain.ParamID]blockchain.ParamChange{}
	for k, byVoter := range e.pv {
		if _, busy := pending[k.id]; busy {
			continue
		}
		ch := blockchain.ParamChange{ID: k.id, Value: k.value}
		for _, v := range byVoter {
			if st.IsAdmitted(v.Voter) && v.Day <= today && today-v.Day < blockchain.ParamVoteDays {
				ch.Votes = append(ch.Votes, v)
			}
		}
		if len(ch.Votes) < need {
			continue
		}
		if cur, ok := best[k.id]; !ok || len(ch.Votes) > len(cur.Votes) || (len(ch.Votes) == len(cur.Votes) && ch.Value < cur.Value) {
			sort.Slice(ch.Votes, func(i, j int) bool { return bytes.Compare(ch.Votes[i].Voter.Hash[:], ch.Votes[j].Voter.Hash[:]) < 0 })
			best[k.id] = ch
		}
	}
	out := make([]blockchain.ParamChange, 0, len(best))
	for _, ch := range best {
		out = append(out, ch)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
