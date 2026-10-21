package consensus

import (
	"errors"
	"sort"
	"time"

	"github.com/makoto-kanamoto/etl/internal/blockchain"
	"github.com/makoto-kanamoto/etl/internal/crypto"
	"github.com/makoto-kanamoto/etl/internal/escrow"
	"github.com/makoto-kanamoto/etl/internal/network"
)

// Handle implémente network.Handler : appelé une fois par message diffusé, après les vérifications
// sans contexte (décodage, signatures). Le moteur décide selon l'état :
//   - Accept : utile, relayé ;
//   - Ignore : périmé ou sans rôle — ni relayé, ni sanctionné (l'émetteur peut être honnête) ;
//   - Reject : invalide quelle que soit la vue locale — l'émetteur est banni.
func (e *Engine) Handle(m network.Message) network.Verdict {
	hrp := e.cfg.Params.AddressHRP
	switch m.Type {
	case network.MsgTx:
		tx, err := blockchain.DecodeTransaction(m.Payload, hrp)
		if err != nil {
			return network.Reject
		}
		e.refreshPoolParams(e.cfg.Now())
		if e.pool.Add(tx, e.chain, e.cfg.Now()) != nil {
			return network.Ignore // doublon, nonce dépassé, solde… : pas forcément malveillant
		}
		return network.Accept
	case network.MsgParticipation:
		pt, err := blockchain.DecodeParticipation(m.Payload, hrp)
		if err != nil {
			return network.Reject
		}
		return e.onParticipation(pt)
	case network.MsgProposal:
		pr, err := blockchain.DecodeProposal(m.Payload, hrp)
		if err != nil {
			return network.Reject
		}
		return e.onProposal(&pr)
	case network.MsgVote:
		v, err := blockchain.DecodeVote(m.Payload, hrp)
		if err != nil {
			return network.Reject
		}
		return e.onVote(v)
	case network.MsgFinalized:
		e.afterFinalize() // déjà appliqué par le réseau : passer à la hauteur suivante
		return network.Accept
	case network.MsgIPCVote:
		v, err := blockchain.DecodeIPCVote(m.Payload, hrp)
		if err != nil {
			return network.Reject
		}
		return e.onIPCVote(v)
	case network.MsgExclusionVote:
		v, err := blockchain.DecodeExclusionVote(m.Payload, hrp)
		if err != nil {
			return network.Reject
		}
		return e.onExclusionVote(v)
	case network.MsgParamVote:
		v, err := blockchain.DecodeParamVote(m.Payload, hrp)
		if err != nil {
			return network.Reject
		}
		return e.onParamVote(v)
	case network.MsgEscrowOp:
		op, err := blockchain.DecodeEscrowOp(m.Payload, hrp)
		if err != nil {
			return network.Reject
		}
		if e.addEscrowOp(op) != nil {
			return network.Ignore // périmée, déjà appliquée ou en avance sur la chaîne : pas forcément malveillante
		}
		return network.Accept
	case network.MsgRegistration:
		r, err := blockchain.DecodeRegistration(m.Payload, hrp)
		if err != nil {
			return network.Reject
		}
		return e.onRegistration(r)
	}
	return network.Ignore
}

func (e *Engine) broadcast(out []network.Message) {
	for _, m := range out {
		e.net.Broadcast(m)
	}
}

func (e *Engine) onParticipation(pt blockchain.Participation) network.Verdict {
	p, now := e.cfg.Params, e.cfg.Now()
	e.mu.Lock()
	defer e.mu.Unlock()
	hs := e.ensureHeight()
	day := e.dayOf(now)
	start := time.Unix(day, 0)
	if day <= hs.prev.Timestamp || now.Before(start) || !now.Before(start.Add(p.SubmissionWindow)) {
		return network.Ignore // hors fenêtre : périmée ou prématurée
	}
	// Une participation d'un autre jour échoue ici : périmée, pas forcément malveillante.
	if pt.Verify(hs.prev.Hash(), day, e.paramsAt(day)) != nil {
		return network.Ignore
	}
	var role blockchain.Role
	e.chain.ReadState(func(st *blockchain.State) { role = e.roleOf(st, pt.Miner, day) })
	if role == blockchain.RoleNone {
		return network.Ignore
	}
	ds := hs.day(day)
	ds.recv = append(ds.recv, blockchain.Received{Participation: pt, ReceivedAt: now})
	return network.Accept
}

