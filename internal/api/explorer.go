package api

import (
	"encoding/hex"
	"encoding/json"
	"math"
	"net/http"
	"slices"
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

// notFound : recherche sans résultat, avec son motif (affiché par l'interface) :
//   - "future_block" : hauteur au-delà du dernier bloc (tip : dernière hauteur) ;
//   - "other_network" : adresse valide d'un autre réseau (hrp : son préfixe) ;
//   - "short_hash" : suite hexadécimale de moins de 64 caractères (length : sa longueur) ;
//   - "unknown_hash" : empreinte complète inconnue (transaction pas encore dans un bloc ?) ;
//   - "unrecognized" : ni hauteur, ni adresse, ni empreinte.
func notFound(w http.ResponseWriter, msg, reason string, extra map[string]any) {
	out := map[string]any{"error": msg, "reason": reason}
	for k, v := range extra {
		out[k] = v
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNotFound)
	_ = json.NewEncoder(w).Encode(out)
}

func isHex(q string) bool {
	for _, c := range q {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// search identifie ce que désigne q : hauteur de bloc, adresse, ou empreinte (bloc, transaction,
// échange). Réponse : {"type": "block"|"address"|"tx"|"escrow", …} ; sans résultat : 404 et motif.
func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	if q == "" || len(q) > 128 {
		fail(w, http.StatusBadRequest, "recherche vide ou trop longue")
		return
	}
	tip := s.cfg.Chain.Tip()
	if h, err := strconv.ParseUint(q, 10, 64); err == nil {
		if h > tip.Height {
			notFound(w, "aucun bloc à cette hauteur", "future_block", map[string]any{"tip": tip.Height})
			return
		}
		reply(w, map[string]any{"type": "block", "height": h})
		return
	}
	if a, err := crypto.ParseAddress(q, s.cfg.Params.AddressHRP); err == nil {
		reply(w, map[string]any{"type": "address", "address": a.String()})
		return
	}
	if hrp, ok := crypto.AddressHRP(q); ok {
		notFound(w, "adresse d'un autre réseau", "other_network", map[string]any{"hrp": hrp})
		return
	}
	id, ok := parseHash(q)
	if !ok {
		if isHex(q) && len(q) < 2*crypto.HashSize {
			notFound(w, "empreinte incomplète", "short_hash", map[string]any{"length": len(q)})
			return
		}
		notFound(w, "ni hauteur, ni adresse de ce réseau, ni empreinte de 64 caractères hexadécimaux", "unrecognized", nil)
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
	if !inState && s.cfg.Index != nil {
		_, inState = s.cfg.Index.EscrowOpenHeight(id) // échange réglé et archivé
	}
	if inState {
		reply(w, map[string]any{"type": "escrow", "id": hexOf(id)})
		return
	}
	notFound(w, "aucun bloc, transaction ni échange avec cette empreinte", "unknown_hash", nil)
}

var escrowKindNames = map[blockchain.EscrowKind]string{blockchain.EscrowOpen: "open", blockchain.EscrowPaid: "paid",
	blockchain.EscrowDispute: "dispute", blockchain.EscrowRelease: "release", blockchain.EscrowRefund: "refund"}

// escrowStep : une opération de l'échange inscrite dans un bloc.
type escrowStep struct {
	Kind    string `json:"kind"`
	Height  uint64 `json:"height"`
	Time    int64  `json:"time"`    // heure du bloc
	Signers uint8  `json:"signers"` // masque : 1 vendeur, 2 acheteur, 4 arbitre
}

// escrowSteps retrouve, grâce à l'index, les opérations de l'échange id, de l'ouverture au règlement :
// elles figurent toutes dans des blocs où le vendeur a un mouvement indexé.
// stepsPage : blocs lus par page d'index (variable pour les tests).
var stepsPage = maxBlocks

func (s *Server) escrowSteps(id crypto.Hash, seller crypto.Address) ([]escrowStep, bool) {
	open, ok := s.cfg.Index.EscrowOpenHeight(id)
	if !ok {
		return nil, false
	}
	var steps []escrowStep
	for before := uint64(math.MaxUint64); ; {
		heights, more, err := s.cfg.Index.AddressHeights(seller, before, stepsPage)
		if err != nil {
			return nil, false
		}
		for _, h := range heights {
			if h < open {
				more = false
				break
			}
			b, err := s.cfg.Chain.BlockAt(h)
			if err != nil {
				return nil, false
			}
			for i := len(b.EscrowOps) - 1; i >= 0; i-- {
				op := &b.EscrowOps[i]
				if (op.Kind == blockchain.EscrowOpen && op.Terms.ID() == id) || (op.Kind != blockchain.EscrowOpen && op.Escrow == id) {
					steps = append(steps, escrowStep{escrowKindNames[op.Kind], h, b.Header.Timestamp, op.Signers})
				}
			}
		}
		if !more || len(heights) == 0 {
			break
		}
		before = heights[len(heights)-1]
	}
	slices.Reverse(steps)
	return steps, true
}

// archivedEscrow répond pour un échange réglé et élagué de l'état : conditions relues dans le bloc
// d'ouverture, étapes retrouvées par l'index. Renvoie false s'il est inconnu.
func (s *Server) archivedEscrow(w http.ResponseWriter, id crypto.Hash) bool {
	if s.cfg.Index == nil {
		return false
	}
	t, ok := s.openedTerms(id)
	if !ok {
		return false
	}
	steps, ok := s.escrowSteps(id, t.Seller)
	if !ok || len(steps) == 0 {
		return false
	}
	out := map[string]any{
		"id": hexOf(id), "seller": s.addr(t.Seller), "buyer": s.addr(t.Buyer), "arbiter": s.addr(t.Arbiter),
		"amount": units(t.Amount), "arbiter_fee": units(t.ArbiterFee), "fee": units(t.Fee), "days": t.Days,
		"opened_at": t.Timestamp, "deadline": (steps[0].Time/86400 + int64(t.Days)) * 86400,
		"outcome": "unknown", "required": map[string][]int{}, "history": steps, "archived": true,
	}
	for _, st := range steps {
		switch st.Kind {
		case "paid":
			out["paid"] = true
		case "dispute":
			out["disputed"] = true
		case "release", "refund":
			out["outcome"] = map[string]string{"release": "released", "refund": "refunded"}[st.Kind]
			out["closed_day"] = st.Time / 86400 * 86400
		}
	}
	reply(w, out)
	return true
}
