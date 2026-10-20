package blockchain

import (
	"bytes"
	"sort"
	"testing"

	"github.com/makoto-kanamoto/etl/internal/crypto"
)

func TestParamGetWith(t *testing.T) {
	p := DefaultParams()
	for id := ParamID(1); id <= lastParamID; id++ {
		v := p.Get(id) + 1
		if got := p.With(id, v).Get(id); got != v {
			t.Errorf("%s : With/Get incohérents", id.Name())
		}
		if id.Name() == "" {
			t.Errorf("paramètre %d sans nom", id)
		}
	}
	if p.With(ParamTargetBits, 22).Target != TargetFromBits(22) {
		t.Fatal("la cible PoW doit suivre le nombre de bits")
	}
}

func TestCheckParamChange(t *testing.T) {
	cases := []struct {
		name     string
		id       ParamID
		old, new uint64
		want     error
	}{
		{"frais doublés", ParamFeeBps, 10, 20, nil},
		{"frais plus que doublés", ParamFeeBps, 10, 21, ErrParamStep},
		{"frais divisés par deux", ParamFeeBps, 10, 5, nil},
		{"frais divisés par plus de deux", ParamFeeBps, 10, 4, ErrParamStep},
		{"frais au-delà de 1 %", ParamFeeBps, 60, 101, ErrParamBounds},
		{"frais de 0 à 1", ParamFeeBps, 0, 1, nil},
		{"frais de 0 à 2", ParamFeeBps, 0, 2, ErrParamStep},
		{"récompense sous 1 ETL", ParamDailyReward, UnitsPerETL, UnitsPerETL / 2, ErrParamBounds},
		{"récompense au-delà de 100 ETL", ParamDailyReward, 80 * UnitsPerETL, 101 * UnitsPerETL, ErrParamBounds},
		{"taille de bloc doublée", ParamMaxBlockTxs, 100_000, 200_000, nil},
		{"difficulté +2 bits", ParamTargetBits, 21, 23, nil},
		{"difficulté +3 bits", ParamTargetBits, 21, 24, ErrParamStep},
		{"difficulté sous 16 bits", ParamTargetBits, 17, 15, ErrParamBounds},
		{"observation sans limite de variation", ParamObservationDays, 7, 30, nil},
		{"observation sous 3 jours", ParamObservationDays, 7, 2, ErrParamBounds},
		{"valeur inchangée", ParamFeeBps, 10, 10, ErrParamUnchanged},
		{"paramètre inconnu", 99, 1, 2, ErrParamUnknown},
	}
	for _, c := range cases {
		if err := CheckParamChange(c.id, c.old, c.new); err != c.want {
			t.Errorf("%s : err = %v, attendu %v", c.name, err, c.want)
		}
	}
}

func paramVotes(t testing.TB, p Params, ws []wallet, id ParamID, v uint64, signDay int) []ParamVote {
	t.Helper()
	var out []ParamVote
	for _, w := range ws {
		pv, err := SignParamVote(w.key, id, v, dayOf(day(signDay)), p)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, pv)
	}
	sort.Slice(out, func(i, j int) bool { return bytes.Compare(out[i].Voter.Hash[:], out[j].Voter.Hash[:]) < 0 })
	return out
}

func withParams(chs ...ParamChange) func(*Block) { return func(b *Block) { b.ParamChanges = chs } }

func change(t testing.TB, p Params, ws []wallet, id ParamID, v uint64, signDay int) ParamChange {
	return ParamChange{ID: id, Value: v, Votes: paramVotes(t, p, ws, id, v, signDay)}
}

