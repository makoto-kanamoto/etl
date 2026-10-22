package api

import (
	"encoding/hex"
	"net/http"
	"strconv"
	"time"

	"github.com/makoto-kanamoto/etl/internal/blockchain"
	"github.com/makoto-kanamoto/etl/internal/crypto"
	"github.com/makoto-kanamoto/etl/internal/escrow"
)

const maxBlocks = 100 // blocs listés ou parcourus au plus par requête

func units(v uint64) string { return strconv.FormatUint(v, 10) }

func hexOf(h crypto.Hash) string { return hex.EncodeToString(h[:]) }

func (s *Server) addr(a crypto.Address) string {
	if a.Hash == ([crypto.AddressHashSize]byte{}) {
		return ""
	}
	return a.String()
}

// nextBlockTime : 23:00 UTC du jour, ou du lendemain si l'heure est passée ou le bloc du jour finalisé.
func (s *Server) nextBlockTime(tip blockchain.Header) int64 {
	if s.cfg.NextBlock != nil {
		return s.cfg.NextBlock().Unix()
	}
	now := s.cfg.Now().Unix()
	at := now - now%86400 + int64(s.cfg.Params.BlockTimeOfDay.Seconds())
	if at <= now {
		at += 86400
	}
	for at <= tip.Timestamp {
		at += 86400
	}
	return at
}

type ipcJSON struct {
	Value  uint64 `json:"value"` // indice × 1000
	Period string `json:"period"`
	Source string `json:"source_sha256"`
}

func ipcOf(r blockchain.IPCRecord) ipcJSON {
	return ipcJSON{r.Value, r.Period, hex.EncodeToString(r.SourceSHA256[:])}
}

func (s *Server) status(w http.ResponseWriter, _ *http.Request) {
	tip := s.cfg.Chain.Tip()
	var active int
	var ipc, base blockchain.IPCRecord
	var node, nodeStatus string
	if s.cfg.Engine != nil {
		node = s.addr(s.cfg.Engine.Address())
	}
	s.cfg.Chain.ReadState(func(st *blockchain.State) {
		active, ipc, base = st.ActiveCount(), st.IPC(), st.BaseIPC()
		if node != "" {
			nodeStatus = st.Status(s.cfg.Engine.Address()).String()
		}
	})
	out := map[string]any{
		"version":      s.cfg.Version,
		"chain_id":     s.cfg.Params.ChainID,
		"hrp":          s.cfg.Params.AddressHRP,
		"height":       tip.Height,
		"tip":          hexOf(tip.Hash()),
		"tip_time":     tip.Timestamp,
		"next_block":   s.nextBlockTime(tip),
		"block_time":   int64(s.cfg.Params.BlockTimeOfDay.Seconds()), // heure du bloc quotidien (secondes après 00:00 UTC)
		"active_nodes": active,
		"ipc":          ipcOf(ipc),
		"base_ipc":     ipcOf(base),
		"read_only":    s.cfg.ReadOnly,
		"node": map[string]any{
			"address": node, // vide : nœud passif
			"status":  nodeStatus,
		},
	}
	dv := s.derivedNow()
	out["supply"] = units(dv.supply)
	if node != "" {
		nodeOut := out["node"].(map[string]any)
		nodeOut["participated"], nodeOut["examined"] = dv.participated, dv.examined
		if dv.lastRewardKnown {
			nodeOut["last_reward"] = units(dv.lastReward)
		}
	}
	var issues []string
	if s.cfg.Network != nil {
		in, outPeers := s.cfg.Network.PeerCount()
		out["peers"] = map[string]int{"in": in, "out": outPeers}
		out["halted"] = s.cfg.Network.Halted()
		off, measured := s.cfg.Network.ClockOffset()
		if measured > 0 {
			out["clock_offset_ms"] = off.Milliseconds() // le dashboard avertit au-delà de 2 s
		}
		issues = networkIssues(in+outPeers, out["halted"].(bool), off, measured)
	}
	if age := s.cfg.Now().Sub(time.Unix(tip.Timestamp, 0)); age >= MaxTipAge {
		issues = append(issues, "stale")
	}
	out["health"] = map[string]any{"ok": len(issues) == 0, "issues": append([]string{}, issues...)}
	reply(w, out)
}

