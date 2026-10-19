package blockchain

import (
	"testing"
)

func TestGenesisContentRejected(t *testing.T) {
	p := testParams()
	g, err := Genesis(p, GenesisMessage, testIPC(), day(0), addrs(newWallets(t, p, 10)))
	if err != nil {
		t.Fatal(err)
	}
	for name, edit := range map[string]func(*Block){
		"participation": func(b *Block) { b.Participants = []Participation{{}} },
		"transaction":   func(b *Block) { b.Txs = []Transaction{{}} },
		"candidature":   func(b *Block) { b.Registrations = []Registration{{}} },
	} {
		b := g
		edit(&b)
		if err := NewState().ApplyBlock(&b, p); err != ErrGenesisContent {
			t.Errorf("%s : err = %v", name, err)
		}
	}
}

// 10 fondateurs participent : chacun reçoit 1 ETL.
func TestRewardSharedBetweenParticipants(t *testing.T) {
	p := testParams()
	n := newNet(t, p)
	n.produce(t, 1, n.founders, nil)
	for _, w := range n.founders {
		if got := n.st.Account(w.addr).Balance; got != UnitsPerETL {
			t.Errorf("part = %s, attendu 1", FormatETL(got))
		}
	}
}

// Seuls 3 des 10 fondateurs participent : la récompense est partagée entre les 3 présents.
func TestRewardOnlyToPresentNodes(t *testing.T) {
	p := testParams()
	n := newNet(t, p)
	n.produce(t, 1, n.founders[:3], nil)
	for i, w := range n.founders {
		want := uint64(0)
		if i < 3 {
			want = 333_333_333
		}
		if got := n.st.Account(w.addr).Balance; got != want {
			t.Errorf("fondateur %d : %s, attendu %s", i, FormatETL(got), FormatETL(want))
		}
	}
}

// Moins de 10 nœuds actifs : aucune récompense (personne ne peut accumuler seul au démarrage).
func TestNoRewardBelowMinActiveNodes(t *testing.T) {
	p := testParams()
	n := newNet(t, p)
	// 7 fondateurs restent absents 30 jours : ils deviennent inactifs.
	n.produce(t, 31, n.founders[:3], nil)
	if n.st.ActiveCount() != 3 {
		t.Fatalf("%d actifs, attendu 3", n.st.ActiveCount())
	}
	before := n.st.Account(n.founders[0].addr).Balance
	n.produce(t, 32, n.founders[:3], nil)
	if got := n.st.Account(n.founders[0].addr).Balance; got != before {
		t.Fatalf("récompense émise avec 3 nœuds actifs : %s", FormatETL(got-before))
	}
}

// Au bloc 2, un fondateur envoie 0.5 ETL à un autre ; les frais vont au proposant.
func TestTransferAndFees(t *testing.T) {
	p := testParams()
	n := newNet(t, p)
	marie, paul := n.founders[0], n.founders[1]
	n.produce(t, 1, n.founders, nil)
	tx := makeTx(t, p, marie, paul.addr, UnitsPerETL/2, 0, day(1)+3600)
	b2 := n.produce(t, 2, n.founders, func(b *Block) { b.Txs = []Transaction{tx} })

	const fee = 50_000 // 0.5 ETL à 0.1 % = 0.0005 ETL
	want := map[addrKey]uint64{}
	for _, w := range n.founders {
		want[w.addr.Hash] = 2 * UnitsPerETL
	}
	want[marie.addr.Hash] -= UnitsPerETL/2 + fee
	want[paul.addr.Hash] += UnitsPerETL / 2
	want[b2.Header.Proposer.Hash] += fee
	for _, w := range n.founders {
		if got := n.st.Account(w.addr).Balance; got != want[w.addr.Hash] {
			t.Errorf("solde = %s, attendu %s", FormatETL(got), FormatETL(want[w.addr.Hash]))
		}
	}
	if n.st.Account(marie.addr).Nonce != 1 {
		t.Error("nonce de Marie non incrémenté")
	}
}

func TestApplyBlockRejectsAndIsAtomic(t *testing.T) {
	p := testParams()
	n := newNet(t, p)
	n.produce(t, 1, n.founders, nil) // chacun : 1 ETL
	marie, paul, victor := n.founders[0], n.founders[1], n.founders[2]
	stranger := newWallet(t, p)

	// Chaque cas : une transaction valide (Marie → Victor, 0.1 ETL, nonce 0), puis une fautive.
	// L'échec de la seconde doit tout annuler, registre compris.
	cases := map[string]struct {
		bad  Transaction
		want error
	}{
		"nonce rejoué":                      {makeTx(t, p, marie, paul.addr, UnitsPerETL/10, 0, day(1)), ErrNonce},
		"nonce en avance":                   {makeTx(t, p, marie, paul.addr, UnitsPerETL/10, 2, day(1)), ErrNonce},
		"solde insuffisant (frais compris)": {makeTx(t, p, marie, paul.addr, 9*UnitsPerETL/10, 1, day(1)), ErrInsufficientFunds},
		"compte vide":                       {makeTx(t, p, stranger, marie.addr, 1, 0, day(1)), ErrInsufficientFunds},
		"transaction du futur":              {makeTx(t, p, marie, paul.addr, UnitsPerETL/10, 1, day(2)+1), ErrTxFuture},
	}
	for name, c := range cases {
		txs := []Transaction{makeTx(t, p, marie, victor.addr, UnitsPerETL/10, 0, day(1)), c.bad}
		b := n.build(t, 2, n.founders, func(b *Block) { b.Txs = txs })
		accounts, nodes := n.st.accountsCopy(), n.st.nodes.clone()
		if err := n.st.ApplyBlock(&b, p); err != c.want {
			t.Errorf("%s : err = %v, attendu %v", name, err, c.want)
		}
		if !equalMaps(accounts, n.st.accounts) || !equalMaps(nodes, n.st.nodes) {
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

func equalMaps[V comparable](a, b map[addrKey]V) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}
