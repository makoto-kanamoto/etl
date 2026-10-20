package consensus

import (
	"testing"
	"time"

	"github.com/makoto-kanamoto/etl/internal/blockchain"
	"github.com/makoto-kanamoto/etl/internal/blockchain/chaintest"
	"github.com/makoto-kanamoto/etl/internal/crypto"
	"github.com/makoto-kanamoto/etl/internal/network"
)

// byRank renvoie, pour le jour bt, l'indice du nœud de chaque rang de ticket (vue du nœud 0).
func byRank(s *sim, bt int64) map[int]int {
	n0 := s.nodes[0]
	n0.e.mu.Lock()
	hs := n0.e.ensureHeight()
	dp := blockchain.Eligible(hs.day(bt).recv, &hs.prev, bt, s.p, func(crypto.Address) blockchain.Role { return blockchain.RoleParticipant })
	n0.e.mu.Unlock()
	rank := map[crypto.Address]int{}
	for i, pt := range dp.Participants {
		rank[pt.Miner] = i
	}
	idx := map[int]int{}
	for i, n := range s.nodes {
		idx[rank[n.wallet.Addr]] = i
	}
	return idx
}

type castKey struct {
	from  int
	typ   blockchain.VoteType
	round blockchain.RoundID
	block crypto.Hash
}

// silence coupe tous les votes sur le bus (les tests les injectent à la main) et note qui a voté quoi.
// Les propositions passent et sont retenues par tour.
func silence(s *sim) (cast map[castKey]bool, proposals map[blockchain.RoundID]*blockchain.Proposal) {
	cast, proposals = map[castKey]bool{}, map[blockchain.RoundID]*blockchain.Proposal{}
	s.drop = func(d delivery) bool {
		switch d.m.Type {
		case network.MsgVote:
			v, _ := blockchain.DecodeVote(d.m.Payload, s.p.AddressHRP)
			cast[castKey{d.from, v.Type, v.Round, v.BlockHash}] = true
			return true
		case network.MsgProposal:
			pr, _ := blockchain.DecodeProposal(d.m.Payload, s.p.AddressHRP)
			if proposals[pr.Round] == nil {
				proposals[pr.Round] = &pr
			}
		}
		return false
	}
	return cast, proposals
}

// inject fait signer un vote par chacun des nœuds donnés et le remet directement au nœud cible.
func inject(t *testing.T, s *sim, target int, typ blockchain.VoteType, h *blockchain.Header, r blockchain.RoundID, voters []int) {
	t.Helper()
	for _, i := range voters {
		v, err := blockchain.SignVote(s.nodes[i].wallet.Key, typ, h, r, s.p)
		if err != nil {
			t.Fatal(err)
		}
		s.nodes[target].e.Handle(network.Message{Type: network.MsgVote, Payload: blockchain.EncodeVote(&v)})
	}
	s.flush()
}