// Règle « réseau en bonne santé » affichée sur l'accueil : au moins MinPeers pairs, horloge à moins
// de MaxClockSkew de celle des pairs, nœud non arrêté, dernier bloc de moins de MaxTipAge.
const (
	MinPeers     = 2
	MaxClockSkew = 2 * time.Second
	MaxTipAge    = 25 * time.Hour // un bloc par jour à 23:00 UTC, plus une heure de marge
)

// networkIssues renvoie les critères non remplis côté réseau : "peers", "clock", "halted".
func networkIssues(peers int, halted bool, offset time.Duration, measured int) []string {
	var issues []string
	if peers < MinPeers {
		issues = append(issues, "peers")
	}
	if measured > 0 && (offset > MaxClockSkew || offset < -MaxClockSkew) {
		issues = append(issues, "clock")
	}
	if halted {
		issues = append(issues, "halted")
	}
	return issues
}

type headerJSON struct {
	Height   uint64   `json:"height"`
	Hash     string   `json:"hash"`
	Time     int64    `json:"time"`
	Round    uint8    `json:"round"`
	Proposer string   `json:"proposer"`
	IPC      *ipcJSON `json:"ipc,omitempty"`
}

func (s *Server) headerOf(h blockchain.Header) headerJSON {
	out := headerJSON{Height: h.Height, Hash: hexOf(h.Hash()), Time: h.Timestamp, Round: h.Round, Proposer: s.addr(h.Proposer)}
	if h.IPC != nil {
		r := ipcOf(*h.IPC)
		out.IPC = &r
	}
	return out
}

func (s *Server) blocks(w http.ResponseWriter, r *http.Request) {
	limit := 10
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxBlocks {
			fail(w, http.StatusBadRequest, "limit : entier de 1 à 100")
			return
		}
		limit = n
	}
	tip := s.cfg.Chain.Tip()
	out := []headerJSON{}
	for h := tip.Height; len(out) < limit; h-- {
		hdr, err := s.cfg.Chain.HeaderAt(h)
		if err != nil {
			break
		}
		out = append(out, s.headerOf(hdr))
		if h == 0 {
			break
		}
	}
	reply(w, out)
}

type txJSON struct {
	ID        string `json:"id"`
	From      string `json:"from"`
	To        string `json:"to"`
	Amount    string `json:"amount"`
	Fee       string `json:"fee"`
	Nonce     string `json:"nonce"`
	Timestamp int64  `json:"timestamp"`
	Height    uint64 `json:"height,omitempty"`
}

func (s *Server) txOf(tx *blockchain.Transaction, height uint64) txJSON {
	return txJSON{ID: hexOf(tx.ID), From: s.addr(tx.From), To: s.addr(tx.To), Amount: units(tx.Amount), Fee: units(tx.Fee),
		Nonce: units(tx.Nonce), Timestamp: tx.Timestamp, Height: height}
}

type escrowOpJSON struct {
	ID      string `json:"id"`
	Kind    uint8  `json:"kind"`
	Escrow  string `json:"escrow"`
	Signers uint8  `json:"signers"`
}

func (s *Server) block(w http.ResponseWriter, r *http.Request) {
	height, err := strconv.ParseUint(r.PathValue("height"), 10, 64)
	if err != nil {
		fail(w, http.StatusBadRequest, "hauteur invalide")
		return
	}
	hdr, err := s.cfg.Chain.HeaderAt(height)
	if err != nil {
		fail(w, http.StatusNotFound, "bloc inconnu")
		return
	}
	out := map[string]any{"header": s.headerOf(hdr)}
	b, err := s.cfg.Chain.BlockAt(height)
	if err != nil { // élagué : seul l'en-tête reste
		out["pruned"] = true
		reply(w, out)
		return
	}
	txs := make([]txJSON, len(b.Txs))
	for i := range b.Txs {
		txs[i] = s.txOf(&b.Txs[i], 0)
	}
	ops := make([]escrowOpJSON, len(b.EscrowOps))
	for i := range b.EscrowOps {
		op := &b.EscrowOps[i]
		id := op.Escrow
		if op.Kind == blockchain.EscrowOpen {
			id = op.Terms.ID()
		}
		ops[i] = escrowOpJSON{ID: hexOf(op.ID()), Kind: uint8(op.Kind), Escrow: hexOf(id), Signers: op.Signers}
	}
	out["participants"] = len(b.Participants)
	out["txs"] = txs
	out["escrow_ops"] = ops
	reply(w, out)
}