// Refus liés à la vue locale : le proposant peut être honnête. On ne pré-vote pas, on relaie, sans sanction.
func viewDependent(err error) bool {
	return errors.Is(err, blockchain.ErrMissingParticipation) ||
		errors.Is(err, blockchain.ErrEquivocatorIncluded) ||
		errors.Is(err, blockchain.ErrRoundTooEarly)
}

func (e *Engine) onProposal(pr *blockchain.Proposal) network.Verdict {
	p, now := e.cfg.Params, e.cfg.Now()
	e.mu.Lock()
	hs := e.ensureHeight()
	if pr.Block.Header.Height != hs.height || hs.committed {
		e.mu.Unlock()
		return network.Ignore
	}
	rs := hs.round(pr.Round)
	if rs.proposal != nil {
		e.mu.Unlock()
		return network.Ignore // déjà une proposition pour ce tour
	}
	var err error
	e.chain.ReadState(func(st *blockchain.State) {
		err = blockchain.CheckProposal(pr, &hs.prev, st, hs.day(pr.Round.Day).recv, now, p)
	})
	if err != nil && !viewDependent(err) {
		e.mu.Unlock()
		return network.Reject // proposition objectivement invalide : proposant fautif
	}
	h := pr.Block.Header.Hash()
	blk := pr.Block
	hs.blocks[h] = &blk
	if pr.POL != nil && (hs.valid == nil || hs.valid.round.Before(pr.POL.Round)) {
		hs.valid = &lockInfo{round: pr.POL.Round, block: &blk, pol: *pr.POL}
	}
	var out []network.Message
	if err == nil {
		rs.proposal = pr
		out = append(out, e.tryPrevote(hs, pr.Round, now)...)
	}
	out = append(out, e.process(hs, pr.Round, h, now)...)
	e.mu.Unlock()
	e.broadcast(out)
	return network.Accept
}

// tryPrevote (verrou tenu) pré-vote la proposition du tour r si r est le tour en cours, selon les
// règles de verrouillage :
//   - nouveau bloc : si le nœud n'est pas verrouillé, ou verrouillé sur ce bloc ;
//   - re-proposition avec preuve au tour vr : si le verrou est antérieur ou égal à vr, ou porte sur ce bloc.
func (e *Engine) tryPrevote(hs *heightState, r blockchain.RoundID, now time.Time) []network.Message {
	rs := hs.round(r)
	cur, ok := e.currentRound(now)
	if !ok || cur != r || rs.proposal == nil || rs.prevoted || !e.canVote() {
		return nil
	}
	pr := rs.proposal
	h := pr.Block.Header.Hash()
	allowed := hs.locked == nil || hs.locked.block.Header.Hash() == h ||
		(pr.POL != nil && !pr.POL.Round.Before(hs.locked.round))
	if !allowed {
		return nil
	}
	rs.prevoted = true
	if m, ok := e.castVote(hs, blockchain.Prevote, r, &pr.Block.Header); ok {
		return append([]network.Message{m}, e.process(hs, r, h, now)...)
	}
	return nil
}

// canVote (verrou tenu) : clé présente, pas d'arrêt, nœud admis.
func (e *Engine) canVote() bool {
	if e.cfg.Key == nil || e.halted() {
		return false
	}
	admitted := false
	e.chain.ReadState(func(st *blockchain.State) { admitted = st.IsAdmitted(e.me) })
	return admitted
}

// castVote (verrou tenu) enregistre le vote sur disque AVANT de le signer et de le diffuser :
// un nœud ne signe jamais deux votes différents pour (hauteur, jour, tour, type).
func (e *Engine) castVote(hs *heightState, t blockchain.VoteType, r blockchain.RoundID, h *blockchain.Header) (network.Message, bool) {
	if e.chain.RecordVote(t, h.Height, r, h.Hash()) != nil {
		return network.Message{}, false
	}
	v, err := blockchain.SignVote(e.cfg.Key, t, h, r, e.cfg.Params)
	if err != nil {
		return network.Message{}, false
	}
	e.recordVote(hs, v)
	return network.Message{Type: network.MsgVote, Payload: blockchain.EncodeVote(&v)}, true
}

