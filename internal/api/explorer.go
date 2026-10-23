package api

import (
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"

	"github.com/makoto-kanamoto/etl/internal/blockchain"
	"github.com/makoto-kanamoto/etl/internal/crypto"
)

// openedTerms relit les conditions d'un échange dans son bloc d'ouverture (index de l'explorateur).
func (s *Server) openedTerms(id crypto.Hash) (blockchain.EscrowTerms, bool) {
	if s.cfg.Index == nil {
		return blockchain.EscrowTerms{}, false
	}
	h, ok := s.cfg.Index.EscrowOpenHeight(id)
	if !ok {
		return blockchain.EscrowTerms{}, false
	}
	b, err := s.cfg.Chain.BlockAt(h)
	if err != nil {
		return blockchain.EscrowTerms{}, false
	}
	for i := range b.EscrowOps {
		if op := &b.EscrowOps[i]; op.Kind == blockchain.EscrowOpen && op.Terms.ID() == id {
			return op.Terms, true
		}
	}
	return blockchain.EscrowTerms{}, false
}

func parseHash(raw string) (crypto.Hash, bool) {
	b, err := hex.DecodeString(raw)
	if err != nil || len(b) != crypto.HashSize {
		return crypto.Hash{}, false
	}
	return crypto.Hash(b), true
}

// tx : une transaction incluse dans un bloc (index de l'explorateur).
func (s *Server) tx(w http.ResponseWriter, r *http.Request) {
	id, ok := parseHash(r.PathValue("id"))
	if !ok {
		fail(w, http.StatusBadRequest, "identifiant de transaction invalide")
		return
	}
	if s.cfg.Index == nil {
		fail(w, http.StatusNotFound, "recherche de transaction indisponible sur ce nœud (index de l'explorateur absent)")
		return
	}
	h, found := s.cfg.Index.TxHeight(id)
	if !found {
		fail(w, http.StatusNotFound, "transaction inconnue (pas encore incluse dans un bloc ?)")
		return
	}
	b, err := s.cfg.Chain.BlockAt(h)
	if err != nil {
		fail(w, http.StatusInternalServerError, "bloc indexé illisible")
		return
	}
	for i := range b.Txs {
		if b.Txs[i].ID == id {
			reply(w, map[string]any{"tx": s.txOf(&b.Txs[i], h), "block_time": b.Header.Timestamp})
			return
		}
	}
	fail(w, http.StatusInternalServerError, "index de l'explorateur incohérent")
}

// search identifie ce que désigne q : hauteur de bloc, adresse, ou empreinte (bloc, transaction,
// échange). Réponse : {"type": "block"|"address"|"tx"|"escrow", …}.
func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	if q == "" || len(q) > 128 {
		fail(w, http.StatusBadRequest, "recherche vide ou trop longue")
		return
	}
	tip := s.cfg.Chain.Tip()
	if h, err := strconv.ParseUint(q, 10, 64); err == nil {
		if h > tip.Height {
			fail(w, http.StatusNotFound, "aucun bloc à cette hauteur")
			return
		}
		reply(w, map[string]any{"type": "block", "height": h})
		return
	}
	if a, err := crypto.ParseAddress(q, s.cfg.Params.AddressHRP); err == nil {
		reply(w, map[string]any{"type": "address", "address": a.String()})
		return
	}
	id, ok := parseHash(q)
	if !ok {
		fail(w, http.StatusNotFound, "ni hauteur, ni adresse de ce réseau, ni empreinte de 64 caractères hexadécimaux")
		return
	}
	if h, err := s.cfg.Chain.HeightOf(id); err == nil {
		reply(w, map[string]any{"type": "block", "height": h})
		return
	}
	if s.cfg.Index != nil {
		if h, found := s.cfg.Index.TxHeight(id); found {
			reply(w, map[string]any{"type": "tx", "id": hexOf(id), "height": h})
			return
		}
	}
	var inState bool
	s.cfg.Chain.ReadState(func(st *blockchain.State) { _, inState = st.Escrow(id) })
	if inState {
		reply(w, map[string]any{"type": "escrow", "id": hexOf(id)})
		return
	}
	fail(w, http.StatusNotFound, "aucun bloc, transaction ni échange avec cette empreinte")
}
