package blockchain

import (
	"testing"
)

func TestRoundStart(t *testing.T) {
	p := DefaultParams()
	ts := day(1)
	for r, want := range map[uint8]string{0: "23:00:30", 1: "23:02:30", 4: "23:08:30"} {
		if got := RoundStart(ts, r, p).UTC().Format("15:04:05"); got != want {
			t.Errorf("tour %d : %s, attendu %s", r, got, want)
		}
	}
}

// atRound transforme un bloc construit en bloc du tour r : proposant = Participants[r].
func atRound(r uint8) func(*Block) {
	return func(b *Block) { b.Header.Round, b.Header.Proposer = r, b.Participants[r].Miner }
}

// Le proposant du tour 0 est hors ligne : le 2e ticket propose au tour 1, le bloc est finalisé.
// Le proposant défaillant garde sa part ; les frais vont au proposant de secours.
func TestFallbackProposer(t *testing.T) {
	p := testParams()
	n := newNet(t, p)
	n.produce(t, 1, n.founders, nil)
	tx := makeTx(t, p, n.founders[0], n.founders[1].addr, UnitsPerETL/2, 0, day(1))

	b := n.build(t, 2, n.founders, func(b *Block) { atRound(1)(b); b.Txs = []Transaction{tx} })
	absent, backup := b.Participants[0].Miner, b.Participants[1].Miner
	cert := certify(t, p, n.founders, &b.Header)
	if err := VerifyCertificate(&cert, &b.Header, n.st, p); err != nil {
		t.Fatal(err)
	}
	before := map[addrKey]uint64{}
	for _, w := range n.founders {
		before[w.addr.Hash] = n.st.Account(w.addr).Balance
	}
	if err := n.apply(&b); err != nil {
		t.Fatalf("bloc du tour 1 : %v", err)
	}
	const fee = 50_000 // 0.5 ETL à 0.1 %
	want := map[addrKey]uint64{}
	for _, w := range n.founders {
		want[w.addr.Hash] = before[w.addr.Hash] + UnitsPerETL // part : 10 ETL ÷ 10
	}
	want[n.founders[0].addr.Hash] -= UnitsPerETL/2 + fee
	want[n.founders[1].addr.Hash] += UnitsPerETL / 2
	want[backup.Hash] += fee
	for name, a := range map[string]addrKey{"défaillant": absent.Hash, "secours": backup.Hash} {
		if got := n.st.accounts[a].Balance; got != want[a] {
			t.Errorf("proposant %s : %s, attendu %s", name, FormatETL(got), FormatETL(want[a]))
		}
	}
}

func TestRoundValidation(t *testing.T) {
	p := testParams()
	n := newNet(t, p)
	for name, c := range map[string]struct {
		nodes []wallet
		edit  func(*Block)
		want  error
	}{
		"tour 1, proposant du tour 0": {n.founders, func(b *Block) { b.Header.Round = 1 }, ErrBlockProposer},
		"tour 0, proposant du tour 1": {n.founders, func(b *Block) { b.Header.Proposer = b.Participants[1].Miner }, ErrBlockProposer},
		"tour 5 (maximum 5 tours)": {n.founders, func(b *Block) {
			b.Header.Round, b.Header.Proposer = 5, b.Participants[5].Miner
		}, ErrBlockRound},
		"tour au-delà des participants": {n.founders[:3], func(b *Block) {
			b.Header.Round, b.Header.Proposer = 3, b.Participants[0].Miner
		}, ErrBlockRound},
	} {
		b := n.build(t, 1, c.nodes, c.edit)
		if err := ValidateBlock(&b, &n.tip, p, n.st.IsAdmitted); err != c.want {
			t.Errorf("%s : err = %v, attendu %v", name, err, c.want)
		}
	}
	for r := uint8(0); r < 5; r++ {
		b := n.build(t, 1, n.founders, atRound(r))
		if err := ValidateBlock(&b, &n.tip, p, n.st.IsAdmitted); err != nil {
			t.Errorf("tour %d valide refusé : %v", r, err)
		}
	}
}