// recordVote (verrou tenu) comptabilise un vote et détecte le double vote (même type, même tour).
func (e *Engine) recordVote(hs *heightState, v blockchain.Vote) {
	k := voteKey{v.Voter.Hash, v.Type, v.Round}
	if first, ok := hs.first[k]; ok && first.BlockHash != v.BlockHash {
		e.veqs[v.Voter.Hash] = blockchain.NewVoteEquivocation(first, v) // incluse dans un prochain bloc
	} else if !ok {
		hs.first[k] = v
	}
	rs := hs.round(v.Round)
	set := rs.prevotes
	if v.Type == blockchain.Precommit {
		set = rs.precommits
	}
	if set[v.BlockHash] == nil {
		set[v.BlockHash] = map[addrKey]blockchain.Vote{}
	}
	set[v.BlockHash][v.Voter.Hash] = v
}

func (e *Engine) onVote(v blockchain.Vote) network.Verdict {
	now := e.cfg.Now()
	e.mu.Lock()
	hs := e.ensureHeight()
	if v.Height != hs.height || hs.committed {
		e.mu.Unlock()
		return network.Ignore
	}
	admitted := false
	e.chain.ReadState(func(st *blockchain.State) { admitted = st.IsAdmitted(v.Voter) })
	rs := hs.round(v.Round)
	set := rs.prevotes
	if v.Type == blockchain.Precommit {
		set = rs.precommits
	}
	if !admitted {
		e.mu.Unlock()
		return network.Ignore
	}
	if _, dup := set[v.BlockHash][v.Voter.Hash]; dup {
		e.mu.Unlock()
		return network.Ignore
	}
	e.recordVote(hs, v)
	out := e.process(hs, v.Round, v.BlockHash, now)
	e.mu.Unlock()
	e.broadcast(out)
	return network.Accept
}

// process (verrou tenu) réagit aux seuils atteints pour le bloc h au tour r :
//   - > 2/3 de pré-votes : bloc « valide » (à re-proposer) ; si r est le tour en cours, se
//     verrouiller (sur disque) puis pré-engager ;
//   - > 2/3 de pré-engagements : certificat, stockage, bloc finalisé à diffuser.
func (e *Engine) process(hs *heightState, r blockchain.RoundID, h crypto.Hash, now time.Time) []network.Message {
	block := hs.blocks[h]
	if block == nil || hs.committed {
		return nil // votes en avance sur la proposition : réexaminés à son arrivée
	}
	p := e.cfg.Params
	rs := hs.round(r)
	var need int
	e.chain.ReadState(func(st *blockchain.State) { need = blockchain.Quorum(st.ActiveCount()) })
	var out []network.Message

	if len(rs.prevotes[h]) >= need {
		pol := blockchain.NewCertificate(blockchain.Prevote, &block.Header, r, votesOf(rs.prevotes[h]), p)
		if hs.valid == nil || hs.valid.round.Before(r) {
			hs.valid = &lockInfo{round: r, block: block, pol: pol}
		}
		if cur, ok := e.currentRound(now); ok && cur == r && !rs.precommitted && e.canVote() {
			rs.precommitted = true
			if e.chain.SaveLock(block, &pol) == nil { // verrou sur disque AVANT le pré-engagement
				hs.locked = &lockInfo{round: r, block: block, pol: pol}
				if m, ok := e.castVote(hs, blockchain.Precommit, r, &block.Header); ok {
					out = append(out, m)
				}
			}
		}
	}

	if len(rs.precommits[h]) >= need {
		cert := blockchain.NewCertificate(blockchain.Precommit, &block.Header, r, votesOf(rs.precommits[h]), p)
		var err error
		e.chain.ReadState(func(st *blockchain.State) { err = blockchain.VerifyCertificate(&cert, &block.Header, st, p) })
		if err == nil && e.chain.Append(block, &cert) == nil {
			hs.committed = true
			out = append(out, network.Message{Type: network.MsgFinalized, Payload: network.EncodeFinalized(block, &cert)})
			e.afterFinalizeLocked()
		}
	}
	return out
}