func (s *Server) parseAddress(w http.ResponseWriter, raw string) (crypto.Address, bool) {
	a, err := crypto.ParseAddress(raw, s.cfg.Params.AddressHRP)
	if err != nil {
		fail(w, http.StatusBadRequest, "adresse invalide")
		return crypto.Address{}, false
	}
	return a, true
}

func (s *Server) account(w http.ResponseWriter, r *http.Request) {
	a, ok := s.parseAddress(w, r.PathValue("address"))
	if !ok {
		return
	}
	var acc blockchain.Account
	var rep blockchain.Reputation
	var status string
	var node blockchain.Node
	var escrows []string
	next := s.nextBlockTime(s.cfg.Chain.Tip())
	var p blockchain.Params
	s.cfg.Chain.ReadState(func(st *blockchain.State) {
		p = st.ParamsAt(s.cfg.Params, next)
		acc, rep, status, node = st.Account(a), st.Reputation(a), st.Status(a).String(), st.Node(a)
		for _, id := range st.EscrowsOf(a) {
			escrows = append(escrows, hexOf(id))
		}
	})
	if escrows == nil {
		escrows = []string{}
	}
	nonce := acc.Nonce
	if s.cfg.Engine != nil {
		nonce = s.cfg.Engine.NextNonce(a) // mempool compris : envois successifs avant le bloc
	}
	out := map[string]any{
		"address":    s.addr(a),
		"balance":    units(acc.Balance),
		"nonce":      units(acc.Nonce),
		"next_nonce": units(nonce),
		"node":       status,
		"sponsor":    s.addr(crypto.Address{HRP: s.cfg.Params.AddressHRP, Hash: node.Sponsor}), // vide : fondateur ou réactivation
		"founder":    node.Founder,
		"reputation": map[string]string{
			"completed":       units(rep.Completed),
			"refunded":        units(rep.Refunded),
			"disputes_opened": units(rep.DisputesOpened),
			"disputes_lost":   units(rep.DisputesLost),
		},
		"escrows": escrows,
	}
	if node.Status == blockchain.StatusAdmitted {
		out["sponsorship"] = sponsorshipOf(node, next/86400, p)
	}
	reply(w, out)
}

// sponsorshipOf : peut-il parrainer au prochain bloc (règle du consensus, Node.CanSponsor), et sinon
// à partir de quand ; détail des trois conditions (ancienneté, délai, interdiction).
func sponsorshipOf(n blockchain.Node, day int64, p blockchain.Params) map[string]any {
	out := map[string]any{
		"eligible":         n.CanSponsor(day, p),
		"from":             n.SponsorFrom(p) * 86400,
		"founder":          n.Founder,
		"seniority_days":   day - n.AdmittedDay,
		"seniority_needed": p.SponsorSeniorityDays,
		"cooldown_days":    p.SponsorCooldownDays,
		"has_sponsored":    n.HasSponsored,
		"ban_until":        n.SponsorBanUntil * 86400,
	}
	if n.HasSponsored {
		out["days_since_last"] = day - n.LastSponsorDay
	}
	return out
}

// history parcourt les blocs conservés (au plus 100, du plus récent au plus ancien) et renvoie les
// transactions de l'adresse. Un historique complet relève de l'explorateur (nœud archive).
func (s *Server) history(w http.ResponseWriter, r *http.Request) {
	a, ok := s.parseAddress(w, r.PathValue("address"))
	if !ok {
		return
	}
	tip := s.cfg.Chain.Tip()
	out := []txJSON{}
	moves := []escrowMove{}
	rewards := []rewardJSON{}
	scanned := 0
	for h := tip.Height; h > 0 && scanned < maxBlocks; h-- {
		b, err := s.cfg.Chain.BlockAt(h)
		if err != nil {
			break // élagué au-delà
		}
		scanned++
		for i := range b.Txs {
			if b.Txs[i].From.Hash == a.Hash || b.Txs[i].To.Hash == a.Hash {
				out = append(out, s.txOf(&b.Txs[i], h))
			}
		}
		for i := range b.EscrowOps {
			moves = append(moves, s.escrowMoves(&b.EscrowOps[i], a, h)...)
		}
		if r, known := s.rewardOf(&b, a); known && r > 0 {
			rewards = append(rewards, rewardJSON{Height: h, Time: b.Header.Timestamp, Amount: units(r)})
		}
	}
	reply(w, map[string]any{"transactions": out, "escrow_movements": moves, "rewards": rewards, "blocks_scanned": scanned})
}

