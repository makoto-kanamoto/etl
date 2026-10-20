package blockchain

import (
	"bytes"
	"testing"
	"time"

	"github.com/makoto-kanamoto/etl/internal/crypto"
)

// nominate renvoie les participations du jour d de chaque nœud, triées par ticket.
func (n *testNet) nominate(t testing.TB, d int, nodes []wallet) []Participation {
	var pts []Participation
	for _, w := range nodes {
		n.track(w)
		pts = append(pts, participate(t, n.p, n.tip.Hash(), w, day(d)))
	}
	sortByTicket(pts, n.tip.Hash())
	return pts
}

// fresh construit une proposition de nouveau bloc au tour (jour d, r), signée par le proposant désigné.
func (n *testNet) fresh(t testing.TB, d int, r uint8, nom []Participation, edit func(*Block)) Proposal {
	t.Helper()
	b := Block{
		Header:       Header{Height: n.tip.Height + 1, PrevHash: n.tip.Hash(), Timestamp: day(d), Round: r, Proposer: nom[r].Miner},
		Participants: nom,
	}
	if edit != nil {
		edit(&b)
	}
	n.finalize(t, &b)
	pr := Proposal{Round: RoundID{day(d), r}, Block: b, Nomination: nom}
	if err := pr.Sign(n.keys[nom[r].Miner.Hash], n.p); err != nil {
		t.Fatal(err)
	}
	return pr
}

// repropose propose à nouveau un bloc antérieur au tour (jour d, r), avec une preuve de verrou.
func (n *testNet) repropose(t testing.TB, d int, r uint8, nom []Participation, b Block, pol *Certificate) Proposal {
	t.Helper()
	pr := Proposal{Round: RoundID{day(d), r}, Block: b, Nomination: nom, POL: pol}
	if err := pr.Sign(n.keys[nom[r].Miner.Hash], n.p); err != nil {
		t.Fatal(err)
	}
	return pr
}