func votesOf(m map[addrKey]blockchain.Vote) []blockchain.Vote {
	out := make([]blockchain.Vote, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

// TickVotes (appelé par Tick) : pré-vote une proposition arrivée juste avant son tour, et
// réexamine les seuils du tour en cours.
func (e *Engine) tickVotes(now time.Time) {
	e.mu.Lock()
	hs := e.ensureHeight()
	r, ok := e.currentRound(now)
	if !ok || hs.committed {
		e.mu.Unlock()
		return
	}
	out := e.tryPrevote(hs, r, now)
	rs := hs.round(r)
	for h := range rs.prevotes {
		out = append(out, e.process(hs, r, h, now)...)
	}
	e.mu.Unlock()
	e.broadcast(out)
}

func (e *Engine) afterFinalize() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.afterFinalizeLocked()
}

// afterFinalizeLocked purge le mempool et ce qui a été inclus ou est devenu inutile.
func (e *Engine) afterFinalizeLocked() {
	now := e.cfg.Now()
	tipTime := e.chain.Tip().Timestamp // hors de ReadState : pas de verrou de lecture imbriqué
	next := e.nextBlockTime(now)       // idem
	e.refreshPoolParams(now)
	e.pool.Update(e.chain, now)
	e.chain.ReadState(func(st *blockchain.State) {
		for k := range e.veqs {
			if st.Status(e.veqs[k].A.Voter) == blockchain.StatusExcluded {
				delete(e.veqs, k)
			}
		}
		for k, r := range e.regs {
			if (st.Status(r.Candidate) != blockchain.StatusUnknown && st.Status(r.Candidate) != blockchain.StatusInactive) ||
				now.Unix()/86400 >= r.Day+e.cfg.Params.RegistrationDays {
				delete(e.regs, k)
			}
		}
		for rec := range e.ipc {
			if rec.Period <= st.IPC().Period {
				delete(e.ipc, rec)
			}
		}
		for target := range e.ex {
			if st.Node(crypto.Address{Hash: target}).Status == blockchain.StatusExcluded {
				delete(e.ex, target)
			}
		}
		// Votes de paramètres devenus sans objet : changement inscrit (en attente) ou valeur déjà en vigueur.
		pending := st.PendingParams()
		current := st.ParamsAt(e.cfg.Params, tipTime)
		for k := range e.pv {
			if _, busy := pending[k.id]; busy || current.Get(k.id) == k.value {
				delete(e.pv, k)
			}
		}
		// Opérations d'escrow incluses ou devenues inapplicables (séquence avancée, échange réglé, ouverture périmée).
		kept := e.escrowOps[:0]
		for i := range e.escrowOps {
			op := &e.escrowOps[i]
			if st.CheckEscrowOp(op, next, e.cfg.Params) == nil {
				kept = append(kept, *op)
			} else {
				delete(e.escrowIDs, op.ID())
			}
		}
		e.escrowOps = kept
	})
	e.ensureHeight()
}

func (e *Engine) onIPCVote(v blockchain.IPCVote) network.Verdict {
	e.mu.Lock()
	defer e.mu.Unlock()
	ok := false
	e.chain.ReadState(func(st *blockchain.State) { ok = st.IsAdmitted(v.Voter) && v.Record.Period > st.IPC().Period })
	if !ok {
		return network.Ignore
	}
	if e.ipc[v.Record] == nil {
		e.ipc[v.Record] = map[addrKey]blockchain.IPCVote{}
	}
	if _, dup := e.ipc[v.Record][v.Voter.Hash]; dup {
		return network.Ignore
	}
	e.ipc[v.Record][v.Voter.Hash] = v
	return network.Accept
}

func (e *Engine) onExclusionVote(v blockchain.ExclusionVote) network.Verdict {
	today := e.cfg.Now().Unix() / 86400
	e.mu.Lock()
	defer e.mu.Unlock()
	ok := false
	e.chain.ReadState(func(st *blockchain.State) {
		ok = st.IsAdmitted(v.Voter) && st.Status(v.Target) != blockchain.StatusUnknown &&
			st.Status(v.Target) != blockchain.StatusExcluded
	})
	if !ok || v.Day > today+1 || today-v.Day >= blockchain.ExclusionVoteDays {
		return network.Ignore
	}
	t := v.Target.Hash
	if e.ex[t] == nil {
		e.ex[t] = map[addrKey]blockchain.ExclusionVote{}
	}
	if _, dup := e.ex[t][v.Voter.Hash]; dup {
		return network.Ignore
	}
	e.ex[t][v.Voter.Hash] = v
	return network.Accept
}

func (e *Engine) onRegistration(r blockchain.Registration) network.Verdict {
	today := e.cfg.Now().Unix() / 86400
	if today < r.Day-1 || today >= r.Day+e.cfg.Params.RegistrationDays {
		return network.Ignore
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, dup := e.regs[r.Candidate.Hash]; dup {
		return network.Ignore
	}
	e.regs[r.Candidate.Hash] = r
	return network.Accept
}

// --- Points d'entrée pour le wallet, l'oracle et l'opérateur ---

// SubmitTransaction ajoute une transaction au mempool et la diffuse.
func (e *Engine) SubmitTransaction(tx blockchain.Transaction) error {
	// Frais en vigueur pour le prochain bloc (ils peuvent avoir changé par vote).
	e.refreshPoolParams(e.cfg.Now())
	if err := e.pool.Add(tx, e.chain, e.cfg.Now()); err != nil {
		return err
	}
	e.net.Broadcast(network.Message{Type: network.MsgTx, Payload: blockchain.EncodeTransaction(&tx)})
	return nil
}

// SubmitIPCVote signe et diffuse le vote de ce nœud pour un triplet IPC lu à la source (oracle).
func (e *Engine) SubmitIPCVote(rec blockchain.IPCRecord) error {
	if e.cfg.Key == nil {
		return ErrPassive
	}
	v, err := blockchain.SignIPCVote(e.cfg.Key, rec, e.cfg.Params)
	if err != nil {
		return err
	}
	e.onIPCVote(v)
	e.net.Broadcast(network.Message{Type: network.MsgIPCVote, Payload: blockchain.EncodeIPCVote(&v)})
	return nil
}

// SubmitExclusionVote signe et diffuse un vote d'exclusion Sybil (décision de l'opérateur).
func (e *Engine) SubmitExclusionVote(target crypto.Address) error {
	if e.cfg.Key == nil {
		return ErrPassive
	}
	v, err := blockchain.SignExclusionVote(e.cfg.Key, target, e.cfg.Now().Unix()/86400, e.cfg.Params)
	if err != nil {
		return err
	}
	e.onExclusionVote(v)
	e.net.Broadcast(network.Message{Type: network.MsgExclusionVote, Payload: blockchain.EncodeExclusionVote(&v)})
	return nil
}

func (e *Engine) onParamVote(v blockchain.ParamVote) network.Verdict {
	today := e.cfg.Now().Unix() / 86400
	e.mu.Lock()
	defer e.mu.Unlock()
	ok := false
	e.chain.ReadState(func(st *blockchain.State) { ok = st.IsAdmitted(v.Voter) })
	if !ok || v.Day > today+1 || today-v.Day >= blockchain.ParamVoteDays {
		return network.Ignore
	}
	k := paramKey{v.ID, v.Value}
	if e.pv[k] == nil {
		e.pv[k] = map[addrKey]blockchain.ParamVote{}
	}
	if _, dup := e.pv[k][v.Voter.Hash]; dup {
		return network.Ignore
	}
	e.pv[k][v.Voter.Hash] = v
	return network.Accept
}

// SubmitParamVote signe et diffuse le vote de ce nœud pour une nouvelle valeur d'un paramètre
// gouvernable (décision de l'opérateur). Bornes et variation maximale vérifiées d'emblée.
func (e *Engine) SubmitParamVote(id blockchain.ParamID, value uint64) error {
	if e.cfg.Key == nil {
		return ErrPassive
	}
	var current uint64
	tipTime := e.chain.Tip().Timestamp // hors de ReadState : pas de verrou de lecture imbriqué
	e.chain.ReadState(func(st *blockchain.State) { current = st.ParamsAt(e.cfg.Params, tipTime).Get(id) })
	if err := blockchain.CheckParamChange(id, current, value); err != nil {
		return err
	}
	v, err := blockchain.SignParamVote(e.cfg.Key, id, value, e.cfg.Now().Unix()/86400, e.cfg.Params)
	if err != nil {
		return err
	}
	e.onParamVote(v)
	e.net.Broadcast(network.Message{Type: network.MsgParamVote, Payload: blockchain.EncodeParamVote(&v)})
	return nil
}

// ErrEscrowPoolFull : trop d'opérations d'escrow en attente sur ce nœud.
var ErrEscrowPoolFull = errors.New("consensus: trop d'opérations d'escrow en attente")

// addEscrowOp ajoute une opération signée, applicable à la chaîne actuelle, au pool d'attente.
// Une opération qui dépend d'une autre encore en attente (« payé » avant l'inclusion de l'ouverture)
// est refusée : le client la soumet après l'inclusion de la précédente.
func (e *Engine) addEscrowOp(op blockchain.EscrowOp) error {
	if err := op.VerifySignatures(e.cfg.Params); err != nil {
		return err
	}
	next := e.nextBlockTime(e.cfg.Now())
	var err error
	e.chain.ReadState(func(st *blockchain.State) { err = st.CheckEscrowOp(&op, next, e.cfg.Params) })
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	id := op.ID()
	if e.escrowIDs[id] {
		return blockchain.ErrEscrowUnknown // déjà en attente
	}
	if len(e.escrowOps) >= maxEscrowPool {
		return ErrEscrowPoolFull
	}
	e.escrowIDs[id] = true
	e.escrowOps = append(e.escrowOps, op)
	return nil
}

// SubmitEscrowOp ajoute une opération d'escrow signée (wallet, dashboard) et la diffuse.
func (e *Engine) SubmitEscrowOp(op blockchain.EscrowOp) error {
	if err := e.addEscrowOp(op); err != nil {
		return err
	}
	e.net.Broadcast(network.Message{Type: network.MsgEscrowOp, Payload: blockchain.EncodeEscrowOp(&op)})
	return nil
}

// SubmitRegistration diffuse une candidature déjà signée par le candidat (et le parrain).
func (e *Engine) SubmitRegistration(r blockchain.Registration) error {
	if err := r.VerifySignatures(e.cfg.Params); err != nil {
		return err
	}
	e.onRegistration(r)
	e.net.Broadcast(network.Message{Type: network.MsgRegistration, Payload: blockchain.EncodeRegistration(&r)})
	return nil
}

// IPCTally : votes IPC en attente d'inclusion, par triplet.
type IPCTally struct {
	Record blockchain.IPCRecord
	Votes  int
}

// IPCTallies renvoie les votes IPC en attente, du plus soutenu au moins soutenu (page IPC).
func (e *Engine) IPCTallies() []IPCTally {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]IPCTally, 0, len(e.ipc))
	for rec, votes := range e.ipc {
		out = append(out, IPCTally{rec, len(votes)})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Votes != out[j].Votes {
			return out[i].Votes > out[j].Votes
		}
		return out[i].Record.Period+string(out[i].Record.SourceSHA256[:]) < out[j].Record.Period+string(out[j].Record.SourceSHA256[:])
	})
	return out
}

