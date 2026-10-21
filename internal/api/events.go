package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/makoto-kanamoto/etl/internal/blockchain"
)

// Notifications par Server-Sent Events (GET /api/events) : un événement « block » par bloc finalisé,
// avec les transactions et opérations d'escrow qu'il contient ; le navigateur filtre ce qui le
// concerne. Commentaire de maintien toutes les 15 s ; nombre de clients simultanés borné.

const keepAlive = 15 * time.Second

type blockEvent struct {
	headerJSON
	Txs       []txJSON       `json:"txs"`
	EscrowOps []escrowOpJSON `json:"escrow_ops"`
}

func (s *Server) eventOf(b *blockchain.Block) blockEvent {
	ev := blockEvent{headerJSON: s.headerOf(b.Header), Txs: []txJSON{}, EscrowOps: []escrowOpJSON{}}
	for i := range b.Txs {
		ev.Txs = append(ev.Txs, s.txOf(&b.Txs[i], b.Header.Height))
	}
	for i := range b.EscrowOps {
		op := &b.EscrowOps[i]
		id := op.Escrow
		if op.Kind == blockchain.EscrowOpen {
			id = op.Terms.ID()
		}
		ev.EscrowOps = append(ev.EscrowOps, escrowOpJSON{ID: hexOf(op.ID()), Kind: uint8(op.Kind), Escrow: hexOf(id), Signers: op.Signers})
	}
	return ev
}

func (s *Server) eventsHandler(w http.ResponseWriter, r *http.Request) {
	select {
	case s.events <- struct{}{}:
		defer func() { <-s.events }()
	default:
		fail(w, http.StatusServiceUnavailable, "trop de clients de notifications")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		fail(w, http.StatusInternalServerError, "flux non pris en charge")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "retry: 5000\n\n")
	flusher.Flush()

	last := s.cfg.Chain.Tip().Height
	poll := time.NewTicker(s.cfg.Poll)
	defer poll.Stop()
	alive := time.NewTicker(keepAlive)
	defer alive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-alive.C:
			fmt.Fprint(w, ": maintien\n\n")
			flusher.Flush()
		case <-poll.C:
			tip := s.cfg.Chain.Tip().Height
			for h := last + 1; h <= tip; h++ {
				b, err := s.cfg.Chain.BlockAt(h)
				if err != nil {
					continue
				}
				data, _ := json.Marshal(s.eventOf(&b))
				fmt.Fprintf(w, "id: %d\nevent: block\ndata: %s\n\n", h, data)
			}
			if tip > last {
				last = tip
				flusher.Flush()
			}
		}
	}
}
