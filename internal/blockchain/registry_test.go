package blockchain

import (
	"testing"

	"github.com/makoto-kanamoto/etl/internal/crypto"
)

// withRegs ajoute des candidatures à un bloc.
func withRegs(regs ...Registration) func(*Block) {
	return func(b *Block) { b.Registrations = regs }
}

// observe fait participer les fondateurs et le candidat du jour d1 au jour d2 inclus.
func observe(t testing.TB, n *testNet, cand wallet, d1, d2 int) {
	for d := d1; d <= d2; d++ {
		n.produce(t, d, append(append([]wallet(nil), n.founders...), cand), nil)
	}
}

// Parcours nominal : parrainage au jour 180 (6 mois), observation 5/7, admission au jour 187.
func TestSponsorshipAndAdmission(t *testing.T) {
	p := testParams()
	n := newNet(t, p)
	sponsor, cand := n.founders[0], newWallet(t, p)

	n.produce(t, 180, n.founders, withRegs(n.register(t, cand, sponsor, 180)))
	if got := n.st.Node(cand.addr); got.Status != StatusCandidate || got.Sponsor != sponsor.addr.Hash {
		t.Fatalf("après candidature : %+v", got)
	}

	observe(t, n, cand, 181, 185) // 5 jours de présence sur 7
	if got := n.st.Node(cand.addr); got.Status != StatusCandidate || got.ObservedDays != 5 {
		t.Fatalf("pendant l'observation : %+v", got)
	}
	if n.st.Account(cand.addr).Balance != 0 {
		t.Fatal("un candidat ne doit recevoir aucune récompense")
	}

	n.produce(t, 187, n.founders, nil) // fin de l'observation
	if got := n.st.Node(cand.addr); got.Status != StatusAdmitted || got.AdmittedDay != dayOf(day(187)) {
		t.Fatalf("après observation : %+v", got)
	}
	if n.st.ActiveCount() != 11 {
		t.Fatalf("%d actifs, attendu 11", n.st.ActiveCount())
	}

	// Le nouveau nœud participe désormais au partage : 10 ETL ÷ 11.
	n.produce(t, 188, append(append([]wallet(nil), n.founders...), cand), nil)
	if got := n.st.Account(cand.addr).Balance; got != 10*UnitsPerETL/11 {
		t.Fatalf("part du nouveau nœud = %s", FormatETL(got))
	}
}

func TestObservationFailure(t *testing.T) {
	p := testParams()
	n := newNet(t, p)
	cand := newWallet(t, p)
	n.produce(t, 180, n.founders, withRegs(n.register(t, cand, n.founders[0], 180)))
	observe(t, n, cand, 181, 184) // 4 jours seulement
	n.produce(t, 187, n.founders, nil)
	if got := n.st.Status(cand.addr); got != StatusUnknown {
		t.Fatalf("candidature échouée : statut %d, attendu inconnu", got)
	}
	// Le candidat peut recandidater avec un autre parrain.
	n.produce(t, 188, n.founders, withRegs(n.register(t, cand, n.founders[1], 188)))
	if n.st.Status(cand.addr) != StatusCandidate {
		t.Fatal("nouvelle candidature refusée")
	}
}

func TestSponsorEligibility(t *testing.T) {
	p := testParams()
	n := newNet(t, p)
	f := n.founders

	// Fondateurs : dispensés d'ancienneté, ils parrainent dès le premier jour…
	n.produce(t, 1, f, withRegs(n.register(t, newWallet(t, p), f[0], 1)))

	// … mais le quota de 1 parrainage par 30 jours s'applique.
	b := n.build(t, 30, f, withRegs(n.register(t, newWallet(t, p), f[0], 30)))
	if err := n.apply(&b); err != ErrSponsorNotEligible {
		t.Fatalf("second parrainage après 29 jours : err = %v", err)
	}
	n.produce(t, 31, f, withRegs(n.register(t, newWallet(t, p), f[0], 31)))

	// Un candidat, ou un nœud inconnu, ne peut pas parrainer.
	cand := newWallet(t, p)
	n.produce(t, 32, f, withRegs(n.register(t, cand, f[1], 32)))
	b = n.build(t, 33, f, withRegs(n.register(t, newWallet(t, p), cand, 33)))
	if err := n.apply(&b); err != ErrSponsorNotEligible {
		t.Fatalf("candidat parrain : err = %v", err)
	}
	b = n.build(t, 33, f, withRegs(n.register(t, newWallet(t, p), newWallet(t, p), 33)))
	if err := n.apply(&b); err != ErrSponsorNotEligible {
		t.Fatalf("inconnu parrain : err = %v", err)
	}
}