// ParamTally : votes en attente pour une nouvelle valeur d'un paramètre gouvernable.
type ParamTally struct {
	ID    blockchain.ParamID
	Value uint64
	Votes int
}

// ParamTallies renvoie les votes de paramètres en attente, par paramètre puis valeur.
func (e *Engine) ParamTallies() []ParamTally {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]ParamTally, 0, len(e.pv))
	for k, votes := range e.pv {
		out = append(out, ParamTally{k.id, k.value, len(votes)})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ID != out[j].ID {
			return out[i].ID < out[j].ID
		}
		return out[i].Value < out[j].Value
	})
	return out
}

// Address renvoie l'adresse du nœud (vide pour un nœud passif).
func (e *Engine) Address() crypto.Address { return e.me }

// NextNonce renvoie le nonce de la prochaine transaction de a, mempool compris (envois successifs).
func (e *Engine) NextNonce(a crypto.Address) uint64 {
	return e.pool.NextNonce(e.chain, a)
}

// --- Arbitrage d'escrow : l'arbitre est un nœud admis, il signe avec la clé du nœud ---

// CosignEscrow cosigne une ouverture dont ce nœud est l'arbitre désigné, après les vérifications
// d'escrow.Cosign (conditions aux frais en vigueur pour le prochain bloc, signature du vendeur,
// validité pour le prochain bloc).
func (e *Engine) CosignEscrow(op *blockchain.EscrowOp) error {
	if e.cfg.Key == nil {
		return ErrPassive
	}
	now := e.cfg.Now()
	return escrow.Cosign(e.cfg.Key, op, now, e.paramsAt(e.nextBlockTime(now)))
}