func TestValidateProposal(t *testing.T) {
	p := testParams()
	n := newNet(t, p)
	nom1 := n.nominate(t, 1, n.founders)
	good := n.fresh(t, 1, 0, nom1, nil)
	if err := ValidateProposal(&good, &n.tip, n.st, p); err != nil {
		t.Fatalf("proposition valide refusée : %v", err)
	}
	// Re-proposition au jour 2 du bloc du jour 1, avec > 2/3 de pré-votes au tour (J1, 0).
	r0 := good.Round
	pol := NewCertificate(Prevote, &good.Block.Header, r0, votes(t, p, n.founders[:7], Prevote, &good.Block.Header, r0), p)
	nom2 := n.nominate(t, 2, n.founders)
	again := n.repropose(t, 2, 0, nom2, good.Block, &pol)
	if err := ValidateProposal(&again, &n.tip, n.st, p); err != nil {
		t.Fatalf("re-proposition valide refusée : %v", err)
	}

	weakPOL := NewCertificate(Prevote, &good.Block.Header, r0, votes(t, p, n.founders[:6], Prevote, &good.Block.Header, r0), p)
	samePOL := pol
	samePOL.Round = RoundID{day(2), 0}
	// Vrais pré-votes au tour même de la re-proposition : signatures valides, mais preuve non antérieure.
	r2 := RoundID{day(2), 0}
	realSamePOL := NewCertificate(Prevote, &good.Block.Header, r2, votes(t, p, n.founders[:7], Prevote, &good.Block.Header, r2), p)
	r21 := RoundID{day(2), 1}
	laterPOL := NewCertificate(Prevote, &good.Block.Header, r21, votes(t, p, n.founders[:7], Prevote, &good.Block.Header, r21), p)
	other := n.fresh(t, 1, 0, n.nominate(t, 1, n.founders[:9]), nil)
	otherPOL := NewCertificate(Prevote, &other.Block.Header, r0, votes(t, p, n.founders[:7], Prevote, &other.Block.Header, r0), p)
	resign := func(pr Proposal) Proposal { _ = pr.Sign(n.keys[pr.Proposer().Hash], p); return pr }
	outsider := newWallet(t, p)

	cases := map[string]struct {
		pr   Proposal
		want error
	}{
		"tour 5":             {resign(func() Proposal { x := good; x.Round.Round = 5; return x }()), ErrProposalRound},
		"jour déjà finalisé": {resign(func() Proposal { x := good; x.Round.Day = day(0); return x }()), ErrProposalRound},
		"signée par un autre": {func() Proposal {
			x := good
			_ = x.Sign(n.founders[0].key, p)
			if x.Proposer() == n.founders[0].addr {
				_ = x.Sign(n.founders[1].key, p)
			}
			return x
		}(), ErrProposalSignature},
		"nomination non triée": {resign(func() Proposal {
			x := good
			x.Nomination = append([]Participation(nil), nom1...)
			x.Nomination[1], x.Nomination[2] = x.Nomination[2], x.Nomination[1]
			return x
		}()), ErrProposalNomination},
		"nomination d'un non-admis": {resign(func() Proposal {
			x := good
			x.Nomination = append(append([]Participation(nil), nom1...), participate(t, p, n.tip.Hash(), outsider, day(1)))
			return x
		}()), ErrProposalNomination},
		// Nominations triées par ticket : seul le contrôle de chaque participation peut les refuser.
		"nomination triée d'un non-admis": {resign(func() Proposal {
			x := good
			n.track(outsider)
			x.Nomination = append(append([]Participation(nil), nom1...), participate(t, p, n.tip.Hash(), outsider, day(1)))
			sortByTicket(x.Nomination, n.tip.Hash())
			return x
		}()), ErrProposalNomination},
		"nomination triée d'une participation d'un autre jour": {resign(func() Proposal {
			x := good
			x.Nomination = []Participation{participate(t, p, n.tip.Hash(), n.founders[0], day(2))}
			for _, pt := range nom1 {
				if pt.Miner != n.founders[0].addr {
					x.Nomination = append(x.Nomination, pt)
				}
			}
			sortByTicket(x.Nomination, n.tip.Hash())
			return x
		}()), ErrProposalNomination},
		"nouveau bloc d'un autre jour":  {resign(func() Proposal { x := good; x.Round.Day = day(2); x.Nomination = nom2; return x }()), ErrProposalFresh},
		"re-proposition sans preuve":    {resign(func() Proposal { x := again; x.POL = nil; return x }()), ErrProposalFresh},
		"preuve insuffisante":           {n.repropose(t, 2, 0, nom2, good.Block, &weakPOL), ErrProposalPOL},
		"preuve pas antérieure au tour": {n.repropose(t, 2, 0, nom2, good.Block, &samePOL), ErrProposalPOL},
		"preuve du même tour":           {n.repropose(t, 2, 0, nom2, good.Block, &realSamePOL), ErrProposalPOL},
		"preuve d'un tour postérieur":   {n.repropose(t, 2, 0, nom2, good.Block, &laterPOL), ErrProposalPOL},
		"preuve d'un autre bloc":        {n.repropose(t, 2, 0, nom2, good.Block, &otherPOL), ErrProposalPOL},
		"bloc invalide": {resign(func() Proposal {
			x := good
			x.Block.Header.StateRoot[0] ^= 1
			_ = x.Block.Sign(n.keys[x.Block.Header.Proposer.Hash], p.ChainID)
			return x
		}()), ErrStateRoot},
	}
	for name, c := range cases {
		if err := ValidateProposal(&c.pr, &n.tip, n.st, p); err != c.want {
			t.Errorf("%s : err = %v, attendu %v", name, err, c.want)
		}
	}
}

