package blockchain

import (
	"testing"
	"time"

	"github.com/makoto-kanamoto/etl/internal/crypto"
)

// selFixture : un jour de participation au-dessus d'un genesis, avec des rôles choisis par le test.
type selFixture struct {
	p     Params
	prev  Header
	ts    int64
	start time.Time
	roles map[addrKey]Role
}

func newSelFixture(t *testing.T) *selFixture {
	p := testParams()
	n := newNet(t, p)
	f := &selFixture{p: p, prev: n.tip, ts: day(1), roles: map[addrKey]Role{}}
	f.start = time.Unix(f.ts, 0)
	return f
}

func (f *selFixture) node(t *testing.T, r Role) wallet {
	w := newWallet(t, f.p)
	f.roles[w.addr.Hash] = r
	return w
}

func (f *selFixture) recv(t *testing.T, w wallet, after time.Duration) Received {
	t.Helper()
	return Received{Participation: participate(t, f.p, f.prev.Hash(), w, f.ts), ReceivedAt: f.start.Add(after)}
}

func (f *selFixture) eligible(r []Received) DayParticipations {
	return Eligible(r, &f.prev, f.ts, f.p, func(a crypto.Address) Role { return f.roles[a.Hash] })
}

func minersOf(pts []Participation) map[addrKey]bool {
	m := map[addrKey]bool{}
	for _, pt := range pts {
		m[pt.Miner.Hash] = true
	}
	return m
}

func TestEligibleSortedByTicket(t *testing.T) {
	f := newSelFixture(t)
	var r []Received
	for i := 0; i < 5; i++ {
		r = append(r, f.recv(t, f.node(t, RoleParticipant), time.Duration(i)*time.Second))
	}
	got := f.eligible(r).Participants
	if len(got) != 5 {
		t.Fatalf("%d éligibles, attendu 5", len(got))
	}
	prevHash := f.prev.Hash()
	for i := 1; i < len(got); i++ {
		if Ticket(prevHash, got[i-1].Miner).Cmp(Ticket(prevHash, got[i].Miner)) >= 0 {
			t.Fatal("classement non croissant")
		}
	}
	reversed := make([]Received, len(r))
	for i := range r {
		reversed[i] = r[len(r)-1-i]
	}
	if f.eligible(reversed).Participants[0].Miner != got[0].Miner {
		t.Fatal("le proposant dépend de l'ordre d'arrivée")
	}
}

// Le ticket ne dépend que du bloc précédent et de l'adresse : miner plus longtemps ne sert à rien.
func TestTicketIgnoresWork(t *testing.T) {
	f := newSelFixture(t)
	w := f.node(t, RoleParticipant)
	pt := participate(t, f.p, f.prev.Hash(), w, f.ts)
	other := pt
	other.Nonce++
	for !other.PoWHash(f.prev.Hash(), f.ts).MeetsTarget(f.p.Target) {
		other.Nonce++
	}
	prev := f.prev.Hash()
	if Ticket(prev, pt.Miner) != Ticket(prev, other.Miner) {
		t.Fatal("le ticket dépend du travail")
	}
	var otherPrev crypto.Hash
	if Ticket(otherPrev, pt.Miner) == Ticket(prev, pt.Miner) {
		t.Fatal("le ticket ne change pas d'un jour à l'autre")
	}
}

func TestEligibleWindow(t *testing.T) {
	f := newSelFixture(t)
	ws := []wallet{f.node(t, RoleParticipant), f.node(t, RoleParticipant), f.node(t, RoleParticipant), f.node(t, RoleParticipant)}
	r := []Received{
		f.recv(t, ws[0], -time.Millisecond),               // avant 23:00:00
		f.recv(t, ws[1], 0),                               // 23:00:00 pile
		f.recv(t, ws[2], 30*time.Second-time.Millisecond), // dernière milliseconde
		f.recv(t, ws[3], 30*time.Second),                  // 23:00:30 : trop tard
	}
	got := minersOf(f.eligible(r).Participants)
	if len(got) != 2 || !got[ws[1].addr.Hash] || !got[ws[2].addr.Hash] {
		t.Fatalf("éligibles : %d, attendu nœuds 1 et 2", len(got))
	}
}

func TestEligibleRoles(t *testing.T) {
	f := newSelFixture(t)
	admitted, candidate, outsider := f.node(t, RoleParticipant), f.node(t, RoleObserver), f.node(t, RoleNone)
	victim := f.node(t, RoleParticipant)

	forged := f.recv(t, victim, time.Second) // au nom de la victime, signée par un autre
	forged.Participation.Signature, _ = admitted.key.Sign(f.p.ChainID, crypto.PurposeParticipation,
		powMessage(f.prev.Hash(), f.ts, victim.addr, forged.Participation.Nonce))
	wrongDay := Received{Participation: participate(t, f.p, f.prev.Hash(), victim, day(2)), ReceivedAt: f.start.Add(time.Second)}

	dp := f.eligible([]Received{
		f.recv(t, admitted, time.Second),
		f.recv(t, candidate, time.Second),
		f.recv(t, outsider, time.Second),
		forged,
		wrongDay,
	})
	if p := minersOf(dp.Participants); len(p) != 1 || !p[admitted.addr.Hash] {
		t.Errorf("participants : %d, attendu le seul nœud admis", len(p))
	}
	if o := minersOf(dp.Observations); len(o) != 1 || !o[candidate.addr.Hash] {
		t.Errorf("observations : %d, attendu le seul candidat", len(o))
	}
	if len(dp.Equivocations) != 0 {
		t.Error("preuve de double participation inattendue")
	}
}

func TestEligibleDoubleParticipation(t *testing.T) {
	f := newSelFixture(t)
	cheater, honest := f.node(t, RoleParticipant), f.node(t, RoleParticipant)

	a := f.recv(t, cheater, time.Second)
	b := a // seconde participation valide du même nœud (autre nonce)
	b.Participation.Nonce++
	for !b.Participation.PoWHash(f.prev.Hash(), f.ts).MeetsTarget(f.p.Target) {
		b.Participation.Nonce++
	}
	b.Participation.Signature, _ = cheater.key.Sign(f.p.ChainID, crypto.PurposeParticipation,
		powMessage(f.prev.Hash(), f.ts, cheater.addr, b.Participation.Nonce))
	b.ReceivedAt = a.ReceivedAt.Add(time.Second)

	dp := f.eligible([]Received{a, b, f.recv(t, honest, time.Second)})
	if got := minersOf(dp.Participants); got[cheater.addr.Hash] || !got[honest.addr.Hash] {
		t.Fatal("le tricheur doit être retiré des participants")
	}
	if len(dp.Equivocations) != 1 || dp.Equivocations[0].A.Miner != cheater.addr {
		t.Fatal("une preuve de double participation doit être produite")
	}
	if err := dp.Equivocations[0].verify(f.prev.Hash(), f.ts, f.p); err != nil {
		t.Fatalf("preuve invalide : %v", err)
	}

	// La même participation reçue deux fois (relais réseau) n'est pas une double participation.
	dup := a
	dup.ReceivedAt = a.ReceivedAt.Add(time.Second)
	if dp := f.eligible([]Received{a, dup}); len(dp.Equivocations) != 0 || !minersOf(dp.Participants)[cheater.addr.Hash] {
		t.Fatal("un doublon réseau ne doit pas exclure le nœud")
	}
}
