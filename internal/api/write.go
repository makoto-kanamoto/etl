package api

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/makoto-kanamoto/etl/internal/blockchain"
	"github.com/makoto-kanamoto/etl/internal/crypto"
	"github.com/makoto-kanamoto/etl/internal/escrow"
	"github.com/makoto-kanamoto/etl/internal/oracle"
)

// decode lit un corps JSON strict (champs inconnus refusés, un seul objet).
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil || d.More() {
		fail(w, http.StatusBadRequest, "corps JSON invalide")
		return false
	}
	return true
}

func (s *Server) needEngine(w http.ResponseWriter) bool {
	if s.cfg.Engine == nil {
		fail(w, http.StatusServiceUnavailable, "nœud passif : soumission impossible")
		return false
	}
	return true
}

// submitTx : {"tx": encodage réseau en hexadécimal} — transaction signée dans le navigateur.
func (s *Server) submitTx(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Tx string `json:"tx"`
	}
	if !decode(w, r, &body) || !s.needEngine(w) {
		return
	}
	raw, err := hex.DecodeString(body.Tx)
	if err != nil {
		fail(w, http.StatusBadRequest, "transaction : hexadécimal invalide")
		return
	}
	tx, err := blockchain.DecodeTransaction(raw, s.cfg.Params.AddressHRP)
	if err != nil {
		fail(w, http.StatusBadRequest, "transaction illisible")
		return
	}
	if err := s.cfg.Engine.SubmitTransaction(tx); err != nil {
		fail(w, http.StatusConflict, err.Error())
		return
	}
	reply(w, map[string]string{"id": hexOf(tx.ID)})
}

// submitEscrow : {"op": "etl-escrow:…"} — opération d'escrow complète, signée dans le navigateur.
func (s *Server) submitEscrow(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Op string `json:"op"`
	}
	if !decode(w, r, &body) || !s.needEngine(w) {
		return
	}
	op, err := escrow.DecodeText(body.Op, s.cfg.Params.AddressHRP)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.cfg.Engine.SubmitEscrowOp(op); err != nil {
		fail(w, http.StatusConflict, err.Error())
		return
	}
	id := op.Escrow
	if op.Kind == blockchain.EscrowOpen {
		id = op.Terms.ID()
	}
	reply(w, map[string]string{"id": hexOf(op.ID()), "escrow": hexOf(id)})
}

// paramVote : {"param": "fee_bps", "value": "20"} — vote du nœud (clé du nœud).
func (s *Server) paramVote(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Param string `json:"param"`
		Value string `json:"value"`
	}
	if !decode(w, r, &body) || !s.needEngine(w) {
		return
	}
	id, ok := blockchain.ParamByName(body.Param)
	value, err := strconv.ParseUint(body.Value, 10, 64)
	if !ok || err != nil {
		fail(w, http.StatusBadRequest, "paramètre ou valeur invalide")
		return
	}
	if err := s.cfg.Engine.SubmitParamVote(id, value); err != nil {
		fail(w, http.StatusConflict, err.Error())
		return
	}
	reply(w, map[string]string{"param": id.Name(), "value": units(value)})
}

// exclusionVote : {"target": "etl1q…"} — vote d'exclusion Sybil du nœud (décision de l'opérateur).
func (s *Server) exclusionVote(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Target string `json:"target"`
	}
	if !decode(w, r, &body) || !s.needEngine(w) {
		return
	}
	target, err := crypto.ParseAddress(body.Target, s.cfg.Params.AddressHRP)
	if err != nil {
		fail(w, http.StatusBadRequest, "adresse invalide")
		return
	}
	if err := s.cfg.Engine.SubmitExclusionVote(target); err != nil {
		fail(w, http.StatusConflict, err.Error())
		return
	}
	reply(w, map[string]string{"target": s.addr(target)})
}

// ipcRead : {} — relire la source de l'IPC maintenant (jamais de valeur saisie à la main).
func (s *Server) ipcRead(w http.ResponseWriter, r *http.Request) {
	var body struct{}
	if !decode(w, r, &body) {
		return
	}
	if s.cfg.Oracle == nil {
		fail(w, http.StatusServiceUnavailable, "oracle inactif sur ce nœud")
		return
	}
	rec, err := s.cfg.Oracle.ReadNow(r.Context(), s.cfg.Now())
	switch {
	case errors.Is(err, oracle.ErrTooSoon):
		fail(w, http.StatusTooManyRequests, err.Error())
		return
	case err != nil:
		fail(w, http.StatusBadGateway, "lecture de la source échouée : "+err.Error())
		return
	}
	out := map[string]any{"voted": rec != nil}
	if rec != nil {
		out["record"] = ipcOf(*rec)
	}
	reply(w, out)
}

// --- arbitrage : le nœud signe en tant qu'arbitre désigné ---

var escrowKinds = map[string]blockchain.EscrowKind{"paid": blockchain.EscrowPaid, "dispute": blockchain.EscrowDispute,
	"release": blockchain.EscrowRelease, "refund": blockchain.EscrowRefund}

