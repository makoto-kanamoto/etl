package blockchain

import (
	"bytes"
	"sort"
	"testing"

	"github.com/makoto-kanamoto/etl/internal/crypto"
)

var q4 = IPCRecord{Value: 103500, Period: "2026-Q4", SourceSHA256: [32]byte{0xab}}

func ipcVotes(t testing.TB, p Params, ws []wallet, rec IPCRecord) []IPCVote {
	t.Helper()
	var out []IPCVote
	for _, w := range ws {
		v, err := SignIPCVote(w.key, rec, p)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return bytes.Compare(out[i].Voter.Hash[:], out[j].Voter.Hash[:]) < 0 })
	return out
}

func withIPC(rec IPCRecord, votes []IPCVote) func(*Block) {
	return func(b *Block) { r := rec; b.Header.IPC, b.IPCVotes = &r, votes }
}

func exclusion(t testing.TB, p Params, ws []wallet, target crypto.Address, signDay int) Exclusion {
	t.Helper()
	ex := Exclusion{Target: target}
	for _, w := range ws {
		v, err := SignExclusionVote(w.key, target, dayOf(day(signDay)), p)
		if err != nil {
			t.Fatal(err)
		}
		ex.Votes = append(ex.Votes, v)
	}
	sort.Slice(ex.Votes, func(i, j int) bool { return bytes.Compare(ex.Votes[i].Voter.Hash[:], ex.Votes[j].Voter.Hash[:]) < 0 })
	return ex
}

func withExclusions(exs ...Exclusion) func(*Block) {
	return func(b *Block) { b.Exclusions = exs }
}

// expectRefused vérifie qu'un bloc est refusé avec l'erreur attendue et ne laisse aucune trace.
func (n *testNet) expectRefused(t *testing.T, name string, b Block, want error) {
	t.Helper()
	accounts, nodes, ipc := n.st.accountsCopy(), n.st.nodes.clone(), n.st.IPC()
	if err := n.apply(&b); err != want {
		t.Errorf("%s : err = %v, attendu %v", name, err, want)
	}
	if !equalMaps(accounts, n.st.accounts) || !equalMaps(nodes, n.st.nodes) || n.st.IPC() != ipc {
		t.Errorf("%s : état modifié malgré le refus", name)
	}
}

func TestGenesisIPC(t *testing.T) {
	p := testParams()
	n := newNet(t, p)
	if n.st.BaseIPC() != testIPC() || n.st.IPC() != testIPC() {
		t.Fatalf("IPC après genesis : base %+v, courant %+v", n.st.BaseIPC(), n.st.IPC())
	}
}

func TestIPCUpdate(t *testing.T) {
	p := testParams()
	n := newNet(t, p)
	f := n.founders
	outsider := newWallet(t, p)

	other := q4
	other.Value++
	mixed := ipcVotes(t, p, f[:7], q4)
	mixed[3], _ = SignIPCVote(n.keys[mixed[3].Voter.Hash], other, p)
	foreign := testParams()
	foreign.ChainID = "ETL-DE"
	forked := ipcVotes(t, p, f[:7], q4)
	forked[0], _ = SignIPCVote(n.keys[forked[0].Voter.Hash], q4, foreign)
	dup := ipcVotes(t, p, f[:7], q4)
	dup[1] = dup[0]
	withOutsider := ipcVotes(t, p, append(append([]wallet(nil), f[:7]...), outsider), q4)
	sameQ := q4
	sameQ.Period = "2026-Q3"
	earlier := q4
	earlier.Period = "2026-Q2"

	for name, c := range map[string]struct {
		edit func(*Block)
		want error
	}{
		"6 votes sur 10":           {withIPC(q4, ipcVotes(t, p, f[:6], q4)), ErrQuorum},
		"un vote pour autre chose": {withIPC(q4, mixed), ErrIPCVotes},
		"vote d'un autre fork":     {withIPC(q4, forked), ErrIPCVotes},
		"votant en double":         {withIPC(q4, dup), ErrGovernanceOrder},
		"votant non admis":         {withIPC(q4, withOutsider), ErrGovernanceVoter},
		"même période":             {withIPC(sameQ, ipcVotes(t, p, f[:7], sameQ)), ErrIPCPeriodOrder},
		"période antérieure":       {withIPC(earlier, ipcVotes(t, p, f[:7], earlier)), ErrIPCPeriodOrder},
		"votes sans mise à jour":   {func(b *Block) { b.IPCVotes = ipcVotes(t, p, f[:7], q4) }, ErrIPCVotesWithout},
	} {
		n.expectRefused(t, name, n.build(t, 1, f, c.edit), c.want)
	}

	votes := ipcVotes(t, p, f[:7], q4)
	n.produce(t, 1, f, withIPC(q4, votes))
	if n.st.IPC() != q4 || n.st.BaseIPC() != testIPC() {
		t.Fatalf("après vote : courant %+v, base %+v", n.st.IPC(), n.st.BaseIPC())
	}
	// Rejouer les mêmes votes : la période n'est plus postérieure.
	n.expectRefused(t, "rejeu", n.build(t, 2, f, withIPC(q4, votes)), ErrIPCPeriodOrder)

	// Trimestres manquants autorisés (l'INSEE n'a pas publié 2027-Q1).
	q2 := IPCRecord{Value: 104100, Period: "2027-Q2", SourceSHA256: [32]byte{0xcd}}
	n.produce(t, 2, f, withIPC(q2, ipcVotes(t, p, f[:7], q2)))
	if n.st.IPC() != q2 {
		t.Fatal("trimestre manquant refusé")
	}
}