// Un fondateur réactivé perd sa dispense : son ancienneté repart de zéro.
func TestReactivatedFounderLosesExemption(t *testing.T) {
	p := testParams()
	n := newNet(t, p)
	absent, present := n.founders[9], n.founders[:9]
	n.produce(t, 30, present, nil)
	n.produce(t, 31, present, withRegs(n.reactivate(t, absent, 31)))
	for d := 32; d <= 36; d++ {
		n.produce(t, d, n.founders, nil)
	}
	n.produce(t, 38, n.founders, nil)
	if got := n.st.Node(absent.addr); got.Status != StatusAdmitted || got.Founder {
		t.Fatalf("après réactivation : %+v", got)
	}
	b := n.build(t, 40, n.founders, withRegs(n.register(t, newWallet(t, p), absent, 40)))
	if err := n.apply(&b); err != ErrSponsorNotEligible {
		t.Fatalf("parrainage par un fondateur réactivé : err = %v", err)
	}
}

// Un nœud fraîchement admis n'a pas l'ancienneté pour parrainer : pas de croissance exponentielle rapide.
func TestNewlyAdmittedCannotSponsor(t *testing.T) {
	p := testParams()
	n := newNet(t, p)
	cand := newWallet(t, p)
	n.produce(t, 180, n.founders, withRegs(n.register(t, cand, n.founders[0], 180)))
	observe(t, n, cand, 181, 187)
	n.founders = append(n.founders, cand)

	b := n.build(t, 200, n.founders, withRegs(n.register(t, newWallet(t, p), cand, 200)))
	if err := n.apply(&b); err != ErrSponsorNotEligible {
		t.Fatalf("admis depuis 13 jours : err = %v", err)
	}
	// 6 mois après son admission (jour 187 + 180), il peut parrainer.
	for d := 210; d < 367; d += 25 {
		n.produce(t, d, n.founders, nil) // rester actif
	}
	n.produce(t, 367, n.founders, withRegs(n.register(t, newWallet(t, p), cand, 367)))
}

func TestRegistrationRejects(t *testing.T) {
	p := testParams()
	n := newNet(t, p)
	f := n.founders
	n.produce(t, 179, f, nil)

	cand := newWallet(t, p)
	good := n.register(t, cand, f[0], 180)

	noSponsorSig := good
	noSponsorSig.SponsorSig = crypto.Signature{}
	wrongPurpose := good // signature du parrain avec l'usage du candidat
	wrongPurpose.SponsorSig, _ = f[0].key.Sign(p.ChainID, crypto.PurposeRegistration, good.message())
	stolen := good // candidature signée par quelqu'un d'autre que le candidat
	_ = stolen.SignCandidate(f[1].key, p.ChainID)
	self := n.register(t, f[0], f[0], 180)
	already := n.register(t, f[1], f[0], 180)
	unsorted := []Registration{n.register(t, newWallet(t, p), f[0], 180), n.register(t, newWallet(t, p), f[1], 180)}
	if unsorted[0].Candidate.Hash[0] < unsorted[1].Candidate.Hash[0] {
		unsorted[0], unsorted[1] = unsorted[1], unsorted[0]
	}

	cases := map[string]struct {
		regs []Registration
		d    int
		want error
	}{
		"signature du parrain absente":  {[]Registration{noSponsorSig}, 180, ErrRegistrationSig},
		"usage de signature détourné":   {[]Registration{wrongPurpose}, 180, ErrRegistrationSig},
		"candidat usurpé":               {[]Registration{stolen}, 180, ErrRegistrationSig},
		"auto-parrainage":               {[]Registration{self}, 180, ErrRegistrationStatus},
		"candidat déjà admis":           {[]Registration{already}, 180, ErrRegistrationStatus},
		"candidature prématurée":        {[]Registration{n.register(t, newWallet(t, p), f[0], 181)}, 180, ErrRegistrationDay},
		"candidature expirée (7 jours)": {[]Registration{good}, 187, ErrRegistrationDay},
		"candidatures non triées":       {unsorted, 180, ErrRegistryOrder},
	}
	for name, c := range cases {
		b := n.build(t, c.d, f, withRegs(c.regs...))
		accounts, nodes := n.st.accountsCopy(), n.st.nodes.clone()
		if err := n.apply(&b); err != c.want {
			t.Errorf("%s : err = %v, attendu %v", name, err, c.want)
		}
		if !equalMaps(accounts, n.st.accounts) || !equalMaps(nodes, n.st.nodes) {
			t.Errorf("%s : état modifié malgré le refus", name)
		}
	}
}

