package blockchain

import (
	"testing"
	"time"

	"github.com/makoto-kanamoto/etl/internal/crypto"
)

type selectionFixture struct {
	p        Params
	prev     Header
	ts       int64
	start    time.Time
	nodes    []wallet
	admitted map[addrKey]bool
}

func newSelectionFixture(t *testing.T, n int) *selectionFixture {
	p := testParams()
	f := &selectionFixture{p: p, prev: testGenesis(t, p).Header, ts: day(1), admitted: map[addrKey]bool{}}
	f.start = time.Unix(f.ts, 0)
	for i := 0; i < n; i++ {
		w := newWallet(t, p)
		f.nodes = append(f.nodes, w)
		f.admitted[w.addr.Hash] = true
	}
	return f
}

func (f *selectionFixture) isAdmitted(a crypto.Address) bool { return f.admitted[a.Hash] }

func (f *selectionFixture) recv(t *testing.T, w wallet, after time.Duration) Received {
	t.Helper()
	return Received{Participation: participate(t, f.p, &f.prev, w, f.ts), ReceivedAt: f.start.Add(after)}
}

func (f *selectionFixture) eligible(r []Received) []Participation {
	return Eligible(r, &f.prev, f.ts, f.p, f.isAdmitted)
}

func minersOf(pts []Participation) map[addrKey]bool {
	m := map[addrKey]bool{}
	for _, pt := range pts {
		m[pt.Miner.Hash] = true
	}
	return m
}

func TestEligibleSortedByTicket(t *testing.T) {
	f := newSelectionFixture(t, 5)
	var r []Received
	for i, w := range f.nodes {
		r = append(r, f.recv(t, w, time.Duration(i)*time.Second))
	}
	got := f.eligible(r)
	if len(got) != 5 {
		t.Fatalf("%d éligibles, attendu 5", len(got))
	}
	prevHash := f.prev.Hash()
	for i := 1; i < len(got); i++ {
		if Ticket(prevHash, got[i-1].Miner).Cmp(Ticket(prevHash, got[i].Miner)) >= 0 {
			t.Fatal("classement non croissant")
		}
	}
	// L'ordre d'arrivée ne change rien.
	reversed := make([]Received, len(r))
	for i := range r {
		reversed[i] = r[len(r)-1-i]
	}
	if f.eligible(reversed)[0].Miner != got[0].Miner {
		t.Fatal("le proposant dépend de l'ordre d'arrivée")
	}
}

// Le ticket ne dépend que du bloc précédent et de l'adresse : miner plus longtemps ne sert à rien.
func TestTicketIgnoresWork(t *testing.T) {
	f := newSelectionFixture(t, 1)
	pt := participate(t, f.p, &f.prev, f.nodes[0], f.ts)
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
	f := newSelectionFixture(t, 4)
	r := []Received{
		f.recv(t, f.nodes[0], -time.Millisecond),               // avant 23:00:00
		f.recv(t, f.nodes[1], 0),                               // 23:00:00 pile
		f.recv(t, f.nodes[2], 30*time.Second-time.Millisecond), // dernière milliseconde
		f.recv(t, f.nodes[3], 30*time.Second),                  // 23:00:30 : trop tard
	}
	got := minersOf(f.eligible(r))
	if len(got) != 2 || !got[f.nodes[1].addr.Hash] || !got[f.nodes[2].addr.Hash] {
		t.Fatalf("éligibles : %d, attendu nœuds 1 et 2", len(got))
	}
}

func TestEligibleExclusions(t *testing.T) {
	f := newSelectionFixture(t, 1)
	honest := f.nodes[0]
	outsider := newWallet(t, f.p) // non admis
	victim := newWallet(t, f.p)
	f.admitted[victim.addr.Hash] = true

	// Participation au nom de la victime, signée par un autre.
	forged := f.recv(t, victim, time.Second)
	forged.Participation.Signature, _ = honest.key.Sign(f.p.ChainID, crypto.PurposeParticipation,
		powMessage(f.prev.Hash(), f.ts, victim.addr, forged.Participation.Nonce))

	// Participation minée pour un autre jour.
	wrongDay := Received{Participation: participate(t, f.p, &f.prev, victim, day(2)), ReceivedAt: f.start.Add(time.Second)}

	got := minersOf(f.eligible([]Received{
		f.recv(t, honest, time.Second),
		f.recv(t, outsider, time.Second),
		forged,
		wrongDay,
	}))
	if len(got) != 1 || !got[honest.addr.Hash] {
		t.Fatalf("seul le nœud honnête devait rester, %d éligibles", len(got))
	}
}

func TestEligibleDoubleParticipationExcluded(t *testing.T) {
	f := newSelectionFixture(t, 2)
	cheater, honest := f.nodes[0], f.nodes[1]

	a := f.recv(t, cheater, time.Second)
	// Seconde participation valide du même nœud (autre nonce).
	b := a
	b.Participation.Nonce++
	for !b.Participation.PoWHash(f.prev.Hash(), f.ts).MeetsTarget(f.p.Target) {
		b.Participation.Nonce++
	}
	b.Participation.Signature, _ = cheater.key.Sign(f.p.ChainID, crypto.PurposeParticipation,
		powMessage(f.prev.Hash(), f.ts, cheater.addr, b.Participation.Nonce))
	b.ReceivedAt = a.ReceivedAt.Add(time.Second)

	got := minersOf(f.eligible([]Received{a, b, f.recv(t, honest, time.Second)}))
	if got[cheater.addr.Hash] || !got[honest.addr.Hash] {
		t.Fatal("le nœud ayant participé deux fois doit être exclu")
	}

	// La même participation reçue deux fois (relais réseau) n'est pas une double participation.
	dup := a
	dup.ReceivedAt = a.ReceivedAt.Add(time.Second)
	if got := minersOf(f.eligible([]Received{a, dup})); !got[cheater.addr.Hash] {
		t.Fatal("un doublon réseau ne doit pas exclure le nœud")
	}
}

// La liste éligible, telle quelle, forme un bloc valide.
func TestEligibleBuildsValidBlock(t *testing.T) {
	f := newSelectionFixture(t, 4)
	var r []Received
	keys := map[addrKey]*crypto.PrivateKey{}
	for _, w := range f.nodes {
		r = append(r, f.recv(t, w, time.Second))
		keys[w.addr.Hash] = w.key
	}
	pts := f.eligible(r)
	b := Block{Header: Header{Height: 1, PrevHash: f.prev.Hash(), Timestamp: f.ts, Proposer: pts[0].Miner}, Participants: pts}
	finalize(t, f.p, &b, keys)
	if err := ValidateBlock(&b, &f.prev, f.p, f.isAdmitted); err != nil {
		t.Fatal(err)
	}
}