// ArbiterSign ajoute la signature de ce nœud, arbitre, à une opération reçue d'une partie (règlement
// 2 sur 3), après vérification contre l'état de l'échange.
func (e *Engine) ArbiterSign(op *blockchain.EscrowOp) error {
	if e.cfg.Key == nil {
		return ErrPassive
	}
	var st blockchain.Escrow
	var ok bool
	e.chain.ReadState(func(s *blockchain.State) { st, ok = s.Escrow(op.Escrow) })
	if !ok {
		return blockchain.ErrEscrowUnknown
	}
	return escrow.AddSignature(e.cfg.Key, op, blockchain.SignerArbiter, op.Escrow, st, e.cfg.Params)
}

// ArbiterAct prépare, signée par ce nœud arbitre, une opération à faire compléter par une partie
// (par exemple une libération que l'acheteur cosignera).
func (e *Engine) ArbiterAct(kind blockchain.EscrowKind, id crypto.Hash) (blockchain.EscrowOp, error) {
	if e.cfg.Key == nil {
		return blockchain.EscrowOp{}, ErrPassive
	}
	var st blockchain.Escrow
	var ok bool
	e.chain.ReadState(func(s *blockchain.State) { st, ok = s.Escrow(id) })
	if !ok {
		return blockchain.EscrowOp{}, blockchain.ErrEscrowUnknown
	}
	if !st.Terms.HasArbiter() || st.Terms.Arbiter.Hash != e.me.Hash {
		return blockchain.EscrowOp{}, escrow.ErrRole
	}
	return escrow.Act(e.cfg.Key, kind, id, st.Seq, blockchain.SignerArbiter, e.cfg.Params)
}