// Seuil et votants comptés au moment de l'inclusion : 3 fondateurs inactifs → 7 actifs → 5 votes suffisent,
// et le vote d'un inactif ne compte pas.
func TestIPCVotersAtInclusion(t *testing.T) {
	p := testParams()
	n := newNet(t, p)
	active := n.founders[:7]
	n.produce(t, 30, active, nil)
	n.expectRefused(t, "vote d'un inactif", n.build(t, 31, active, withIPC(q4, ipcVotes(t, p, append(active[:4:4], n.founders[9]), q4))), ErrGovernanceVoter)
	n.produce(t, 31, active, withIPC(q4, ipcVotes(t, p, active[:5], q4)))
	if n.st.IPC() != q4 {
		t.Fatal("5 votes sur 7 actifs refusés")
	}
}

// admitNode fait parrainer et admettre un nouveau nœud par le fondateur 0.
func admitNode(t *testing.T, n *testNet) wallet {
	t.Helper()
	w := newWallet(t, n.p)
	n.produce(t, 1, n.founders, withRegs(n.register(t, w, n.founders[0], 1)))
	observe(t, n, w, 2, 8)
	if n.st.Status(w.addr) != StatusAdmitted {
		t.Fatal("prémisse : nœud admis")
	}
	return w
}

func TestSybilExclusionVote(t *testing.T) {
	p := testParams()
	n := newNet(t, p)
	fake := admitNode(t, n)
	f := n.founders
	other := newWallet(t, p)

	wrongTarget := exclusion(t, p, f[:7], fake.addr, 9)
	wrongTarget.Votes[2], _ = SignExclusionVote(n.keys[wrongTarget.Votes[2].Voter.Hash], f[5].addr, dayOf(day(9)), p)
	a, b := exclusion(t, p, f[:8], fake.addr, 9), exclusion(t, p, f[:8], f[9].addr, 9) // chacune valide seule
	unsorted := []Exclusion{a, b}
	if bytes.Compare(a.Target.Hash[:], b.Target.Hash[:]) < 0 {
		unsorted = []Exclusion{b, a}
	}

	for name, c := range map[string]struct {
		edit func(*Block)
		want error
	}{
		"6 votes sur 11":               {withExclusions(exclusion(t, p, f[:6], fake.addr, 9)), ErrQuorum},
		"7 votes sur 11":               {withExclusions(exclusion(t, p, f[:7], fake.addr, 9)), ErrQuorum},
		"un vote pour une autre cible": {withExclusions(wrongTarget), ErrExclusion},
		"vote de 30 jours":             {withExclusions(exclusion(t, p, f[:8], fake.addr, -21)), ErrExclusionVoteDays},
		"vote daté du futur":           {withExclusions(exclusion(t, p, f[:8], fake.addr, 10)), ErrExclusionVoteDays},
		"cible inconnue":               {withExclusions(exclusion(t, p, f[:8], other.addr, 9)), ErrExclusion},
		"exclusions non triées":        {withExclusions(unsorted...), ErrGovernanceOrder},
	} {
		n.expectRefused(t, name, n.build(t, 9, f, c.edit), c.want)
	}
	// La cible ne peut pas toucher sa part le jour même de son exclusion.
	n.expectRefused(t, "cible participante", n.build(t, 9, append(append([]wallet(nil), f...), fake),
		withExclusions(exclusion(t, p, f[:8], fake.addr, 9))), ErrEquivocatorPresent)

	// 8 votes sur 11 actifs, signés il y a 29 jours : exclusion.
	n.produce(t, 38, f, withExclusions(exclusion(t, p, f[:8], fake.addr, 9)))
	if n.st.Status(fake.addr) != StatusExcluded {
		t.Fatal("faux nœud non exclu")
	}
	if got := n.st.Node(f[0].addr).SponsorBanUntil; got != dayOf(day(38))+p.SponsorBanDays {
		t.Fatalf("parrain : interdiction jusqu'au jour %d", got)
	}
	if n.st.Status(f[0].addr) != StatusAdmitted {
		t.Fatal("le parrain doit rester admis")
	}
	n.expectRefused(t, "exclusion répétée", n.build(t, 39, f, withExclusions(exclusion(t, p, f[:8], fake.addr, 39))), ErrExclusion)
}