// rewardJSON : part de récompense touchée par l'adresse au bloc Height.
type rewardJSON struct {
	Height uint64 `json:"height"`
	Time   int64  `json:"time"`
	Amount string `json:"amount"`
}

// escrowMove : effet d'une opération d'escrow sur le solde d'une adresse.
type escrowMove struct {
	Height uint64 `json:"height"`
	Escrow string `json:"escrow"`
	Kind   string `json:"kind"`   // lock (ouverture), release, refund, fee (rémunération d'arbitre), fee_back
	Amount string `json:"amount"` // unités, toujours positif
	Out    bool   `json:"out"`    // vrai : débit
}

// escrowMoves calcule les mouvements de solde de a dus à op (bloc height). Les conditions d'un
// échange réglé viennent de l'état ; un échange élagué depuis n'apparaît plus.
func (s *Server) escrowMoves(op *blockchain.EscrowOp, a crypto.Address, height uint64) []escrowMove {
	var t blockchain.EscrowTerms
	id := op.Escrow
	if op.Kind == blockchain.EscrowOpen {
		t, id = op.Terms, op.Terms.ID()
	} else if op.Kind == blockchain.EscrowRelease || op.Kind == blockchain.EscrowRefund {
		var e blockchain.Escrow
		var ok bool
		s.cfg.Chain.ReadState(func(st *blockchain.State) { e, ok = st.Escrow(op.Escrow) })
		if !ok {
			return nil
		}
		t = e.Terms
	} else {
		return nil // « payé », litige : aucun mouvement
	}
	var out []escrowMove
	add := func(who crypto.Address, kind string, amount uint64, debit bool) {
		if who.Hash == a.Hash && amount > 0 {
			out = append(out, escrowMove{height, hexOf(id), kind, units(amount), debit})
		}
	}
	switch op.Kind {
	case blockchain.EscrowOpen:
		add(t.Seller, "lock", t.Amount+t.ArbiterFee+t.Fee, true)
	case blockchain.EscrowRelease, blockchain.EscrowRefund:
		to, kind := t.Buyer, "release"
		if op.Kind == blockchain.EscrowRefund {
			to, kind = t.Seller, "refund"
		}
		add(to, kind, t.Amount, false)
		if op.Signers&blockchain.SignerArbiter != 0 {
			add(t.Arbiter, "fee", t.ArbiterFee, false)
		} else {
			add(t.Seller, "fee_back", t.ArbiterFee, false)
		}
	}
	return out
}