// Règles du verrou, au plus près :
//   - > 2/3 de pré-votes pour un tour PASSÉ : le bloc devient « valide » (à re-proposer), mais le
//     nœud ne se verrouille pas et ne pré-engage pas (il n'est plus dans ce tour) ;
//   - un nœud verrouillé au tour 1 refuse la re-proposition d'un autre bloc dont la preuve date du tour 0.
func TestLockRules(t *testing.T) {
	s := newSim(t)
	p := s.p
	bt := chaintest.Day(1)
	cast, props := silence(s)
	s.at(time.Unix(bt, 0))
	idx := byRank(s, bt)
	P2, T, U := idx[2], idx[5], idx[6]
	all := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}
	r0, r1, r2 := blockchain.RoundID{Day: bt, Round: 0}, blockchain.RoundID{Day: bt, Round: 1}, blockchain.RoundID{Day: bt, Round: 2}

	s.at(blockchain.RoundStart(bt, 0, p).Add(time.Second))
	s.at(blockchain.RoundStart(bt, 1, p).Add(time.Second))
	B, C := props[r0], props[r1]
	if B == nil || C == nil || B.POL != nil || C.POL != nil || B.Block.Header.Hash() == C.Block.Header.Hash() {
		t.Fatal("prémisse : deux nouveaux blocs différents aux tours 0 et 1")
	}

	// Tour 1 en cours : T reçoit > 2/3 de pré-votes pour C au tour 1 → verrou sur C.
	inject(t, s, T, blockchain.Prevote, &C.Block.Header, r1, all)
	if b, _, ok := s.nodes[T].store.Lock(); !ok || b.Header.Hash() != C.Block.Header.Hash() {
		t.Fatal("prémisse : T verrouillé sur C au tour 1")
	}
	// Tour 1 en cours : P2 reçoit > 2/3 de pré-votes pour B au tour 0 (passé).
	inject(t, s, P2, blockchain.Prevote, &B.Block.Header, r0, all)
	if _, _, ok := s.nodes[P2].store.Lock(); ok {
		t.Fatal("verrou pris sur un tour passé")
	}
	if cast[castKey{P2, blockchain.Precommit, r0, B.Block.Header.Hash()}] {
		t.Fatal("pré-engagement émis pour un tour passé")
	}

	// Tour 2 : P2 re-propose B avec sa preuve du tour 0.
	s.at(blockchain.RoundStart(bt, 2, p).Add(time.Second))
	pr := props[r2]
	if pr == nil || pr.POL == nil || pr.POL.Round != r0 || pr.Block.Header.Hash() != B.Block.Header.Hash() {
		t.Fatal("prémisse : re-proposition de B avec la preuve du tour 0")
	}
	if !cast[castKey{U, blockchain.Prevote, r2, B.Block.Header.Hash()}] {
		t.Fatal("prémisse : un nœud non verrouillé pré-vote la re-proposition")
	}
	if cast[castKey{T, blockchain.Prevote, r2, B.Block.Header.Hash()}] {
		t.Fatal("T, verrouillé au tour 1, a pré-voté un autre bloc sur une preuve plus ancienne (tour 0)")
	}
}

// Le vote est inscrit sur disque avant d'être signé : redémarré (mémoire perdue), un nœud ne pré-vote
// pas une seconde proposition du même tour, même valide (proposant qui joue double jeu).
func TestNoSecondVoteAfterRestart(t *testing.T) {
	s := newSim(t)
	p := s.p
	s.runDay(1) // les fondateurs ont 1 ETL : une transaction distingue la seconde proposition
	s.requireHeight(1)
	bt := chaintest.Day(2)
	cast, props := silence(s)
	s.at(time.Unix(bt, 0))
	idx := byRank(s, bt)
	P0, T := idx[0], idx[5]
	r0 := blockchain.RoundID{Day: bt, Round: 0}

	s.at(blockchain.RoundStart(bt, 0, p).Add(time.Second))
	B := props[r0]
	if B == nil || !cast[castKey{T, blockchain.Prevote, r0, B.Block.Header.Hash()}] {
		t.Fatal("prémisse : T a pré-voté B au tour 0")
	}

	// Le proposant construit un second bloc pour le même tour, avec une transaction.
	from, to := s.nodes[1].wallet, s.nodes[2].wallet
	tx := s.net.Tx(t, from, to.Addr, blockchain.UnitsPerETL/2, 0, bt)
	if err := s.nodes[P0].e.SubmitTransaction(tx); err != nil {
		t.Fatal(err)
	}
	s.flush()
	e := s.nodes[P0].e
	e.mu.Lock()
	hs := e.ensureHeight()
	var B2 *blockchain.Proposal
	e.chain.ReadState(func(st *blockchain.State) { B2 = e.buildProposal(st, hs, r0) })
	e.mu.Unlock()
	if B2 == nil || len(B2.Block.Txs) != 1 {
		t.Fatal("prémisse : seconde proposition avec la transaction")
	}

	s.restart(T, true)
	var err error
	s.nodes[T].store.ReadState(func(st *blockchain.State) {
		prev := s.nodes[T].store.Tip()
		err = blockchain.CheckProposal(B2, &prev, st, nil, s.now, p)
	})
	if err != nil {
		t.Fatalf("prémisse : seconde proposition valide pour T : %v", err)
	}
	s.nodes[T].e.Handle(network.Message{Type: network.MsgProposal, Payload: blockchain.EncodeProposal(B2)})
	s.flush()
	if cast[castKey{T, blockchain.Prevote, r0, B2.Block.Header.Hash()}] {
		t.Fatal("double pré-vote après redémarrage : le vote n'était pas inscrit sur disque")
	}
}
