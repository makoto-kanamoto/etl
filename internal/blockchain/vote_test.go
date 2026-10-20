package blockchain

import (
	"bytes"
	"testing"

	"github.com/makoto-kanamoto/etl/internal/crypto"
)

func TestQuorum(t *testing.T) {
	for n, want := range map[int]int{1: 1, 2: 2, 3: 3, 4: 3, 9: 7, 10: 7, 11: 8, 1000: 667} {
		if got := Quorum(n); got != want {
			t.Errorf("Quorum(%d) = %d, attendu %d", n, got, want)
		}
	}
	for n := 1; n <= 3000; n++ { // strictement plus de 2/3, et le plus petit tel nombre
		q := Quorum(n)
		if 3*q <= 2*n || 3*(q-1) > 2*n {
			t.Fatalf("Quorum(%d) = %d n'est pas le plus petit nombre > 2n/3", n, q)
		}
	}
}

func TestRoundIDOrder(t *testing.T) {
	a, b, c := RoundID{Day: 100, Round: 4}, RoundID{Day: 200, Round: 0}, RoundID{Day: 200, Round: 1}
	if !a.Before(b) || !b.Before(c) || c.Before(a) || a.Before(a) {
		t.Fatal("ordre (jour, tour) incorrect")
	}
}

// rid : le tour d'origine d'un bloc (sa date, son tour).
func rid(h *Header) RoundID { return RoundID{Day: h.Timestamp, Round: h.Round} }