func TestObservationRejects(t *testing.T) {
	p := testParams()
	n := newNet(t, p)
	cand := newWallet(t, p)
	n.track(cand)
	n.produce(t, 180, n.founders, withRegs(n.register(t, cand, n.founders[0], 180)))

	obs := func(d int, w wallet) func(*Block) {
		return func(b *Block) { b.Observations = []Participation{participate(t, p, n.tip.Hash(), w, day(d))} }
	}
	// Observation hors fenêtre (jour 188 > 180 + 7) et observation d'un fondateur (non candidat).
	for name, c := range map[string]struct {
		d int
		w wallet
	}{
		"après la fenêtre": {188, cand},
		"non candidat":     {181, n.founders[1]},
	} {
		b := n.build(t, c.d, n.founders, obs(c.d, c.w))
		if err := n.apply(&b); err != ErrObservationStatus {
			t.Errorf("%s : err = %v", name, err)
		}
	}
}

func TestInactivityAndReactivation(t *testing.T) {
	p := testParams()
	n := newNet(t, p)
	absent := n.founders[9]
	present := n.founders[:9]

	n.produce(t, 29, present, nil)
	if n.st.Status(absent.addr) != StatusAdmitted {
		t.Fatal("inactif trop tôt (29 jours)")
	}
	n.produce(t, 30, present, nil)
	if n.st.Status(absent.addr) != StatusInactive || n.st.ActiveCount() != 9 {
		t.Fatalf("après 30 jours d'absence : statut %d, %d actifs", n.st.Status(absent.addr), n.st.ActiveCount())
	}

	// Un nœud inactif ne participe plus : ni part, ni ticket.
	b := n.build(t, 31, present, func(b *Block) {
		b.Participants = append(b.Participants, participate(t, p, n.tip.Hash(), absent, day(31)))
		sortByTicket(b.Participants, n.tip.Hash())
		b.Header.Proposer = b.Participants[0].Miner
	})
	if err := n.apply(&b); err != ErrBlockNotAdmitted {
		t.Fatalf("participation d'un inactif : err = %v", err)
	}

	// Réactivation sans parrain, puis nouvelle observation.
	n.produce(t, 31, present, withRegs(n.reactivate(t, absent, 31)))
	if n.st.Status(absent.addr) != StatusCandidate {
		t.Fatal("réactivation refusée")
	}
	for d := 32; d <= 36; d++ {
		n.produce(t, d, n.founders, nil) // l'absent est observé via son rôle
	}
	n.produce(t, 38, present, nil)
	if got := n.st.Node(absent.addr); got.Status != StatusAdmitted || got.AdmittedDay != dayOf(day(38)) {
		t.Fatalf("après réactivation : %+v", got)
	}
}

func TestFailedReactivationStaysInactive(t *testing.T) {
	p := testParams()
	n := newNet(t, p)
	absent, present := n.founders[9], n.founders[:9]
	n.produce(t, 30, present, nil)
	n.produce(t, 31, present, withRegs(n.reactivate(t, absent, 31)))
	n.produce(t, 38, present, nil) // aucune observation
	if n.st.Status(absent.addr) != StatusInactive {
		t.Fatalf("statut %d, attendu inactif", n.st.Status(absent.addr))
	}
	// Une réactivation n'est possible que pour un nœud inactif.
	b := n.build(t, 39, present, withRegs(n.reactivate(t, present[0], 39)))
	if err := n.apply(&b); err != ErrRegistrationStatus {
		t.Fatalf("réactivation d'un admis : err = %v", err)
	}
}

// doubleParticipation renvoie deux participations valides et différentes du même nœud.
func doubleParticipation(t testing.TB, n *testNet, w wallet, d int) (Participation, Participation) {
	a := participate(t, n.p, n.tip.Hash(), w, day(d))
	b := a
	for b.Nonce++; !b.PoWHash(n.tip.Hash(), day(d)).MeetsTarget(n.p.Target); b.Nonce++ {
	}
	b.Signature, _ = w.key.Sign(n.p.ChainID, crypto.PurposeParticipation, powMessage(n.tip.Hash(), day(d), w.addr, b.Nonce))
	return a, b
}