func TestCheckProposalView(t *testing.T) {
	p := testParams()
	n := newNet(t, p)
	cand := newWallet(t, p)
	n.produce(t, 1, n.founders, withRegs(n.register(t, cand, n.founders[0], 1)))

	ts := day(2)
	start := time.Unix(ts, 0)
	nom := n.nominate(t, 2, n.founders)
	obs := []Participation{participate(t, p, n.tip.Hash(), cand, ts)}
	var recv []Received
	for i, pt := range nom {
		recv = append(recv, Received{Participation: pt, ReceivedAt: start.Add(time.Duration(i+1) * time.Second)})
	}
	recv = append(recv, Received{Participation: obs[0], ReceivedAt: start.Add(3 * time.Second)})
	round0 := RoundStart(ts, 0, p)
	check := func(pr Proposal, r []Received, now time.Time) error {
		return CheckProposal(&pr, &n.tip, n.st, r, now, p)
	}
	withObs := func(b *Block) { b.Observations = obs }

	full := n.fresh(t, 2, 0, nom, withObs)
	if err := check(full, recv, round0); err != nil {
		t.Fatalf("proposition complète : %v", err)
	}
	// Une participation reçue à 23:00:0x et absente de la nomination : refus.
	omitted := append(append([]Participation(nil), nom[:4]...), nom[5:]...)
	if err := check(n.fresh(t, 2, 0, omitted, withObs), recv, round0); err != ErrMissingParticipation {
		t.Fatalf("omission : err = %v", err)
	}
	// La même, reçue à 23:00:27 (après la tolérance) : accepté.
	late := append([]Received(nil), recv...)
	for i := range late {
		if late[i].Participation.Miner == nom[4].Miner {
			late[i].ReceivedAt = start.Add(27 * time.Second)
		}
	}
	if err := check(n.fresh(t, 2, 0, omitted, withObs), late, round0); err != nil {
		t.Fatalf("omission tardive : %v", err)
	}
	if err := check(n.fresh(t, 2, 0, nom, nil), recv, round0); err != ErrMissingParticipation {
		t.Fatalf("observation omise : err = %v", err)
	}
	// Double participation vue avant 23:00:25 : le tricheur ne doit pas figurer dans la nomination.
	second := nom[3]
	for second.Nonce++; !second.PoWHash(n.tip.Hash(), ts).MeetsTarget(p.Target); second.Nonce++ {
	}
	second.Signature, _ = n.keys[second.Miner.Hash].Sign(p.ChainID, crypto.PurposeParticipation, powMessage(n.tip.Hash(), ts, second.Miner, second.Nonce))
	early := append(append([]Received(nil), recv...), Received{Participation: second, ReceivedAt: start.Add(20 * time.Second)})
	if err := check(full, early, round0); err != ErrEquivocatorIncluded {
		t.Fatalf("tricheur inclus : err = %v", err)
	}
	lateDouble := append(append([]Received(nil), recv...), Received{Participation: second, ReceivedAt: start.Add(28 * time.Second)})
	if err := check(full, lateDouble, round0); err != nil {
		t.Fatalf("double participation tardive : %v", err)
	}
	// Trop tôt : avant le début du tour, moins la tolérance d'horloge.
	if err := check(full, recv, round0.Add(-p.ClockTolerance-time.Millisecond)); err != ErrRoundTooEarly {
		t.Fatalf("proposition en avance : err = %v", err)
	}
	if err := check(full, recv, round0.Add(-p.ClockTolerance)); err != nil {
		t.Fatalf("dans la tolérance d'horloge : %v", err)
	}
}

func TestProposalCodec(t *testing.T) {
	p := testParams()
	n := newNet(t, p)
	pr := n.fresh(t, 1, 0, n.nominate(t, 1, n.founders), nil)
	pol := NewCertificate(Prevote, &pr.Block.Header, pr.Round, votes(t, p, n.founders, Prevote, &pr.Block.Header, pr.Round), p)
	again := n.repropose(t, 2, 1, n.nominate(t, 2, n.founders), pr.Block, &pol)
	for name, x := range map[string]Proposal{"nouveau bloc": pr, "re-proposition": again} {
		enc := EncodeProposal(&x)
		got, err := DecodeProposal(enc, p.AddressHRP)
		if err != nil || !bytes.Equal(EncodeProposal(&got), enc) {
			t.Fatalf("%s : aller-retour : %v", name, err)
		}
		if got.VerifySignature(p) != nil || got.Block.Header.Hash() != x.Block.Header.Hash() {
			t.Fatalf("%s : proposition décodée incohérente", name)
		}
		for i := 0; i < len(enc); i++ {
			if _, err := DecodeProposal(enc[:i], p.AddressHRP); err == nil {
				t.Fatalf("%s : tronquée à %d octets acceptée", name, i)
			}
		}
	}
}

func FuzzDecodeProposal(f *testing.F) {
	p := testParams()
	n := newNet(f, p)
	pr := n.fresh(f, 1, 0, n.nominate(f, 1, n.founders), nil)
	pol := NewCertificate(Prevote, &pr.Block.Header, pr.Round, votes(f, p, n.founders, Prevote, &pr.Block.Header, pr.Round), p)
	again := n.repropose(f, 2, 1, n.nominate(f, 2, n.founders), pr.Block, &pol)
	f.Add(EncodeProposal(&pr))
	f.Add(EncodeProposal(&again))
	f.Fuzz(func(t *testing.T, in []byte) {
		got, err := DecodeProposal(in, "etl")
		if err != nil {
			return
		}
		if !bytes.Equal(EncodeProposal(&got), in) {
			t.Fatal("encodage non canonique accepté")
		}
	})
}
