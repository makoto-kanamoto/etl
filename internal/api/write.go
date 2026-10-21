package api

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

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
