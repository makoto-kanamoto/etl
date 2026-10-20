package consensus

import (
	"testing"
	"time"

	"github.com/makoto-kanamoto/etl/internal/blockchain"
	"github.com/makoto-kanamoto/etl/internal/blockchain/chaintest"
	"github.com/makoto-kanamoto/etl/internal/network"
)

// De bout en bout : l'ouverture et les opérations soumises à un nœud se diffusent, sont incluses par
// le proposant du jour et appliquées par tous. Un arbitre malhonnête seul ne peut rien : ni libérer,
// ni rembourser ; avec l'acheteur, il tranche le litige.
func TestEscrowEndToEnd(t *testing.T) {
	s := newSim(t)
	s.runDay(1) // chaque fondateur : 1 ETL
	s.requireHeight(1)
	p := s.p
	seller, arbiter := s.nodes[0].wallet, s.nodes[1].wallet
	buyer := chaintest.NewWallet(t, p)
	fee, _ := blockchain.RequiredFee(blockchain.UnitsPerETL/2, p)
	terms := blockchain.EscrowTerms{Seller: seller.Addr, Buyer: buyer.Addr, Arbiter: arbiter.Addr, Amount: blockchain.UnitsPerETL / 2,
		ArbiterFee: 5000, Fee: fee, Days: 3, Timestamp: chaintest.Day(1) + 3600}
	id := terms.ID()
	sign := func(op blockchain.EscrowOp, signers ...uint8) blockchain.EscrowOp {
		for _, r := range signers {
			key := map[uint8]chaintest.Wallet{blockchain.SignerSeller: seller, blockchain.SignerBuyer: buyer, blockchain.SignerArbiter: arbiter}[r].Key
			if err := op.Sign(key, r, p); err != nil {
				t.Fatal(err)
			}
		}
		return op
	}
	submit := func(node int, op blockchain.EscrowOp) error {
		err := s.nodes[node].e.SubmitEscrowOp(op)
		s.flush()
		return err
	}
	stateOf := func(i int) (e blockchain.Escrow, ok bool) {
		s.nodes[i].store.ReadState(func(st *blockchain.State) { e, ok = st.Escrow(id) })
		return
	}

	s.at(time.Unix(chaintest.Day(1), 0).Add(2 * time.Hour))
	open := sign(blockchain.EscrowOp{Kind: blockchain.EscrowOpen, Terms: terms}, blockchain.SignerSeller, blockchain.SignerArbiter)
	if err := submit(5, open); err != nil {
		t.Fatal(err)
	}
	if err := submit(5, open); err != blockchain.ErrEscrowUnknown {
		t.Fatalf("doublon dans le pool : err = %v", err)
	}
	// Pool plein : refusé (borne locale de mémoire).
	saved := maxEscrowPool
	maxEscrowPool = 1
	other := terms
	other.Amount, other.Fee = terms.Amount/2, fee/2
	if err := s.nodes[5].e.SubmitEscrowOp(sign(blockchain.EscrowOp{Kind: blockchain.EscrowOpen, Terms: other}, blockchain.SignerSeller, blockchain.SignerArbiter)); err != ErrEscrowPoolFull {
		t.Fatalf("pool plein : err = %v", err)
	}
	maxEscrowPool = saved
	// « Payé » avant l'inclusion de l'ouverture : refusé (à resoumettre après).
	paid := sign(blockchain.EscrowOp{Kind: blockchain.EscrowPaid, Escrow: id}, blockchain.SignerBuyer)
	if err := submit(3, paid); err != blockchain.ErrEscrowUnknown {
		t.Fatalf("« payé » en avance : err = %v", err)
	}
	s.runDay(2)
	s.requireHeight(2)
	for i := range s.nodes {
		if e, ok := stateOf(i); !ok || e.Outcome != blockchain.EscrowPending {
			t.Fatalf("nœud %d : échange non ouvert", i)
		}
	}
	if err := submit(3, paid); err != nil {
		t.Fatal(err)
	}
	// L'arbitre seul : refusé localement, et ignoré (ni relayé ni inclus) s'il arrive par le réseau.
	alone := sign(blockchain.EscrowOp{Kind: blockchain.EscrowRelease, Escrow: id, Seq: 1}, blockchain.SignerArbiter)
	if err := submit(1, alone); err == nil {
		t.Fatal("libération par l'arbitre seul acceptée")
	}
	if v := s.nodes[4].e.Handle(network.Message{Type: network.MsgEscrowOp, Payload: blockchain.EncodeEscrowOp(&alone)}); v != network.Ignore {
		t.Fatalf("verdict : %v", v)
	}
	s.runDay(3)
	if e, _ := stateOf(0); !e.Paid || e.Seq != 1 || e.Outcome != blockchain.EscrowPending {
		t.Fatalf("après « payé » : %+v", e)
	}
	// Litige ouvert par l'acheteur, puis règlement acheteur + arbitre.
	if err := submit(7, sign(blockchain.EscrowOp{Kind: blockchain.EscrowDispute, Escrow: id, Seq: 1}, blockchain.SignerBuyer)); err != nil {
		t.Fatal(err)
	}
	s.runDay(4)
	if err := submit(2, sign(blockchain.EscrowOp{Kind: blockchain.EscrowRelease, Escrow: id, Seq: 2}, blockchain.SignerBuyer, blockchain.SignerArbiter)); err != nil {
		t.Fatal(err)
	}
	s.runDay(5)
	s.requireHeight(5)
	for i, n := range s.nodes {
		e, _ := stateOf(i)
		var rep blockchain.Reputation
		n.store.ReadState(func(st *blockchain.State) { rep = st.Reputation(seller.Addr) })
		if e.Outcome != blockchain.EscrowReleased || n.store.Account(buyer.Addr).Balance != terms.Amount ||
			rep != (blockchain.Reputation{Completed: 1, DisputesLost: 1}) {
			t.Fatalf("nœud %d : %+v, acheteur %d, réputation vendeur %+v", i, e, n.store.Account(buyer.Addr).Balance, rep)
		}
	}
	// Le pool a été purgé : plus rien en attente pour cet échange.
	for i, n := range s.nodes {
		n.e.mu.Lock()
		pending := len(n.e.escrowOps)
		n.e.mu.Unlock()
		if pending != 0 {
			t.Fatalf("nœud %d : %d opérations encore en attente", i, pending)
		}
	}
}