// --- Parrainage et réactivation (clé du nœud) ---

// PrepareRegistration prépare la candidature de ce nœud, signée par lui, à faire contresigner par
// le parrain sponsor (texte « etl-registration: »).
func (e *Engine) PrepareRegistration(sponsor crypto.Address) (blockchain.Registration, error) {
	if e.cfg.Key == nil {
		return blockchain.Registration{}, ErrPassive
	}
	var status blockchain.NodeStatus
	e.chain.ReadState(func(st *blockchain.State) { status = st.Status(e.me) })
	if status != blockchain.StatusUnknown || sponsor.Hash == e.me.Hash || sponsor.HRP != e.cfg.Params.AddressHRP {
		return blockchain.Registration{}, blockchain.ErrRegistrationStatus
	}
	r := blockchain.Registration{Candidate: e.me, Sponsor: sponsor, Day: e.cfg.Now().Unix() / 86400}
	return r, r.SignCandidate(e.cfg.Key, e.cfg.Params.ChainID)
}

// SponsorRegistration contresigne la candidature reçue (ce nœud est le parrain désigné), après avoir
// vérifié qu'elle serait acceptée par le prochain bloc (éligibilité du parrain, statut du candidat).
func (e *Engine) SponsorRegistration(r *blockchain.Registration) error {
	if e.cfg.Key == nil {
		return ErrPassive
	}
	if r.IsReactivation() || r.Sponsor.Hash != e.me.Hash {
		return blockchain.ErrSponsorNotEligible
	}
	if err := r.SignSponsor(e.cfg.Key, e.cfg.Params.ChainID); err != nil {
		return err
	}
	return e.checkRegistration(*r)
}

// Reactivate prépare la réactivation de ce nœud inactif (sans parrain), vérifiée pour le prochain bloc.
func (e *Engine) Reactivate() (blockchain.Registration, error) {
	if e.cfg.Key == nil {
		return blockchain.Registration{}, ErrPassive
	}
	r := blockchain.Registration{Candidate: e.me, Sponsor: crypto.Address{HRP: e.cfg.Params.AddressHRP}, Day: e.cfg.Now().Unix() / 86400}
	if err := r.SignCandidate(e.cfg.Key, e.cfg.Params.ChainID); err != nil {
		return r, err
	}
	return r, e.checkRegistration(r)
}

func (e *Engine) checkRegistration(r blockchain.Registration) error {
	next := e.nextBlockTime(e.cfg.Now()) // hors de ReadState : pas de verrou de lecture imbriqué
	var err error
	e.chain.ReadState(func(st *blockchain.State) { err = st.CheckRegistration(r, next, e.cfg.Params) })
	return err
}
