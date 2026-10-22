package blockchain

import (
	"math/rand/v2"
	"testing"
)

// La part de chaque bloc est renvoyée avec les changements (affichage), hors racine d'état.
func TestRewardInChanges(t *testing.T) {
	p := testParams()
	n := newNet(t, p)
	share := func(d int, nodes []wallet) uint64 {
		t.Helper()
		b := n.build(t, d, nodes, nil)
		c, err := n.st.Prepare(&b, p)
		if err != nil {
			t.Fatal(err)
		}
		return c.Reward
	}
	if got := share(1, n.founders); got != UnitsPerETL {
		t.Fatalf("10 participants : %s", FormatETL(got))
	}
	if got := share(1, n.founders[:3]); got != 333_333_333 {
		t.Fatalf("3 participants sur 10 actifs : %s", FormatETL(got))
	}
	n.produce(t, 31, n.founders[:3], nil) // 7 fondateurs inactifs : 3 actifs < MinActiveNodes
	if got := share(32, n.founders[:3]); got != 0 {
		t.Fatalf("sous MinActiveNodes : %s", FormatETL(got))
	}
}

func TestRewardedExcludesExcludedParticipants(t *testing.T) {
	p := testParams()
	ws := newWallets(t, p, 3)
	b := Block{Participants: []Participation{{Miner: ws[0].addr}, {Miner: ws[1].addr}}, Exclusions: []Exclusion{{Target: ws[1].addr}}}
	if !b.Rewarded(ws[0].addr) {
		t.Fatal("participant non récompensé")
	}
	if b.Rewarded(ws[1].addr) {
		t.Fatal("participant exclu par ce bloc récompensé")
	}
	if b.Rewarded(ws[2].addr) {
		t.Fatal("non-participant récompensé")
	}
}

// CanSponsor reprend exactement l'ancienne condition écrite dans applyRegistrations.
func TestCanSponsorMatchesConsensusRule(t *testing.T) {
	p := testParams()
	old := func(n Node, day int64) bool {
		return !(n.Status != StatusAdmitted ||
			(!n.Founder && day-n.AdmittedDay < p.SponsorSeniorityDays) ||
			(n.HasSponsored && day-n.LastSponsorDay < p.SponsorCooldownDays) ||
			day < n.SponsorBanUntil)
	}
	r := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < 200_000; i++ {
		n := Node{
			Status:          NodeStatus(r.IntN(5)),
			Founder:         r.IntN(4) == 0,
			AdmittedDay:     r.Int64N(800),
			HasSponsored:    r.IntN(2) == 0,
			LastSponsorDay:  r.Int64N(800),
			SponsorBanUntil: r.Int64N(1200),
		}
		day := r.Int64N(1200)
		if n.CanSponsor(day, p) != old(n, day) {
			t.Fatalf("divergence : %+v, jour %d", n, day)
		}
		// SponsorFrom : premier jour éligible (nœud admis, paramètres constants).
		if n.Status == StatusAdmitted {
			from := n.SponsorFrom(p)
			if !n.CanSponsor(from, p) || n.CanSponsor(from-1, p) {
				t.Fatalf("SponsorFrom = %d pour %+v", from, n)
			}
		}
	}
}

// Supply : total émis (récompenses), inchangé par un transfert, des frais ou un échange en cours.
func TestSupplyIsTotalIssued(t *testing.T) {
	n := newEscrowNet(t) // jour 1 : 10 ETL émis
	if got := n.st.Supply(); got != 10*UnitsPerETL {
		t.Fatalf("après 1 bloc : %s", FormatETL(got))
	}
	terms := n.terms(true, UnitsPerETL/2, 3)
	n.next(t, n.open(t, terms)) // jour 2 : montant et rémunération retenus, frais au proposant
	if got := n.st.Supply(); got != 20*UnitsPerETL {
		t.Fatalf("échange en cours : %s", FormatETL(got))
	}
	n.next(t, n.op(t, EscrowRelease, terms.ID(), 0, SignerSeller))
	if got := n.st.Supply(); got != 30*UnitsPerETL {
		t.Fatalf("échange réglé : %s", FormatETL(got))
	}
}
