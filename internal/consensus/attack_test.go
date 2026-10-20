package consensus

import (
	"testing"
	"time"

	"github.com/makoto-kanamoto/etl/internal/blockchain"
	"github.com/makoto-kanamoto/etl/internal/blockchain/chaintest"
	"github.com/makoto-kanamoto/etl/internal/crypto"
	"github.com/makoto-kanamoto/etl/internal/network"
)

// Attaque ciblée « amnésie » : le scénario exact que le verrouillage empêche.
//
//	Tour 0  Le groupe A (4 honnêtes) et les 3 malveillants pré-votent B ; A se verrouille sur B.
//	        Un seul nœud de A, X, reçoit les 7 pré-engagements : il FINALISE B, puis est isolé.
//	Tour 1  Le proposant suivant (groupe Y, 3 honnêtes, qui n'a rien vu) propose C.
//	        Les malveillants pré-votent et pré-engagent C.
//
// Avec verrou : A refuse C → 3 + 3 = 6 voix → C n'est pas finalisé. Sécurité préservée.
// Sans verrou : A pré-vote C → 9 voix → C finalisé → DEUX blocs à la hauteur 1.
func TestAmnesiaAttackPrevented(t *testing.T) {
	s := newSim(t)
	p := s.p
	bt := chaintest.Day(1)
	s.at(time.Unix(bt, 0)) // tous participent

	// Ordre des tickets du jour, vu par le nœud 0.
	n0 := s.nodes[0]
	n0.e.mu.Lock()
	hs := n0.e.ensureHeight()
	dp := blockchain.Eligible(hs.day(bt).recv, &hs.prev, bt, p, func(crypto.Address) blockchain.Role { return blockchain.RoleParticipant })
	n0.e.mu.Unlock()
	rank := map[crypto.Address]int{}
	for i, pt := range dp.Participants {
		rank[pt.Miner] = i
	}
	idx := map[int]int{} // rang → nœud
	for i, n := range s.nodes {
		idx[rank[n.wallet.Addr]] = i
	}
	P0, P1 := idx[0], idx[1]
	byz := []int{idx[7], idx[8], idx[9]}
	A := []int{P0, idx[2], idx[3], idx[4]}
	Y := []int{P1, idx[5], idx[6]}
	X := P0
	in := func(set []int, i int) bool {
		for _, x := range set {
			if x == i {
				return true
			}
		}
		return false
	}
	for _, b := range byz {
		s.mute[b] = true
	}

	var B, C *blockchain.Proposal
	s.drop = func(d delivery) bool {
		switch d.m.Type {
		case network.MsgProposal:
			pr, _ := blockchain.DecodeProposal(d.m.Payload, p.AddressHRP)
			if pr.Round.Round == 0 {
				B = &pr
				return !in(A, d.to) // B n'atteint que A
			}
			if pr.Round.Round == 1 && C == nil {
				C = &pr
			}
		case network.MsgVote:
			v, _ := blockchain.DecodeVote(d.m.Payload, p.AddressHRP)
			if v.Round.Round == 0 && v.Type == blockchain.Prevote {
				return !in(A, d.to) // Y ne voit pas les pré-votes du tour 0
			}
			if v.Round.Round == 0 && v.Type == blockchain.Precommit {
				return d.to != X // seul X voit les pré-engagements du tour 0
			}
		case network.MsgFinalized:
			return true // la finalisation de X ne se propage pas
		}
		return false
	}
	inject := func(t blockchain.VoteType, pr *blockchain.Proposal, targets []int) {
		for _, b := range byz {
			v, err := blockchain.SignVote(s.nodes[b].wallet.Key, t, &pr.Block.Header, pr.Round, p)
			if err != nil {
				s.t.Fatal(err)
			}
			for _, j := range targets {
				s.send(b, j, network.Message{Type: network.MsgVote, Payload: blockchain.EncodeVote(&v)})
			}
		}
		s.flush()
	}

	// Tour 0
	s.at(blockchain.RoundStart(bt, 0, p).Add(time.Second))
	if B == nil {
		t.Fatal("prémisse : B proposé")
	}
	inject(blockchain.Prevote, B, A)   // POL(B) pour A → A se verrouille et pré-engage
	inject(blockchain.Precommit, B, A) // seul X en reçoit assez (filtre)
	if tip := s.nodes[X].store.Tip(); tip.Height != 1 || tip.Hash() != B.Block.Header.Hash() {
		t.Fatal("prémisse : X a finalisé B")
	}
	for _, a := range A[1:] {
		if s.nodes[a].store.Tip().Height != 0 {
			t.Fatal("prémisse : seul X a finalisé")
		}
		if _, _, ok := s.nodes[a].store.Lock(); !ok {
			t.Fatal("prémisse : A verrouillé sur B")
		}
	}
	s.down[X] = true

	// Tour 1 : C proposé par P1 ; les malveillants votent C partout.
	s.at(blockchain.RoundStart(bt, 1, p).Add(time.Second))
	if C == nil || C.Block.Header.Hash() == B.Block.Header.Hash() {
		t.Fatal("prémisse : un bloc C différent proposé au tour 1")
	}
	honestOnline := append(append([]int(nil), A[1:]...), Y...)
	inject(blockchain.Prevote, C, honestOnline)
	inject(blockchain.Precommit, C, honestOnline)
	for r := 2; r < p.MaxRounds; r++ {
		s.at(blockchain.RoundStart(bt, uint8(r), p).Add(time.Second))
	}
	honest := append([]int{X}, honestOnline...)
	s.safety(honest) // ÉCHOUE si les verrous ne sont pas respectés

	// Le lendemain, X revient : B (finalisé par X) se propage, la chaîne repart.
	s.down[X] = false
	s.drop = nil
	s.syncLagging(honest)
	s.safety(honest)
	for _, i := range honest {
		if tip := s.nodes[i].store.Tip(); tip.Hash() != B.Block.Header.Hash() {
			t.Fatalf("nœud %d : la hauteur 1 doit être B", i)
		}
	}
	_ = Y
}
