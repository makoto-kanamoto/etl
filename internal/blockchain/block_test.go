package blockchain

import (
	"context"
	"testing"
	"time"

	"github.com/makoto-kanamoto/etl/internal/crypto"
)

func TestTargetFromBits(t *testing.T) {
	cases := map[uint]string{
		0:  "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
		8:  "00ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
		21: "000007ffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
	}
	for bits, want := range cases {
		if got := TargetFromBits(bits).String(); got != want {
			t.Errorf("TargetFromBits(%d) = %s", bits, got)
		}
	}
}

func TestMineParticipation(t *testing.T) {
	p := testParams()
	g := testGenesis(t, p)
	w := newWallet(t, p)
	pt := participate(t, p, &g.Header, w, day(1))
	if pt.Miner != w.addr {
		t.Fatal("participation au mauvais nom")
	}
	if err := pt.Verify(g.Header.Hash(), day(1), p); err != nil {
		t.Fatal(err)
	}
	// Une PoW ne sert qu'un seul jour.
	if err := pt.Verify(g.Header.Hash(), day(2), p); err == nil {
		t.Error("participation réutilisée un autre jour")
	}
	var otherPrev crypto.Hash
	if err := pt.Verify(otherPrev, day(1), p); err == nil {
		t.Error("participation réutilisée sur un autre bloc précédent")
	}
}

func TestMineCanceled(t *testing.T) {
	p := testParams()
	p.Target = crypto.Hash{} // cible impossible
	w := newWallet(t, p)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := MineParticipation(ctx, w.key, crypto.Hash{}, day(1), p); err != ErrMiningCanceled {
		t.Fatalf("err = %v", err)
	}
}

// Personne ne peut participer au nom d'un autre, même avec une PoW valide.
func TestParticipationForgery(t *testing.T) {
	p := testParams()
	g := testGenesis(t, p)
	victim, attacker := newWallet(t, p), newWallet(t, p)
	pt := participate(t, p, &g.Header, victim, day(1))
	pt.Signature, _ = attacker.key.Sign(p.ChainID, crypto.PurposeParticipation,
		powMessage(g.Header.Hash(), day(1), pt.Miner, pt.Nonce))
	if err := pt.Verify(g.Header.Hash(), day(1), p); err != crypto.ErrSignerMismatch {
		t.Fatalf("err = %v", err)
	}
	// Une signature de transaction ne vaut pas participation.
	pt2 := participate(t, p, &g.Header, victim, day(1))
	pt2.Signature, _ = victim.key.Sign(p.ChainID, crypto.PurposeTx,
		powMessage(g.Header.Hash(), day(1), pt2.Miner, pt2.Nonce))
	if err := pt2.Verify(g.Header.Hash(), day(1), p); err == nil {
		t.Fatal("signature d'un autre usage acceptée")
	}
}

func TestHeaderEncodeCoversEveryField(t *testing.T) {
	p := testParams()
	w := newWallet(t, p)
	base := Header{Height: 1, Timestamp: day(1), Proposer: w.addr,
		IPC: &IPCRecord{Value: 103070, Period: "2026-Q3"}}
	mutations := map[string]func(*Header){
		"Height":           func(h *Header) { h.Height++ },
		"PrevHash":         func(h *Header) { h.PrevHash[0] ^= 1 },
		"Timestamp":        func(h *Header) { h.Timestamp++ },
		"TxRoot":           func(h *Header) { h.TxRoot[0] ^= 1 },
		"ParticipantsRoot": func(h *Header) { h.ParticipantsRoot[0] ^= 1 },
		"IPC nil":          func(h *Header) { h.IPC = nil },
		"IPC.Value":        func(h *Header) { h.IPC = &IPCRecord{Value: 103071, Period: "2026-Q3"} },
		"IPC.Period":       func(h *Header) { h.IPC = &IPCRecord{Value: 103070, Period: "2026-Q4"} },
		"IPC.SHA":          func(h *Header) { h.IPC = &IPCRecord{Value: 103070, Period: "2026-Q3", SourceSHA256: [32]byte{1}} },
		"Proposer":         func(h *Header) { h.Proposer.Hash[0] ^= 1 },
	}
	ref := base.Hash()
	for name, mutate := range mutations {
		h := base
		mutate(&h)
		if h.Hash() == ref {
			t.Errorf("%s : non couvert par le hash", name)
		}
	}
}