// Cycle complet : vote au jour 1 → en attente 30 jours → appliqué au jour 31.
// Une transaction au montant de frais périmé est alors refusée.
func TestParamChangeLifecycle(t *testing.T) {
	p := testParams()
	n := newNet(t, p)
	f := n.founders
	n.produce(t, 1, f, withParams(change(t, p, f[:7], ParamFeeBps, 20, 1)))
	pend := n.st.PendingParams()[ParamFeeBps]
	if pend.Value != 20 || pend.ActivationDay != dayOf(day(1))+ParamActivationDays {
		t.Fatalf("changement en attente : %+v", pend)
	}
	if got := n.st.ParamsAt(p, day(30)).FeeBps; got != 10 {
		t.Fatalf("jour 30 : frais %d, attendu 10 (pas encore actif)", got)
	}
	if got := n.st.ParamsAt(p, day(31)).FeeBps; got != 20 {
		t.Fatalf("jour 31 : frais %d, attendu 20", got)
	}

	oldFee := makeTx(t, p, f[0], f[1].addr, UnitsPerETL/2, 0, day(30)) // 0,1 % : périmé au jour 31
	b := n.build(t, 31, f, func(b *Block) { b.Txs = []Transaction{oldFee} })
	if err := n.apply(&b); err != ErrTxFee {
		t.Fatalf("frais périmés : err = %v", err)
	}
	newP := p.With(ParamFeeBps, 20)
	newFee := makeTx(t, newP, f[0], f[1].addr, UnitsPerETL/2, 0, day(30))
	n.produce(t, 31, f, func(b *Block) { b.Txs = []Transaction{newFee} })
	if len(n.st.PendingParams()) != 0 || n.st.ParamsAt(p, day(31)).FeeBps != 20 {
		t.Fatal("changement non appliqué dans l'état")
	}
}

func TestParamChangeRejects(t *testing.T) {
	p := testParams()
	n := newNet(t, p)
	f := n.founders
	outsider := newWallet(t, p)

	mixed := change(t, p, f[:7], ParamFeeBps, 20, 1)
	mixed.Votes[2], _ = SignParamVote(n.keys[mixed.Votes[2].Voter.Hash], ParamFeeBps, 19, dayOf(day(1)), p)
	withOutsider := ParamChange{ID: ParamFeeBps, Value: 20, Votes: paramVotes(t, p, append(append([]wallet(nil), f[:7]...), outsider), ParamFeeBps, 20, 1)}
	a, b := change(t, p, f[:7], ParamFeeBps, 20, 1), change(t, p, f[:7], ParamDailyReward, 20*UnitsPerETL, 1)

	for name, c := range map[string]struct {
		chs  []ParamChange
		want error
	}{
		"6 votes sur 10":           {[]ParamChange{change(t, p, f[:6], ParamFeeBps, 20, 1)}, ErrQuorum},
		"un vote pour autre chose": {[]ParamChange{mixed}, ErrParamVote},
		"votant non admis":         {[]ParamChange{withOutsider}, ErrGovernanceVoter},
		"hors bornes":              {[]ParamChange{change(t, p, f[:7], ParamFeeBps, 101, 1)}, ErrParamBounds},
		"variation trop forte":     {[]ParamChange{change(t, p, f[:7], ParamFeeBps, 21, 1)}, ErrParamStep},
		"vote de 30 jours":         {[]ParamChange{change(t, p, f[:7], ParamFeeBps, 20, -29)}, ErrParamVoteDays},
		"vote daté du futur":       {[]ParamChange{change(t, p, f[:7], ParamFeeBps, 20, 2)}, ErrParamVoteDays},
		"non triés":                {[]ParamChange{b, a}, ErrGovernanceOrder},
		"observation min > durée":  {[]ParamChange{change(t, p, f[:7], ParamObservationMinDays, 8, 1)}, ErrParamRelation},
	} {
		n.expectRefused(t, name, n.build(t, 1, f, withParams(c.chs...)), c.want)
	}

	// Un seul changement en attente par paramètre.
	n.produce(t, 1, f, withParams(a))
	n.expectRefused(t, "second changement en attente", n.build(t, 2, f, withParams(change(t, p, f[:7], ParamFeeBps, 15, 2))), ErrParamPending)
	// Après activation, un nouveau changement est possible.
	n.produce(t, 31, f, nil)
	n.produce(t, 32, f, withParams(change(t, p, f[:7], ParamFeeBps, 40, 32)))
}

// La récompense quotidienne change par vote ; la nouvelle valeur s'applique au jour d'activation.
func TestDailyRewardChange(t *testing.T) {
	p := testParams()
	n := newNet(t, p)
	f := n.founders
	n.produce(t, 1, f, withParams(change(t, p, f[:7], ParamDailyReward, 20*UnitsPerETL, 1)))
	before := n.st.Account(f[0].addr).Balance
	n.produce(t, 30, f, nil)
	if got := n.st.Account(f[0].addr).Balance - before; got != UnitsPerETL {
		t.Fatalf("jour 30 : part %s, attendu 1 (10 ETL ÷ 10)", FormatETL(got))
	}
	before = n.st.Account(f[0].addr).Balance
	n.produce(t, 31, f, nil)
	if got := n.st.Account(f[0].addr).Balance - before; got != 2*UnitsPerETL {
		t.Fatalf("jour 31 : part %s, attendu 2 (20 ETL ÷ 10)", FormatETL(got))
	}
}

