package blockchain

import (
	"testing"
)

func TestApplyGenesis(t *testing.T) {
	p := testParams()
	s := NewState()
	g := testGenesis(t, p)
	if err := s.ApplyBlock(&g, p); err != nil {
		t.Fatal(err)
	}
	if len(s.accounts) != 0 {
		t.Fatal("le genesis a créé des ETL")
	}
	g.Participants = []Participation{{}}
	if err := NewState().ApplyBlock(&g, p); err != ErrGenesisContent {
		t.Fatalf("genesis avec participant : err = %v", err)
	}
	g.Participants, g.Txs = nil, []Transaction{{}}
	if err := NewState().ApplyBlock(&g, p); err != ErrGenesisContent {
		t.Fatalf("genesis avec transaction : err = %v", err)
	}
}

func TestRewardSharedBetweenParticipants(t *testing.T) {
	p := testParams()
	s := NewState()
	g := testGenesis(t, p)
	_ = s.ApplyBlock(&g, p)
	nodes := []wallet{newWallet(t, p), newWallet(t, p), newWallet(t, p)}
	b1, _ := buildBlock(t, p, &g.Header, nodes, nil, day(1))
	if err := s.ApplyBlock(&b1, p); err != nil {
		t.Fatal(err)
	}
	var total uint64
	for _, w := range nodes {
		got := s.Account(w.addr).Balance
		if got != 333_333_333 {
			t.Errorf("part = %s, attendu 3.33333333", FormatETL(got))
		}
		total += got
	}
	if total != 10*UnitsPerETL-1 {
		t.Errorf("émission du jour = %s", FormatETL(total))
	}
}

// Scénario : Marie, Paul et Victor participent au bloc 1 ;
// au bloc 2, Marie envoie 2 ETL à Paul. Les frais vont au proposant du bloc 2.
func TestTransferAndFees(t *testing.T) {
	p := testParams()
	s := NewState()
	g := testGenesis(t, p)
	marie, paul, victor := newWallet(t, p), newWallet(t, p), newWallet(t, p)
	nodes := []wallet{marie, paul, victor}
	_ = s.ApplyBlock(&g, p)
	b1, _ := buildBlock(t, p, &g.Header, nodes, nil, day(1))
	if err := s.ApplyBlock(&b1, p); err != nil {
		t.Fatal(err)
	}

	tx := makeTx(t, p, marie, paul.addr, 2*UnitsPerETL, 0, day(1)+3600)
	b2, _ := buildBlock(t, p, &b1.Header, nodes, []Transaction{tx}, day(2))
	if err := s.ApplyBlock(&b2, p); err != nil {
		t.Fatal(err)
	}

	const share, fee = 333_333_333, 200_000 // 2 ETL à 0.1 % = 0.002 ETL
	want := map[addrKey]uint64{
		marie.addr.Hash:  2*share - 2*UnitsPerETL - fee,
		paul.addr.Hash:   2*share + 2*UnitsPerETL,
		victor.addr.Hash: 2 * share,
	}
	want[b2.Header.Proposer.Hash] += fee
	for _, w := range nodes {
		if got := s.Account(w.addr).Balance; got != want[w.addr.Hash] {
			t.Errorf("solde = %s, attendu %s", FormatETL(got), FormatETL(want[w.addr.Hash]))
		}
	}
	if s.Account(marie.addr).Nonce != 1 {
		t.Error("nonce de Marie non incrémenté")
	}
}

func TestApplyBlockRejectsAndIsAtomic(t *testing.T) {
	p := testParams()
	g := testGenesis(t, p)
	marie, paul, victor := newWallet(t, p), newWallet(t, p), newWallet(t, p)

	setup := func() (*State, Block) {
		s := NewState()
		_ = s.ApplyBlock(&g, p)
		b1, _ := buildBlock(t, p, &g.Header, []wallet{marie}, nil, day(1)) // Marie seule : 10 ETL
		if err := s.ApplyBlock(&b1, p); err != nil {
			t.Fatal(err)
		}
		return s, b1
	}

	// Chaque cas : une transaction valide (Marie → Victor, 1 ETL, nonce 0), puis une fautive.
	// L'échec de la seconde doit annuler la première : tout ou rien.
	cases := map[string]struct {
		bad  Transaction
		want error
	}{
		"nonce rejoué":                      {makeTx(t, p, marie, paul.addr, UnitsPerETL, 0, day(1)), ErrNonce},
		"nonce en avance":                   {makeTx(t, p, marie, paul.addr, UnitsPerETL, 2, day(1)), ErrNonce},
		"solde insuffisant (frais compris)": {makeTx(t, p, marie, paul.addr, 9*UnitsPerETL, 1, day(1)), ErrInsufficientFunds},
		"compte vide":                       {makeTx(t, p, paul, marie.addr, 1, 0, day(1)), ErrInsufficientFunds},
		"transaction du futur":              {makeTx(t, p, marie, paul.addr, UnitsPerETL, 1, day(2)+1), ErrTxFuture},
	}
	for name, c := range cases {
		s, b1 := setup()
		txs := []Transaction{makeTx(t, p, marie, victor.addr, UnitsPerETL, 0, day(1)), c.bad}
		b2, _ := buildBlock(t, p, &b1.Header, []wallet{victor}, txs, day(2))
		before := s.accountsCopy()
		if err := s.ApplyBlock(&b2, p); err != c.want {
			t.Errorf("%s : err = %v, attendu %v", name, err, c.want)
		}
		if !equalAccounts(before, s.accountsCopy()) {
			t.Errorf("%s : état modifié malgré l'échec", name)
		}
	}
}

func (s *State) accountsCopy() map[addrKey]Account {
	m := make(map[addrKey]Account, len(s.accounts))
	for k, v := range s.accounts {
		m[k] = v
	}
	return m
}

func equalAccounts(a, b map[addrKey]Account) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
