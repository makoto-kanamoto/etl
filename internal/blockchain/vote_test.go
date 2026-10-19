package blockchain

import (
	"bytes"
	"testing"
	"time"

	"github.com/makoto-kanamoto/etl/internal/crypto"
)

func TestQuorum(t *testing.T) {
	for n, want := range map[int]int{1: 1, 2: 2, 3: 3, 4: 3, 9: 7, 10: 7, 11: 8, 1000: 667} {
		if got := Quorum(n); got != want {
			t.Errorf("Quorum(%d) = %d, attendu %d", n, got, want)
		}
	}
	// Strictement plus de 2/3, et le plus petit tel nombre.
	for n := 1; n <= 3000; n++ {
		q := Quorum(n)
		if 3*q <= 2*n || 3*(q-1) > 2*n {
			t.Fatalf("Quorum(%d) = %d n'est pas le plus petit nombre > 2n/3", n, q)
		}
	}
}

// vote fait voter w pour l'en-tête h.
func vote(t testing.TB, p Params, w wallet, h *Header) Vote {
	t.Helper()
	v, err := SignVote(w.key, h, p)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func votes(t testing.TB, p Params, ws []wallet, h *Header) []Vote {
	var out []Vote
	for _, w := range ws {
		out = append(out, vote(t, p, w, h))
	}
	return out
}

func TestVoteSignVerify(t *testing.T) {
	p := testParams()
	n := newNet(t, p)
	b := n.build(t, 1, n.founders, nil)
	v := vote(t, p, n.founders[0], &b.Header)
	if err := v.Verify(p); err != nil {
		t.Fatal(err)
	}
	other := testParams()
	other.ChainID = "ETL-DE"
	forged := v
	forged.BlockHash[0] ^= 1
	wrongUse := v // signature d'une participation présentée comme vote
	wrongUse.Signature, _ = n.founders[0].key.Sign(p.ChainID, crypto.PurposeParticipation, voteMessage(v.Height, v.BlockHash))
	for name, c := range map[string]struct {
		v Vote
		p Params
	}{"hash modifié": {forged, p}, "autre fork": {v, other}, "autre usage": {wrongUse, p}} {
		if err := c.v.Verify(c.p); err != ErrVoteSignature {
			t.Errorf("%s : err = %v", name, err)
		}
	}
}

func TestVerifyCertificate(t *testing.T) {
	p := testParams()
	n := newNet(t, p)
	b := n.build(t, 1, n.founders, nil)
	h := &b.Header

	seven := NewCertificate(h, votes(t, p, n.founders[:7], h), p)
	if err := VerifyCertificate(&seven, h, n.st, p); err != nil {
		t.Fatalf("7 votes sur 10 : %v", err)
	}
	six := NewCertificate(h, votes(t, p, n.founders[:6], h), p)
	if err := VerifyCertificate(&six, h, n.st, p); err != ErrQuorum {
		t.Fatalf("6 votes sur 10 (= 60 %%) : err = %v", err)
	}

	// NewCertificate écarte doublons, votes pour un autre bloc et signatures fausses.
	alt := n.build(t, 1, n.founders[:9], nil)
	mixed := votes(t, p, n.founders[:6], h)
	mixed = append(mixed, mixed[0], vote(t, p, n.founders[6], &alt.Header))
	if c := NewCertificate(h, mixed, p); len(c.Votes) != 6 {
		t.Fatalf("NewCertificate a gardé %d votes, attendu 6", len(c.Votes))
	}

	outsider := newWallet(t, p)
	cases := map[string]struct {
		edit func(*Certificate)
		want error
	}{
		"certificat d'un autre bloc": {func(c *Certificate) { c.BlockHash = alt.Header.Hash() }, ErrCertificateTarget},
		"vote pour un autre bloc": {func(c *Certificate) { // même votant, autre bloc
			c.Votes[3], _ = SignVote(n.keys[c.Votes[3].Voter.Hash], &alt.Header, p)
		}, ErrCertificateTarget},
		"votant en double": {func(c *Certificate) { c.Votes[1] = c.Votes[0] }, ErrCertificateOrder},
		"votes non triés":  {func(c *Certificate) { c.Votes[0], c.Votes[1] = c.Votes[1], c.Votes[0] }, ErrCertificateOrder},
		"votant non admis": {func(c *Certificate) {
			c.Votes = append(c.Votes, vote(t, p, outsider, h))
			sortVotes(c.Votes)
		}, ErrCertificateVoter},
		"signature falsifiée": {func(c *Certificate) { c.Votes[2].Signature[10] ^= 1 }, nil},
	}
	for name, c := range cases {
		cert := NewCertificate(h, votes(t, p, n.founders[:8], h), p)
		cert.Votes = append([]Vote(nil), cert.Votes...)
		c.edit(&cert)
		err := VerifyCertificate(&cert, h, n.st, p)
		if (c.want == nil && err == nil) || (c.want != nil && err != c.want) {
			t.Errorf("%s : err = %v, attendu %v", name, err, c.want)
		}
	}
}

// Le seuil se calcule sur les nœuds ACTIFS : 3 inactifs → 7 actifs → 5 votes suffisent.
func TestQuorumFollowsActiveNodes(t *testing.T) {
	p := testParams()
	n := newNet(t, p)
	n.produce(t, 30, n.founders[:7], nil) // 3 fondateurs absents depuis 30 jours
	if n.st.ActiveCount() != 7 {
		t.Fatalf("%d actifs", n.st.ActiveCount())
	}
	b := n.build(t, 31, n.founders[:7], nil)
	h := &b.Header
	five := NewCertificate(h, votes(t, p, n.founders[:5], h), p)
	if err := VerifyCertificate(&five, h, n.st, p); err != nil {
		t.Fatalf("5 votes sur 7 actifs : %v", err)
	}
	// Le vote d'un inactif ne compte pas.
	withInactive := NewCertificate(h, votes(t, p, append(n.founders[:4], n.founders[9]), h), p)
	if err := VerifyCertificate(&withInactive, h, n.st, p); err != ErrCertificateVoter {
		t.Fatalf("vote d'un inactif : err = %v", err)
	}
}

func TestVoteEquivocationExcludes(t *testing.T) {
	p := testParams()
	n := newNet(t, p)
	cheater := n.founders[0]
	b1 := n.build(t, 1, n.founders, nil)
	alt := n.build(t, 1, n.founders[1:], nil) // bloc concurrent à la même hauteur
	v1, v2 := vote(t, p, cheater, &b1.Header), vote(t, p, cheater, &alt.Header)
	if err := n.apply(&b1); err != nil {
		t.Fatal(err)
	}
	proof := NewVoteEquivocation(v1, v2)

	other := vote(t, p, n.founders[1], &alt.Header)
	b2Header := Header{Height: 2}
	sameHash := VoteEquivocation{A: v1, B: v1}
	unordered := VoteEquivocation{A: proof.B, B: proof.A}
	future := NewVoteEquivocation(vote(t, p, cheater, &b2Header), vote(t, p, cheater, &Header{Height: 2, Timestamp: 1}))

	for name, c := range map[string]struct {
		eq   VoteEquivocation
		want error
	}{
		"même bloc deux fois":  {sameHash, ErrVoteEquivocation},
		"deux votants":         {NewVoteEquivocation(v1, other), ErrVoteEquivocation},
		"ordre non canonique":  {unordered, ErrVoteEquivocation},
		"hauteur du bloc même": {future, ErrVoteEquivocation},
		"signature falsifiée":  {func() VoteEquivocation { e := proof; e.B.Signature[5] ^= 1; return e }(), ErrVoteEquivocation},
	} {
		blk := n.build(t, 2, n.founders[1:], func(b *Block) { b.VoteEquivocations = []VoteEquivocation{c.eq} })
		if err := n.apply(&blk); err != c.want {
			t.Errorf("%s : err = %v, attendu %v", name, err, c.want)
		}
	}

	n.produce(t, 2, n.founders[1:], func(b *Block) { b.VoteEquivocations = []VoteEquivocation{proof} })
	if n.st.Status(cheater.addr) != StatusExcluded {
		t.Fatal("double vote non sanctionné")
	}
	// Exclu : ses votes ne comptent plus, et la preuve ne peut pas être rejouée.
	b3 := n.build(t, 3, n.founders[1:], nil)
	c := NewCertificate(&b3.Header, votes(t, p, n.founders, &b3.Header), p)
	if err := VerifyCertificate(&c, &b3.Header, n.st, p); err != ErrCertificateVoter {
		t.Fatalf("vote d'un exclu : err = %v", err)
	}
	replay := n.build(t, 3, n.founders[1:], func(b *Block) { b.VoteEquivocations = []VoteEquivocation{proof} })
	if err := n.apply(&replay); err != ErrVoteEquivocation {
		t.Fatalf("preuve rejouée : err = %v", err)
	}
}

// proposal construit un bloc du jour d à partir de participations choisies (tri canonique).
func (n *testNet) proposal(t testing.TB, d int, pts []Participation, obs []Participation) Block {
	t.Helper()
	pts = append([]Participation(nil), pts...)
	sortByTicket(pts, n.tip.Hash())
	b := Block{
		Header:       Header{Height: n.tip.Height + 1, PrevHash: n.tip.Hash(), Timestamp: day(d), Proposer: pts[0].Miner},
		Participants: pts,
		Observations: obs,
	}
	n.finalize(t, &b)
	return b
}

func TestCheckProposal(t *testing.T) {
	p := testParams()
	n := newNet(t, p)
	cand := newWallet(t, p)
	n.produce(t, 1, n.founders, withRegs(n.register(t, cand, n.founders[0], 1)))

	ts := day(2)
	start := time.Unix(ts, 0)
	prev := n.tip.Hash()
	var pts []Participation
	var recv []Received
	for i, w := range n.founders {
		pt := participate(t, p, prev, w, ts)
		pts = append(pts, pt)
		recv = append(recv, Received{Participation: pt, ReceivedAt: start.Add(time.Duration(i+1) * time.Second)})
	}
	obs := []Participation{participate(t, p, prev, cand, ts)}
	recv = append(recv, Received{Participation: obs[0], ReceivedAt: start.Add(3 * time.Second)})
	check := func(b Block, r []Received) error { return CheckProposal(&b, &n.tip, n.st, r, p) }

	if err := check(n.proposal(t, 2, pts, obs), recv); err != nil {
		t.Fatalf("bloc complet : %v", err)
	}
	// Fondateur 4 reçu à 23:00:05 puis omis : refus.
	omitted := append(append([]Participation(nil), pts[:4]...), pts[5:]...)
	if err := check(n.proposal(t, 2, omitted, obs), recv); err != ErrMissingParticipation {
		t.Fatalf("omission d'une participation reçue à temps : err = %v", err)
	}
	// Même omission, mais reçue à 23:00:27 (après la tolérance) : accepté.
	late := append([]Received(nil), recv...)
	late[4].ReceivedAt = start.Add(27 * time.Second)
	if err := check(n.proposal(t, 2, omitted, obs), late); err != nil {
		t.Fatalf("omission d'une participation tardive : %v", err)
	}
	// Observation du candidat omise : refus.
	if err := check(n.proposal(t, 2, pts, nil), recv); err != ErrMissingParticipation {
		t.Fatalf("omission d'une observation : err = %v", err)
	}
	// Ce nœud a vu le fondateur 3 participer deux fois : le bloc ne doit pas l'inclure.
	second := pts[3]
	for second.Nonce++; !second.PoWHash(prev, ts).MeetsTarget(p.Target); second.Nonce++ {
	}
	second.Signature, _ = n.founders[3].key.Sign(p.ChainID, crypto.PurposeParticipation, powMessage(prev, ts, second.Miner, second.Nonce))
	double := append(append([]Received(nil), recv...), Received{Participation: second, ReceivedAt: start.Add(20 * time.Second)})
	if err := check(n.proposal(t, 2, pts, obs), double); err != ErrEquivocatorIncluded {
		t.Fatalf("tricheur inclus : err = %v", err)
	}
	// Seconde participation reçue à 23:00:28 seulement : le proposant a pu ne pas la voir.
	// Refuser permettrait à un seul tricheur de faire sauter la journée.
	lateDouble := append(append([]Received(nil), recv...), Received{Participation: second, ReceivedAt: start.Add(28 * time.Second)})
	if err := check(n.proposal(t, 2, pts, obs), lateDouble); err != nil {
		t.Fatalf("double participation tardive : %v", err)
	}
	// Bloc invalide (racine d'état fausse) : refus.
	bad := n.proposal(t, 2, pts, obs)
	bad.Header.StateRoot[0] ^= 1
	_ = bad.Sign(n.keys[bad.Header.Proposer.Hash], p.ChainID)
	if err := check(bad, recv); err != ErrStateRoot {
		t.Fatalf("racine fausse : err = %v", err)
	}
}

func TestCertificateCodec(t *testing.T) {
	p := testParams()
	n := newNet(t, p)
	b := n.build(t, 1, n.founders, nil)
	c := NewCertificate(&b.Header, votes(t, p, n.founders, &b.Header), p)
	enc := EncodeCertificate(&c)
	got, err := DecodeCertificate(enc, p.AddressHRP)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyCertificate(&got, &b.Header, n.st, p); err != nil {
		t.Fatalf("certificat décodé invalide : %v", err)
	}
	if !bytes.Equal(EncodeCertificate(&got), enc) {
		t.Fatal("ré-encodage différent")
	}
	for i := 0; i < len(enc); i++ {
		if _, err := DecodeCertificate(enc[:i], p.AddressHRP); err == nil {
			t.Fatalf("certificat tronqué à %d octets accepté", i)
		}
	}
	v := c.Votes[0]
	if got, err := DecodeVote(EncodeVote(&v), p.AddressHRP); err != nil || got != v {
		t.Fatalf("vote : %v", err)
	}
}

func FuzzDecodeCertificate(f *testing.F) {
	p := testParams()
	n := newNet(f, p)
	b := n.build(f, 1, n.founders, nil)
	c := NewCertificate(&b.Header, votes(f, p, n.founders, &b.Header), p)
	f.Add(EncodeCertificate(&c))
	f.Fuzz(func(t *testing.T, in []byte) {
		got, err := DecodeCertificate(in, "etl")
		if err != nil {
			return
		}
		if !bytes.Equal(EncodeCertificate(&got), in) {
			t.Fatal("encodage non canonique accepté")
		}
	})
}