// arbiterOp lit {"op": "etl-escrow:…"}, fait signer le nœud par sign, soumet l'opération complète.
func (s *Server) arbiterOp(w http.ResponseWriter, r *http.Request, sign func(*blockchain.EscrowOp) error) {
	var body struct {
		Op string `json:"op"`
	}
	if !decode(w, r, &body) || !s.needEngine(w) {
		return
	}
	op, err := escrow.DecodeText(body.Op, s.cfg.Params.AddressHRP)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := sign(&op); err != nil {
		fail(w, http.StatusConflict, err.Error())
		return
	}
	if err := s.cfg.Engine.SubmitEscrowOp(op); err != nil {
		fail(w, http.StatusConflict, err.Error())
		return
	}
	id := op.Escrow
	if op.Kind == blockchain.EscrowOpen {
		id = op.Terms.ID()
	}
	reply(w, map[string]string{"op": escrow.EncodeText(&op), "escrow": hexOf(id)})
}

// arbiterCosign : {"op": ouverture signée par le vendeur} — cosignée par le nœud arbitre puis soumise.
func (s *Server) arbiterCosign(w http.ResponseWriter, r *http.Request) {
	s.arbiterOp(w, r, func(op *blockchain.EscrowOp) error {
		if op.Kind != blockchain.EscrowOpen {
			return escrow.ErrNotOpen
		}
		return s.cfg.Engine.CosignEscrow(op)
	})
}

// arbiterSign : {"op": règlement signé par une partie} — complété par le nœud arbitre puis soumis.
func (s *Server) arbiterSign(w http.ResponseWriter, r *http.Request) {
	s.arbiterOp(w, r, func(op *blockchain.EscrowOp) error { return s.cfg.Engine.ArbiterSign(op) })
}

// arbiterAct : {"escrow": hex, "kind": "release"|"refund"} — opération signée par le nœud arbitre,
// à transmettre à la partie qui la complétera.
func (s *Server) arbiterAct(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Escrow string `json:"escrow"`
		Kind   string `json:"kind"`
	}
	if !decode(w, r, &body) || !s.needEngine(w) {
		return
	}
	raw, err := hex.DecodeString(body.Escrow)
	kind, ok := escrowKinds[body.Kind]
	if err != nil || len(raw) != crypto.HashSize || !ok || (kind != blockchain.EscrowRelease && kind != blockchain.EscrowRefund) {
		fail(w, http.StatusBadRequest, "échange ou type d'opération invalide (release ou refund)")
		return
	}
	op, err := s.cfg.Engine.ArbiterAct(kind, crypto.Hash(raw))
	if err != nil {
		fail(w, http.StatusConflict, err.Error())
		return
	}
	reply(w, map[string]string{"op": escrow.EncodeText(&op), "escrow": body.Escrow})
}

// --- registre : candidature, parrainage, réactivation (clé du nœud) ---

// RegistrationPrefix précède une candidature transmise du candidat à son parrain (même forme que
// les opérations d'escrow : encodage canonique en base64url sans remplissage).
const RegistrationPrefix = "etl-registration:"

func encodeRegistration(r *blockchain.Registration) string {
	return RegistrationPrefix + base64.RawURLEncoding.EncodeToString(blockchain.EncodeRegistration(r))
}

func decodeRegistration(s, hrp string) (blockchain.Registration, error) {
	raw, ok := strings.CutPrefix(strings.TrimSpace(s), RegistrationPrefix)
	if !ok {
		return blockchain.Registration{}, errRegistrationText
	}
	b, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if err != nil {
		return blockchain.Registration{}, errRegistrationText
	}
	r, err := blockchain.DecodeRegistration(b, hrp)
	if err != nil {
		return blockchain.Registration{}, errRegistrationText
	}
	return r, nil
}

var errRegistrationText = errors.New("api: texte de candidature invalide")

// registrationRequest : {"sponsor": "etl1q…"} — candidature de ce nœud, signée, à transmettre au parrain.
func (s *Server) registrationRequest(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Sponsor string `json:"sponsor"`
	}
	if !decode(w, r, &body) || !s.needEngine(w) {
		return
	}
	sponsor, err := crypto.ParseAddress(body.Sponsor, s.cfg.Params.AddressHRP)
	if err != nil {
		fail(w, http.StatusBadRequest, "adresse invalide")
		return
	}
	reg, err := s.cfg.Engine.PrepareRegistration(sponsor)
	if err != nil {
		fail(w, http.StatusConflict, err.Error())
		return
	}
	reply(w, map[string]string{"registration": encodeRegistration(&reg)})
}

// registrationSponsor : {"registration": "etl-registration:…"} — contresignée par ce nœud parrain,
// vérifiée pour le prochain bloc, puis soumise.
func (s *Server) registrationSponsor(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Registration string `json:"registration"`
	}
	if !decode(w, r, &body) || !s.needEngine(w) {
		return
	}
	reg, err := decodeRegistration(body.Registration, s.cfg.Params.AddressHRP)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.cfg.Engine.SponsorRegistration(&reg); err != nil {
		fail(w, http.StatusConflict, err.Error())
		return
	}
	if err := s.cfg.Engine.SubmitRegistration(reg); err != nil {
		fail(w, http.StatusConflict, err.Error())
		return
	}
	reply(w, map[string]string{"candidate": s.addr(reg.Candidate)})
}

// registrationReactivate : {} — réactivation de ce nœud inactif, vérifiée puis soumise.
func (s *Server) registrationReactivate(w http.ResponseWriter, r *http.Request) {
	var body struct{}
	if !decode(w, r, &body) || !s.needEngine(w) {
		return
	}
	reg, err := s.cfg.Engine.Reactivate()
	if err == nil {
		err = s.cfg.Engine.SubmitRegistration(reg)
	}
	if err != nil {
		fail(w, http.StatusConflict, err.Error())
		return
	}
	reply(w, map[string]string{"candidate": s.addr(reg.Candidate)})
}