func TestGovernanceCodec(t *testing.T) {
	p := testParams()
	n := newNet(t, p)
	b := n.build(t, 1, n.founders, func(b *Block) {
		withIPC(q4, ipcVotes(t, p, n.founders[:7], q4))(b)
		b.Exclusions = []Exclusion{exclusion(t, p, n.founders[:8], n.founders[9].addr, 1)}
	})
	enc := EncodeBlock(&b)
	got, err := DecodeBlock(enc, p.AddressHRP)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(EncodeBlock(&got), enc) || got.Header.VotesRoot != VotesRoot(got.IPCVotes, got.Exclusions) {
		t.Fatal("aller-retour du bloc différent")
	}
	for i := 0; i < len(enc); i++ {
		if _, err := DecodeBlock(enc[:i], p.AddressHRP); err == nil {
			t.Fatalf("bloc tronqué à %d octets accepté", i)
		}
	}
	iv := b.IPCVotes[0]
	if d, err := DecodeIPCVote(EncodeIPCVote(&iv), p.AddressHRP); err != nil || d != iv {
		t.Fatalf("vote IPC : %v", err)
	}
	ev := b.Exclusions[0].Votes[0]
	if d, err := DecodeExclusionVote(EncodeExclusionVote(&ev), p.AddressHRP); err != nil || d != ev {
		t.Fatalf("vote d'exclusion : %v", err)
	}
	if r, err := DecodeIPCRecord(AppendIPCRecord(nil, q4)); err != nil || r != q4 {
		t.Fatalf("enregistrement IPC : %v", err)
	}
}

// La liste des votes est engagée par l'en-tête : en retirer un sans recalculer la racine est détecté.
func TestVotesRootBindsVotes(t *testing.T) {
	p := testParams()
	n := newNet(t, p)
	b := n.build(t, 1, n.founders, withIPC(q4, ipcVotes(t, p, n.founders[:8], q4)))
	b.IPCVotes = b.IPCVotes[:7] // toujours au-dessus du seuil, mais la liste ne correspond plus à l'en-tête
	if err := ValidateBlock(&b, &n.tip, p, n.st.IsAdmitted); err != ErrBlockVotesRoot {
		t.Fatalf("err = %v", err)
	}
}

// Votants et seuil se comptent sur le registre AVANT le bloc, même si ce bloc exclut un votant
// pour une autre raison (ici : double participation prouvée dans le même bloc).
func TestGovernanceUsesRegistryBeforeBlock(t *testing.T) {
	p := testParams()
	n := newNet(t, p)
	f := n.founders
	a, b := doubleParticipation(t, n, f[0], 1)
	ex := exclusion(t, p, f[:7], f[9].addr, 1) // f[0] vote ; 7 sur 10 actifs avant le bloc
	n.produce(t, 1, f[1:9], func(bl *Block) {
		bl.Equivocations = []Equivocation{NewEquivocation(a, b)}
		bl.Exclusions = []Exclusion{ex}
	})
	if n.st.Status(f[0].addr) != StatusExcluded || n.st.Status(f[9].addr) != StatusExcluded {
		t.Fatal("les deux exclusions doivent s'appliquer")
	}
}