func (s *Server) escrow(w http.ResponseWriter, r *http.Request) {
	raw, err := hex.DecodeString(r.PathValue("id"))
	if err != nil || len(raw) != crypto.HashSize {
		fail(w, http.StatusBadRequest, "identifiant d'échange invalide")
		return
	}
	id := crypto.Hash(raw)
	var e blockchain.Escrow
	var found, arbiterActive bool
	s.cfg.Chain.ReadState(func(st *blockchain.State) {
		e, found = st.Escrow(id)
		arbiterActive = st.Status(e.Terms.Arbiter) != blockchain.StatusExcluded
	})
	if !found {
		fail(w, http.StatusNotFound, "échange inconnu (jamais ouvert, ou réglé depuis plus de 90 jours)")
		return
	}
	day := s.nextBlockTime(s.cfg.Chain.Tip()) / 86400
	required := map[string][]int{} // []int : un []uint8 serait encodé en base64 par encoding/json
	for kind, name := range map[blockchain.EscrowKind]string{blockchain.EscrowPaid: "paid", blockchain.EscrowDispute: "dispute",
		blockchain.EscrowRelease: "release", blockchain.EscrowRefund: "refund"} {
		masks := []int{}
		for _, m := range escrow.Required(e, kind, day, arbiterActive) {
			masks = append(masks, int(m))
		}
		required[name] = masks
	}
	t := e.Terms
	reply(w, map[string]any{
		"id":          hexOf(id),
		"seller":      s.addr(t.Seller),
		"buyer":       s.addr(t.Buyer),
		"arbiter":     s.addr(t.Arbiter),
		"amount":      units(t.Amount),
		"arbiter_fee": units(t.ArbiterFee),
		"fee":         units(t.Fee),
		"days":        t.Days,
		"opened_at":   t.Timestamp,
		"deadline":    e.Deadline * 86400, // début du jour d'échéance (UTC)
		"seq":         e.Seq,
		"paid":        e.Paid,
		"disputed":    e.Disputed,
		"outcome":     []string{"pending", "released", "refunded"}[e.Outcome],
		"closed_day":  e.ClosedDay * 86400,
		"required":    required, // masques de signataires possibles : 1 vendeur, 2 acheteur, 4 arbitre
	})
}

func (s *Server) ipc(w http.ResponseWriter, _ *http.Request) {
	var current, base blockchain.IPCRecord
	var active int
	s.cfg.Chain.ReadState(func(st *blockchain.State) { current, base, active = st.IPC(), st.BaseIPC(), st.ActiveCount() })
	tallies := []map[string]any{}
	if s.cfg.Engine != nil {
		for _, t := range s.cfg.Engine.IPCTallies() {
			tallies = append(tallies, map[string]any{"record": ipcOf(t.Record), "votes": t.Votes})
		}
	}
	out := map[string]any{"current": ipcOf(current), "base": ipcOf(base), "active_nodes": active,
		"quorum": blockchain.Quorum(active), "pending": tallies}
	if s.cfg.Oracle != nil {
		st := s.cfg.Oracle.Status()
		o := map[string]any{"voted": st.Voted, "error": st.LastError}
		if !st.LastAttempt.IsZero() {
			o["last_attempt"] = st.LastAttempt.Unix()
		}
		if !st.NextRead.IsZero() {
			o["next_read"] = st.NextRead.Unix()
		}
		if st.Read != nil {
			o["read"] = ipcOf(*st.Read)
		}
		out["oracle"] = o
	}
	out["history"] = s.derivedNow().ipc
	out["source_url"] = s.cfg.IPCSource
	reply(w, out)
}

func (s *Server) params(w http.ResponseWriter, _ *http.Request) {
	tip := s.cfg.Chain.Tip()
	next := s.nextBlockTime(tip)
	var inForce blockchain.Params
	var pending map[blockchain.ParamID]blockchain.PendingParam
	var active int
	s.cfg.Chain.ReadState(func(st *blockchain.State) {
		inForce, pending, active = st.ParamsAt(s.cfg.Params, next), st.PendingParams(), st.ActiveCount()
	})
	votes := map[blockchain.ParamID][]map[string]any{}
	if s.cfg.Engine != nil {
		for _, t := range s.cfg.Engine.ParamTallies() {
			votes[t.ID] = append(votes[t.ID], map[string]any{"value": units(t.Value), "votes": t.Votes})
		}
	}
	list := []map[string]any{}
	for _, id := range blockchain.GovernableParams() {
		lo, hi := id.Bounds()
		p := map[string]any{"name": id.Name(), "value": units(inForce.Get(id)), "min": units(lo), "max": units(hi)}
		if pp, ok := pending[id]; ok {
			p["pending"] = map[string]any{"value": units(pp.Value), "activation": pp.ActivationDay * 86400}
		}
		if v := votes[id]; v != nil {
			p["votes"] = v
		}
		list = append(list, p)
	}
	reply(w, map[string]any{"params": list, "active_nodes": active, "quorum": blockchain.Quorum(active),
		"vote_days": blockchain.ParamVoteDays, "activation_days": blockchain.ParamActivationDays, "as_of": time.Unix(next, 0).UTC().Unix()})
}