func vote(t testing.TB, p Params, w wallet, typ VoteType, h *Header, r RoundID) Vote {
	t.Helper()
	v, err := SignVote(w.key, typ, h, r, p)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func votes(t testing.TB, p Params, ws []wallet, typ VoteType, h *Header, r RoundID) []Vote {
	var out []Vote
	for _, w := range ws {
		out = append(out, vote(t, p, w, typ, h, r))
	}
	return out
}

// certify : certificat de finalité (pré-engagements) au tour d'origine du bloc.
func certify(t testing.TB, p Params, ws []wallet, h *Header) Certificate {
	return NewCertificate(Precommit, h, rid(h), votes(t, p, ws, Precommit, h, rid(h)), p)
}

func TestVoteSignVerify(t *testing.T) {
	p := testParams()
	n := newNet(t, p)
	b := n.build(t, 1, n.founders, nil)
	v := vote(t, p, n.founders[0], Prevote, &b.Header, rid(&b.Header))
	if err := v.Verify(p); err != nil {
		t.Fatal(err)
	}
	other := testParams()
	other.ChainID = "ETL-DE"
	mutate := func(f func(*Vote)) Vote { x := v; f(&x); return x }
	for name, c := range map[string]struct {
		v Vote
		p Params
	}{
		"hash modifié":          {mutate(func(x *Vote) { x.BlockHash[0] ^= 1 }), p},
		"pré-vote → pré-engag.": {mutate(func(x *Vote) { x.Type = Precommit }), p},
		"autre tour":            {mutate(func(x *Vote) { x.Round.Round++ }), p},
		"autre jour":            {mutate(func(x *Vote) { x.Round.Day += 86400 }), p},
		"type inconnu":          {mutate(func(x *Vote) { x.Type = 3 }), p},
		"autre fork":            {v, other},
		"signature d'autre usage": {mutate(func(x *Vote) {
			x.Signature, _ = n.founders[0].key.Sign(p.ChainID, crypto.PurposeParticipation, voteMessage(x.Type, x.Height, x.Round, x.BlockHash))
		}), p},
	} {
		if err := c.v.Verify(c.p); err != ErrVoteSignature {
			t.Errorf("%s : err = %v", name, err)
		}
	}
}

func TestVerifyCertificate(t *testing.T) {
	p := testParams()
	n := newNet(t, p)
	b := n.build(t, 1, n.founders, nil)
	h, r := &b.Header, rid(&b.Header)

	if c := certify(t, p, n.founders[:7], h); VerifyCertificate(&c, h, n.st, p) != nil {
		t.Fatal("7 pré-engagements sur 10 refusés")
	}
	if c := certify(t, p, n.founders[:6], h); VerifyCertificate(&c, h, n.st, p) != ErrQuorum {
		t.Fatal("6 pré-engagements sur 10 acceptés")
	}
	prevotes := NewCertificate(Prevote, h, r, votes(t, p, n.founders, Prevote, h, r), p)
	if err := VerifyCertificate(&prevotes, h, n.st, p); err != ErrCertificateType {
		t.Fatalf("des pré-votes ne finalisent pas : err = %v", err)
	}
	if err := VerifyPOL(&prevotes, h, n.st, p); err != nil {
		t.Fatalf("preuve de verrou valide refusée : %v", err)
	}
	// NewCertificate écarte doublons, autres types, autres tours et votes pour un autre bloc.
	alt := n.build(t, 1, n.founders[:9], nil)
	mixed := votes(t, p, n.founders[:6], Precommit, h, r)
	mixed = append(mixed, mixed[0], vote(t, p, n.founders[6], Prevote, h, r),
		vote(t, p, n.founders[7], Precommit, h, RoundID{r.Day, r.Round + 1}), vote(t, p, n.founders[8], Precommit, &alt.Header, r))
	if c := NewCertificate(Precommit, h, r, mixed, p); len(c.Votes) != 6 {
		t.Fatalf("NewCertificate a gardé %d votes, attendu 6", len(c.Votes))
	}

	outsider := newWallet(t, p)
	cases := map[string]struct {
		edit func(*Certificate)
		want error
	}{
		"autre bloc": {func(c *Certificate) { c.BlockHash = alt.Header.Hash() }, ErrCertificateTarget},
		"vote d'un autre tour": {func(c *Certificate) {
			c.Votes[3], _ = SignVote(n.keys[c.Votes[3].Voter.Hash], Precommit, h, RoundID{r.Day, r.Round + 1}, p)
		}, ErrCertificateTarget},
		"votant en double": {func(c *Certificate) { c.Votes[1] = c.Votes[0] }, ErrCertificateOrder},
		"non triés":        {func(c *Certificate) { c.Votes[0], c.Votes[1] = c.Votes[1], c.Votes[0] }, ErrCertificateOrder},
		"votant non admis": {func(c *Certificate) {
			c.Votes = append(c.Votes, vote(t, p, outsider, Precommit, h, r))
			sortVotes(c.Votes)
		}, ErrCertificateVoter},
		"signature falsifiée": {func(c *Certificate) { c.Votes[2].Signature[10] ^= 1 }, ErrVoteSignature},
	}
	for name, c := range cases {
		cert := certify(t, p, n.founders[:8], h)
		cert.Votes = append([]Vote(nil), cert.Votes...)
		c.edit(&cert)
		if err := VerifyCertificate(&cert, h, n.st, p); err != c.want {
			t.Errorf("%s : err = %v, attendu %v", name, err, c.want)
		}
	}
}

// Le seuil se calcule sur les nœuds ACTIFS : 3 inactifs → 7 actifs → 5 votes suffisent.
func TestQuorumFollowsActiveNodes(t *testing.T) {
	p := testParams()
	n := newNet(t, p)
	n.produce(t, 30, n.founders[:7], nil)
	b := n.build(t, 31, n.founders[:7], nil)
	h := &b.Header
	if c := certify(t, p, n.founders[:5], h); VerifyCertificate(&c, h, n.st, p) != nil {
		t.Fatal("5 votes sur 7 actifs refusés")
	}
	if c := certify(t, p, append(n.founders[:4:4], n.founders[9]), h); VerifyCertificate(&c, h, n.st, p) != ErrCertificateVoter {
		t.Fatal("vote d'un inactif compté")
	}
}

// Double vote = deux votes du même type, au même tour, pour deux blocs. Voter différemment
// à des tours différents est normal.
func TestVoteEquivocation(t *testing.T) {
	p := testParams()
	n := newNet(t, p)
	cheater := n.founders[0]
	b1 := n.build(t, 1, n.founders, nil)
	alt := n.build(t, 1, n.founders[1:], nil)
	r := rid(&b1.Header)
	r2 := RoundID{r.Day, r.Round + 1}
	pv1, pv2 := vote(t, p, cheater, Prevote, &b1.Header, r), vote(t, p, cheater, Prevote, &alt.Header, r)
	if err := n.apply(&b1); err != nil {
		t.Fatal(err)
	}
	proof := NewVoteEquivocation(pv1, pv2)
	if err := proof.verify(2, p); err != nil {
		t.Fatalf("double pré-vote au même tour : %v", err)
	}
	for name, e := range map[string]VoteEquivocation{
		"tours différents (normal)": NewVoteEquivocation(pv1, vote(t, p, cheater, Prevote, &alt.Header, r2)),
		"types différents (normal)": NewVoteEquivocation(pv1, vote(t, p, cheater, Precommit, &alt.Header, r)),
		"même bloc":                 {A: pv1, B: pv1},
		"deux votants":              NewVoteEquivocation(pv1, vote(t, p, n.founders[1], Prevote, &alt.Header, r)),
		"ordre non canonique":       {A: proof.B, B: proof.A},
	} {
		if err := e.verify(2, p); err != ErrVoteEquivocation {
			t.Errorf("%s : err = %v", name, err)
		}
	}
	future := NewVoteEquivocation(vote(t, p, cheater, Precommit, &Header{Height: 3}, r), vote(t, p, cheater, Precommit, &Header{Height: 3, Timestamp: 1}, r))
	if err := future.verify(2, p); err != ErrVoteEquivocation {
		t.Fatal("preuve d'une hauteur future acceptée")
	}

	n.produce(t, 2, n.founders, func(b *Block) { b.VoteEquivocations = []VoteEquivocation{proof} })
	if n.st.Status(cheater.addr) != StatusExcluded {
		t.Fatal("double vote non sanctionné")
	}
	if got := n.st.Account(cheater.addr).Balance; got != UnitsPerETL {
		t.Fatalf("le tricheur ne touche pas sa part le jour de son exclusion : solde %s", FormatETL(got))
	}
	b3 := n.build(t, 3, n.founders[1:], nil)
	if c := certify(t, p, n.founders, &b3.Header); VerifyCertificate(&c, &b3.Header, n.st, p) != ErrCertificateVoter {
		t.Fatal("vote d'un exclu compté")
	}
}

func TestCertificateCodec(t *testing.T) {
	p := testParams()
	n := newNet(t, p)
	b := n.build(t, 1, n.founders, nil)
	for _, typ := range []VoteType{Prevote, Precommit} {
		c := NewCertificate(typ, &b.Header, rid(&b.Header), votes(t, p, n.founders, typ, &b.Header, rid(&b.Header)), p)
		enc := EncodeCertificate(&c)
		got, err := DecodeCertificate(enc, p.AddressHRP)
		if err != nil || !bytes.Equal(EncodeCertificate(&got), enc) {
			t.Fatalf("type %d : aller-retour : %v", typ, err)
		}
		if err := verifyVotes(&got, typ, b.Header.Height, b.Header.Hash(), n.st, p); err != nil {
			t.Fatalf("type %d : certificat décodé invalide : %v", typ, err)
		}
		for i := 0; i < len(enc); i++ {
			if _, err := DecodeCertificate(enc[:i], p.AddressHRP); err == nil {
				t.Fatalf("certificat tronqué à %d octets accepté", i)
			}
		}
	}
	v := vote(t, p, n.founders[0], Precommit, &b.Header, rid(&b.Header))
	if got, err := DecodeVote(EncodeVote(&v), p.AddressHRP); err != nil || got != v {
		t.Fatalf("vote : %v", err)
	}
}

func FuzzDecodeCertificate(f *testing.F) {
	p := testParams()
	n := newNet(f, p)
	b := n.build(f, 1, n.founders, nil)
	c := certify(f, p, n.founders, &b.Header)
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