func TestTxRoot(t *testing.T) {
	id := func(b byte) Transaction { var x Transaction; x.ID[0] = b; return x }
	a, b, c := id(1), id(2), id(3)
	roots := map[string]crypto.Hash{
		"vide": TxRoot(nil),
		"a":    TxRoot([]Transaction{a}),
		"ab":   TxRoot([]Transaction{a, b}),
		"ba":   TxRoot([]Transaction{b, a}),
		"abc":  TxRoot([]Transaction{a, b, c}),
		"abcc": TxRoot([]Transaction{a, b, c, c}), // le piège Bitcoin (CVE-2012-2459)
	}
	if roots["a"] != crypto.Sum(crypto.ContextMerkle, append([]byte{0x00}, a.ID[:]...)) {
		t.Error("racine d'un seul élément ≠ feuille")
	}
	seen := map[crypto.Hash]string{}
	for name, r := range roots {
		if prev, ok := seen[r]; ok {
			t.Errorf("racines identiques : %s et %s", prev, name)
		}
		seen[r] = name
	}
}

func TestValidateBlock(t *testing.T) {
	p := testParams()
	g := testGenesis(t, p)
	nodes := []wallet{newWallet(t, p), newWallet(t, p), newWallet(t, p)}
	good, keys := buildBlock(t, p, &g.Header, nodes, nil, day(1))
	if err := ValidateBlock(&good, &g.Header, p, admitAll); err != nil {
		t.Fatalf("bloc valide rejeté : %v", err)
	}
	// Un jour sans bloc est permis.
	later, _ := buildBlock(t, p, &g.Header, nodes, nil, day(3))
	if err := ValidateBlock(&later, &g.Header, p, admitAll); err != nil {
		t.Fatalf("bloc après un jour manqué rejeté : %v", err)
	}

	// mutate modifie une copie du bloc ; resign recalcule racines et signature.
	mutate := func(resign bool, f func(*Block)) Block {
		b := good
		b.Participants = append([]Participation(nil), good.Participants...)
		f(&b)
		if resign {
			finalize(t, p, &b, keys)
		}
		return b
	}
	outsider := newWallet(t, p)
	keys[outsider.addr.Hash] = outsider.key
	foreignTx := makeTx(t, p, outsider, nodes[0].addr, 1000, 0, day(1))
	notAdmitted := func(a crypto.Address) bool { return a != nodes[2].addr }

	cases := map[string]struct {
		b        Block
		admitted func(crypto.Address) bool
		want     error
	}{
		"hauteur":                     {mutate(true, func(b *Block) { b.Header.Height = 5 }), admitAll, ErrBlockHeight},
		"précédent":                   {mutate(true, func(b *Block) { b.Header.PrevHash[0] ^= 1 }), admitAll, ErrBlockPrev},
		"pas 23:00":                   {mutate(true, func(b *Block) { b.Header.Timestamp = day(1) + 60 }), admitAll, ErrBlockTime},
		"même jour":                   {mutate(true, func(b *Block) { b.Header.Timestamp = day(0) }), admitAll, ErrBlockTime},
		"racine tx":                   {mutate(false, func(b *Block) { b.Txs = []Transaction{foreignTx} }), admitAll, ErrBlockTxRoot},
		"participant omis":            {mutate(false, func(b *Block) { b.Participants = b.Participants[:2] }), admitAll, ErrBlockPartRoot},
		"aucun participant":           {mutate(true, func(b *Block) { b.Participants = nil }), admitAll, ErrBlockNoParticipant},
		"ordre":                       {mutate(true, func(b *Block) { b.Participants[1], b.Participants[2] = b.Participants[2], b.Participants[1] }), admitAll, ErrBlockPartOrder},
		"doublon":                     {mutate(true, func(b *Block) { b.Participants[2] = b.Participants[1] }), admitAll, ErrBlockPartOrder},
		"non admis":                   {good, notAdmitted, ErrBlockNotAdmitted},
		"PoW falsifiée":               {mutate(true, func(b *Block) { b.Participants[1].Nonce++ }), admitAll, nil},
		"proposant ≠ meilleur ticket": {mutate(true, func(b *Block) { b.Header.Proposer = b.Participants[1].Miner }), admitAll, ErrBlockProposer},
		"signature d'un autre":        {mutate(false, func(b *Block) { _ = b.Sign(keys[b.Participants[1].Miner.Hash], p.ChainID) }), admitAll, crypto.ErrSignerMismatch},
		"IPC invalide":                {mutate(true, func(b *Block) { b.Header.IPC = &IPCRecord{Value: 1, Period: "2026-T3"} }), admitAll, ErrIPCPeriod},
		"IPC nulle":                   {mutate(true, func(b *Block) { b.Header.IPC = &IPCRecord{Period: "2026-Q3"} }), admitAll, ErrIPCValue},
	}
	for name, c := range cases {
		err := ValidateBlock(&c.b, &g.Header, p, c.admitted)
		if c.want == nil { // n'importe quelle erreur convient
			if err == nil {
				t.Errorf("%s : accepté à tort", name)
			}
			continue
		}
		if err != c.want {
			t.Errorf("%s : err = %v, attendu %v", name, err, c.want)
		}
	}
}