// La difficulté change par vote : une participation minée à l'ancienne difficulté devient invalide.
func TestTargetBitsChange(t *testing.T) {
	p := testParams()
	p.TargetBits, p.Target = 16, TargetFromBits(16)
	n := newNet(t, p)
	f := n.founders
	n.produce(t, 1, f, withParams(change(t, p, f[:7], ParamTargetBits, 18, 1)))
	n.produce(t, 30, f, nil) // encore 16 bits
	p31 := n.st.ParamsAt(p, day(31))
	if p31.TargetBits != 18 {
		t.Fatalf("difficulté au jour 31 : %d bits", p31.TargetBits)
	}
	// Nonce valide à 16 bits mais pas à 18 : cherché délibérément (le test reste déterministe).
	prev := n.tip.Hash()
	pt := Participation{Miner: f[0].addr}
	for h := pt.PoWHash(prev, day(31)); !h.MeetsTarget(p.Target) || h.MeetsTarget(p31.Target); h = pt.PoWHash(prev, day(31)) {
		pt.Nonce++
	}
	pt.Signature, _ = f[0].key.Sign(p.ChainID, crypto.PurposeParticipation, powMessage(prev, day(31), pt.Miner, pt.Nonce))
	if pt.Verify(prev, day(31), p) != nil {
		t.Fatal("prémisse : participation valide à 16 bits")
	}
	if pt.Verify(prev, day(31), p31) == nil {
		t.Fatal("participation à l'ancienne difficulté acceptée")
	}
	if good := participate(t, p31, prev, f[0], day(31)); good.Verify(prev, day(31), p31) != nil {
		t.Fatal("participation à la nouvelle difficulté refusée")
	}
	n.produce(t, 31, f, nil) // le bloc du jour 31 se mine à 18 bits
}

func TestParamCodec(t *testing.T) {
	p := testParams()
	n := newNet(t, p)
	ch := change(t, p, n.founders[:7], ParamFeeBps, 20, 1)
	b := n.build(t, 1, n.founders, withParams(ch))
	enc := EncodeBlock(&b)
	got, err := DecodeBlock(enc, p.AddressHRP)
	if err != nil || !bytes.Equal(EncodeBlock(&got), enc) || len(got.ParamChanges) != 1 {
		t.Fatalf("bloc : %v", err)
	}
	v := ch.Votes[0]
	if d, err := DecodeParamVote(EncodeParamVote(&v), p.AddressHRP); err != nil || d != v {
		t.Fatalf("vote : %v", err)
	}
	n.produce(t, 1, n.founders, withParams(ch))
	gov := n.st.EncodeGov()
	values, pending, err := DecodeGov(gov)
	if err != nil || values[ParamFeeBps] != 10 || pending[ParamFeeBps].Value != 20 {
		t.Fatalf("paramètres de l'état : %v", err)
	}
	for i := 0; i < len(gov); i++ {
		if _, _, err := DecodeGov(gov[:i]); err == nil {
			t.Fatalf("paramètres tronqués à %d octets acceptés", i)
		}
	}
	if s := NewStateFrom(n.st.accounts, n.st.nodes, n.st.baseIPC, n.st.ipc, values, pending, EscrowData{}); s.Root() != n.st.Root() {
		t.Fatal("état rechargé : racine différente")
	}
}

func FuzzDecodeGov(f *testing.F) {
	g := newGov(DefaultParams())
	g.pending[ParamFeeBps] = PendingParam{Value: 20, ActivationDay: 100}
	f.Add(g.encode())
	f.Fuzz(func(t *testing.T, in []byte) {
		values, pending, err := DecodeGov(in)
		if err != nil {
			return
		}
		if !bytes.Equal((govState{values, pending}).encode(), in) {
			t.Fatal("encodage non canonique accepté")
		}
	})
}