func TestEquivocationExcludes(t *testing.T) {
	p := testParams()
	n := newNet(t, p)
	cheater, others := n.founders[0], n.founders[1:]
	a, b := doubleParticipation(t, n, cheater, 1)
	other := participate(t, p, n.tip.Hash(), others[0], day(1))

	cases := map[string]struct {
		nodes []wallet
		eq    Equivocation
		want  error
	}{
		"tricheur aussi participant":   {n.founders, NewEquivocation(a, b), ErrEquivocatorPresent},
		"même participation deux fois": {others, Equivocation{A: a, B: a}, ErrEquivocationInvalid},
		"deux nœuds différents":        {others, NewEquivocation(a, other), ErrEquivocationInvalid},
		"ordre non canonique":          {others, func() Equivocation { e := NewEquivocation(a, b); e.A, e.B = e.B, e.A; return e }(), ErrEquivocationInvalid},
	}
	for name, c := range cases {
		blk := n.build(t, 1, c.nodes, func(bl *Block) { bl.Equivocations = []Equivocation{c.eq} })
		if err := n.apply(&blk); err != c.want {
			t.Errorf("%s : err = %v, attendu %v", name, err, c.want)
		}
	}

	n.produce(t, 1, others, func(bl *Block) { bl.Equivocations = []Equivocation{NewEquivocation(a, b)} })
	if n.st.Status(cheater.addr) != StatusExcluded {
		t.Fatal("le tricheur doit être exclu")
	}
	// Exclusion définitive : ni réactivation, ni nouvelle candidature, ni parrainage.
	blk := n.build(t, 180, others, withRegs(n.reactivate(t, cheater, 180)))
	if err := n.apply(&blk); err != ErrRegistrationStatus {
		t.Errorf("réactivation d'un exclu : err = %v", err)
	}
	blk = n.build(t, 180, others, withRegs(n.register(t, cheater, others[0], 180)))
	if err := n.apply(&blk); err != ErrRegistrationStatus {
		t.Errorf("recandidature d'un exclu : err = %v", err)
	}
	blk = n.build(t, 180, others, withRegs(n.register(t, newWallet(t, p), cheater, 180)))
	if err := n.apply(&blk); err != ErrSponsorNotEligible {
		t.Errorf("parrainage par un exclu : err = %v", err)
	}
}

func TestSybilExclusionBansSponsor(t *testing.T) {
	p := testParams()
	n := newNet(t, p)
	sponsor, fake := n.founders[0], newWallet(t, p)
	n.produce(t, 180, n.founders, withRegs(n.register(t, fake, sponsor, 180)))
	observe(t, n, fake, 181, 187)

	// Vote 2/3 « faux nœud » (sera appliqué par le module de votes).
	if err := n.st.nodes.applySybilExclusion(fake.addr.Hash, dayOf(day(190)), p); err != nil {
		t.Fatal(err)
	}
	if n.st.Status(fake.addr) != StatusExcluded {
		t.Fatal("faux nœud non exclu")
	}
	if got := n.st.Node(sponsor.addr).SponsorBanUntil; got != dayOf(day(190))+365 {
		t.Fatalf("interdiction du parrain jusqu'au jour %d", got)
	}
	// Le parrain reste admis (il n'est pas exclu) mais ne peut plus parrainer pendant 1 an.
	if n.st.Status(sponsor.addr) != StatusAdmitted {
		t.Fatal("le parrain ne doit pas être exclu")
	}
	for d := 200; d < 554; d += 25 {
		n.produce(t, d, n.founders, nil)
	}
	b := n.build(t, 554, n.founders, withRegs(n.register(t, newWallet(t, p), sponsor, 554)))
	if err := n.apply(&b); err != ErrSponsorNotEligible {
		t.Fatalf("parrainage pendant l'interdiction : err = %v", err)
	}
	n.produce(t, 555, n.founders, withRegs(n.register(t, newWallet(t, p), sponsor, 555)))

	// Exclure un nœud inconnu ou déjà exclu est une erreur.
	if err := n.st.nodes.applySybilExclusion(fake.addr.Hash, 556, p); err != ErrRegistrationStatus {
		t.Errorf("double exclusion : err = %v", err)
	}
}
