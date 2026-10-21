package mempool

import (
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/makoto-kanamoto/etl/internal/blockchain"
	"github.com/makoto-kanamoto/etl/internal/blockchain/chaintest"
	"github.com/makoto-kanamoto/etl/internal/crypto"
)

// setup : une chaîne où chaque fondateur possède 1 ETL (bloc 1), et un mempool vide.
func setup(t *testing.T) (*chaintest.Net, *Pool) {
	t.Helper()
	net := chaintest.New(t, chaintest.Params())
	net.Next(t, 1, nil)
	return net, New(net.P, DefaultOptions())
}

// now : 12:00 UTC le jour 2 (avant le bloc de 23:00).
func now() time.Time { return time.Unix(chaintest.Day(2), 0).Add(-11 * time.Hour) }

func tx(t *testing.T, net *chaintest.Net, from chaintest.Wallet, to crypto.Address, amount, nonce uint64) blockchain.Transaction {
	return net.Tx(t, from, to, amount, nonce, now().Unix())
}

func TestAddRules(t *testing.T) {
	net, m := setup(t)
	f := net.Founders
	a, b := f[0], f[1]
	if err := m.Add(tx(t, net, a, b.Addr, blockchain.UnitsPerETL/10, 0), net.State, now()); err != nil {
		t.Fatal(err)
	}
	conflict := tx(t, net, a, f[2].Addr, blockchain.UnitsPerETL/10, 0)
	future := net.Tx(t, a, b.Addr, 1000, 1, now().Add(time.Minute).Unix())
	old := net.Tx(t, a, b.Addr, 1000, 1, now().Add(-8*24*time.Hour).Unix())
	badFee := tx(t, net, a, b.Addr, 1000, 1)
	badFee.Fee = 1
	_ = badFee.Sign(a.Key, net.P.ChainID)

	for name, c := range map[string]struct {
		tx   blockchain.Transaction
		want error
	}{
		"doublon":                   {tx(t, net, a, b.Addr, blockchain.UnitsPerETL/10, 0), ErrDuplicate},
		"même nonce, autre tx":      {conflict, ErrConflict},
		"nonce trop en avance (16)": {tx(t, net, a, b.Addr, 1000, 16), ErrNonceTooHigh},
		"datée du futur":            {future, ErrFuture},
		"plus de 7 jours":           {old, ErrExpired},
		"frais sous le minimum":     {badFee, blockchain.ErrTxFee},
		"solde cumulé insuffisant":  {tx(t, net, a, b.Addr, 9*blockchain.UnitsPerETL/10, 1), ErrInsufficientFunds},
	} {
		if err := m.Add(c.tx, net.State, now()); !errors.Is(err, c.want) {
			t.Errorf("%s : err = %v, attendu %v", name, err, c.want)
		}
	}
	if m.Len() != 1 {
		t.Fatalf("%d transactions en attente, attendu 1", m.Len())
	}
	// Un nonce en avance (trou) est accepté : il attendra le précédent.
	if err := m.Add(tx(t, net, a, b.Addr, 1000, 2), net.State, now()); err != nil {
		t.Fatalf("nonce avec trou : %v", err)
	}
}

// Un montant énorme dont « montant + frais » dépasse la capacité d'un uint64 ne doit jamais
// passer pour finançable : il ferait échouer tout le bloc du proposant.
func TestCostOverflowRejected(t *testing.T) {
	net, m := setup(t)
	// Montant choisi pour que montant + frais (0.1 %) fasse exactement 2^64 : le coût « reboucle » à 0,
	// sous le solde de 1 ETL. Sans contrôle du dépassement, la transaction serait admise.
	const wraps = 18428315757951600016
	huge := tx(t, net, net.Founders[0], net.Founders[1].Addr, wraps, 0)
	if err := huge.Verify(net.P); err != nil {
		t.Fatalf("prémisse : la transaction est bien formée : %v", err)
	}
	if huge.Amount+huge.Fee != 0 || huge.Amount > math.MaxUint64 {
		t.Fatal("prémisse : le coût doit reboucler à 0")
	}
	if err := m.Add(huge, net.State, now()); !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("err = %v", err)
	}
}

func TestLimits(t *testing.T) {
	net := chaintest.New(t, chaintest.Params())
	net.Next(t, 1, nil)
	m := New(net.P, Options{MaxPerAccount: 3, MaxTotal: 4, MaxAge: 7 * 24 * time.Hour})
	f := net.Founders
	for n := uint64(0); n < 3; n++ {
		if err := m.Add(tx(t, net, f[0], f[1].Addr, 1000, n), net.State, now()); err != nil {
			t.Fatal(err)
		}
	}
	// 3 en attente, nonce 3 refusé (hors fenêtre de 3 nonces) ; la limite par compte joue aussi.
	if err := m.Add(tx(t, net, f[0], f[1].Addr, 1000, 3), net.State, now()); !errors.Is(err, ErrNonceTooHigh) {
		t.Fatalf("4e transaction du compte : err = %v", err)
	}
	if err := m.Add(tx(t, net, f[2], f[1].Addr, 1000, 0), net.State, now()); err != nil {
		t.Fatal(err)
	}
	if err := m.Add(tx(t, net, f[3], f[1].Addr, 1000, 0), net.State, now()); !errors.Is(err, ErrPoolFull) {
		t.Fatalf("mempool plein : err = %v", err)
	}
}

func TestSelectOrder(t *testing.T) {
	net, m := setup(t)
	f := net.Founders
	add := func(tx blockchain.Transaction) {
		t.Helper()
		if err := m.Add(tx, net.State, now()); err != nil {
			t.Fatal(err)
		}
	}
	// Arrivées : f0 nonce 1 (trou), f2 nonce 0, f0 nonce 0, f3 nonce 0.
	t01 := tx(t, net, f[0], f[1].Addr, 1000, 1)
	t20 := tx(t, net, f[2], f[1].Addr, 1000, 0)
	t00 := tx(t, net, f[0], f[1].Addr, 1000, 0)
	t30 := tx(t, net, f[3], f[1].Addr, 1000, 0)
	for _, x := range []blockchain.Transaction{t01, t20, t00, t30} {
		add(x)
	}
	got := m.Select(net.State, chaintest.Day(2), 100)
	want := []crypto.Hash{t20.ID, t00.ID, t01.ID, t30.ID} // FIFO, mais nonce 0 avant nonce 1
	if len(got) != len(want) {
		t.Fatalf("%d sélectionnées, attendu %d", len(got), len(want))
	}
	for i := range want {
		if got[i].ID != want[i] {
			t.Fatalf("position %d : mauvais ordre", i)
		}
	}
	if sel := m.Select(net.State, chaintest.Day(2), 2); len(sel) != 2 {
		t.Fatalf("maximum de 2 : %d sélectionnées", len(sel))
	}
}

func TestSelectSkipsGapsAndLateTxs(t *testing.T) {
	net, m := setup(t)
	f := net.Founders
	// f0 : nonce 1 seulement (le 0 manque) → rien.
	_ = m.Add(tx(t, net, f[0], f[1].Addr, 1000, 1), net.State, now())
	// f2 : nonce 0 horodaté après 23:00 → rien, ni son nonce 1.
	late := net.Tx(t, f[2], f[1].Addr, 1000, 0, chaintest.Day(2)+1)
	if err := m.Add(late, net.State, time.Unix(chaintest.Day(2)+60, 0)); err != nil {
		t.Fatal(err)
	}
	_ = m.Add(tx(t, net, f[2], f[1].Addr, 1000, 1), net.State, now())
	if got := m.Select(net.State, chaintest.Day(2), 100); len(got) != 0 {
		t.Fatalf("%d sélectionnées, attendu 0", len(got))
	}
}

// Un crédit reçu plus tôt dans le même bloc finance une transaction suivante.
func TestSelectChainedFunding(t *testing.T) {
	net, m := setup(t)
	f := net.Founders
	poor := chaintest.NewWallet(t, net.P)
	toPoor := tx(t, net, f[0], poor.Addr, blockchain.UnitsPerETL/2, 0)
	if err := m.Add(toPoor, net.State, now()); err != nil {
		t.Fatal(err)
	}
	// Le mempool refuse une dépense d'un compte vide (les crédits en attente ne comptent pas)…
	fromPoor := tx(t, net, poor, f[1].Addr, blockchain.UnitsPerETL/10, 0)
	if err := m.Add(fromPoor, net.State, now()); !errors.Is(err, ErrInsufficientFunds) {
		t.Fatalf("err = %v", err)
	}
	// … mais une fois le crédit finalisé, elle passe.
	b, _ := net.Next(t, 2, func(b *blockchain.Block) { b.Txs = m.Select(net.State, chaintest.Day(2), 100) })
	m.Update(net.State, now().Add(24*time.Hour))
	if len(b.Txs) != 1 || m.Len() != 0 {
		t.Fatalf("bloc : %d tx, mempool : %d", len(b.Txs), m.Len())
	}
	if err := m.Add(net.Tx(t, poor, f[1].Addr, blockchain.UnitsPerETL/10, 0, chaintest.Day(2)+60), net.State, now().Add(24*time.Hour)); err != nil {
		t.Fatalf("après crédit : %v", err)
	}
}

// accounts : état contrôlé par le test.
type accounts map[crypto.Address]blockchain.Account

func (a accounts) Account(x crypto.Address) blockchain.Account { return a[x] }

func TestUpdate(t *testing.T) {
	net, m := setup(t)
	f := net.Founders
	keep := tx(t, net, f[0], f[1].Addr, 1000, 0)
	next := tx(t, net, f[0], f[1].Addr, 1000, 1)
	half := tx(t, net, f[2], f[1].Addr, blockchain.UnitsPerETL/2, 0)
	quarter := tx(t, net, f[2], f[1].Addr, blockchain.UnitsPerETL/4, 1)
	for _, x := range []blockchain.Transaction{keep, next, half, quarter} {
		if err := m.Add(x, net.State, now()); err != nil {
			t.Fatal(err)
		}
	}
	// Après le bloc : keep inclus (nonce de f0 = 1) ; f2 n'a plus que 0.6 ETL :
	// half (0.5005) reste couverte, quarter (0.25025) ne l'est plus.
	after := accounts{
		f[0].Addr: {Balance: blockchain.UnitsPerETL, Nonce: 1},
		f[2].Addr: {Balance: 6 * blockchain.UnitsPerETL / 10, Nonce: 0},
	}
	m.Update(after, now().Add(12*time.Hour))
	for name, c := range map[string]struct {
		id   crypto.Hash
		want bool
	}{"incluse": {keep.ID, false}, "suivante": {next.ID, true}, "encore couverte": {half.ID, true}, "plus couverte": {quarter.ID, false}} {
		if m.Has(c.id) != c.want {
			t.Errorf("%s : présente = %v, attendu %v", name, m.Has(c.id), c.want)
		}
	}
	// Solde effondré : la première n'est plus couverte, et tout ce qui en dépend part avec elle.
	after[f[2].Addr] = blockchain.Account{Balance: blockchain.UnitsPerETL / 10}
	m.Update(after, now().Add(12*time.Hour))
	if m.Has(half.ID) {
		t.Error("transaction non couverte conservée")
	}
	// Expiration après 7 jours.
	m.Update(after, now().Add(8*24*time.Hour))
	if m.Len() != 0 {
		t.Fatalf("%d transactions expirées restantes", m.Len())
	}
}

// La sélection du mempool forme toujours un bloc que le consensus accepte.
func TestSelectionIsValidBlock(t *testing.T) {
	net, m := setup(t)
	f := net.Founders
	for i, w := range f {
		for n := uint64(0); n < 3; n++ {
			_ = m.Add(tx(t, net, w, f[(i+1)%len(f)].Addr, blockchain.UnitsPerETL/10, n), net.State, now())
		}
	}
	sel := m.Select(net.State, chaintest.Day(2), net.P.MaxBlockTxs)
	if len(sel) == 0 {
		t.Fatal("rien sélectionné")
	}
	net.Next(t, 2, func(b *blockchain.Block) { b.Txs = sel }) // échoue si le bloc est invalide
}

func TestConcurrentAccess(t *testing.T) {
	net, m := setup(t)
	f := net.Founders
	txs := make([]blockchain.Transaction, 0, 50)
	for i := 0; i < 50; i++ {
		txs = append(txs, tx(t, net, f[i%10], f[(i+1)%10].Addr, 1000, uint64(i/10)))
	}
	var wg sync.WaitGroup
	for i := range txs {
		wg.Add(2)
		go func(x blockchain.Transaction) { defer wg.Done(); _ = m.Add(x, net.State, now()) }(txs[i])
		go func() { defer wg.Done(); _ = m.Select(net.State, chaintest.Day(2), 100); _ = m.Len() }()
	}
	wg.Wait()
	if m.Len() == 0 {
		t.Fatal("aucune transaction ajoutée")
	}
}

func TestNonceTooLow(t *testing.T) {
	net, m := setup(t)
	f := net.Founders
	used := tx(t, net, f[0], f[1].Addr, 1000, 0)
	net.Next(t, 2, func(b *blockchain.Block) { b.Txs = []blockchain.Transaction{used} })
	replay := net.Tx(t, f[0], f[2].Addr, 1000, 0, chaintest.Day(2)+60) // nonce 0 déjà consommé
	if err := m.Add(replay, net.State, now().Add(12*time.Hour)); !errors.Is(err, ErrNonceTooLow) {
		t.Fatalf("err = %v", err)
	}
}

// Frais changés par vote : après SetParams, Update écarte les transactions au montant de frais périmé,
// et Add refuse les nouvelles.
func TestSetParamsDropsStaleFees(t *testing.T) {
	net, m := setup(t)
	f := net.Founders
	old := tx(t, net, f[0], f[1].Addr, blockchain.UnitsPerETL/2, 0)
	if err := m.Add(old, net.State, now()); err != nil {
		t.Fatal(err)
	}
	newP := net.P.With(blockchain.ParamFeeBps, 20)
	m.SetParams(newP)
	m.Update(net.State, now())
	if m.Has(old.ID) {
		t.Fatal("transaction aux frais périmés conservée")
	}
	if err := m.Add(old, net.State, now()); !errors.Is(err, blockchain.ErrTxFee) {
		t.Fatalf("frais périmés à l'admission : err = %v", err)
	}
	fee, _ := blockchain.RequiredFee(blockchain.UnitsPerETL/2, newP)
	fresh := blockchain.Transaction{From: f[0].Addr, To: f[1].Addr, Amount: blockchain.UnitsPerETL / 2, Fee: fee, Timestamp: now().Unix()}
	_ = fresh.Sign(f[0].Key, net.P.ChainID)
	if err := m.Add(fresh, net.State, now()); err != nil {
		t.Fatalf("nouveaux frais : %v", err)
	}
}

func TestNextNonce(t *testing.T) {
	net, m := setup(t)
	a, b := net.Founders[0], net.Founders[1]
	if got := m.NextNonce(net.State, a.Addr); got != 0 {
		t.Fatalf("compte sans attente : %d", got)
	}
	for n := uint64(0); n < 3; n++ {
		if err := m.Add(tx(t, net, a, b.Addr, 1000, n), net.State, now()); err != nil {
			t.Fatal(err)
		}
	}
	if got := m.NextNonce(net.State, a.Addr); got != 3 {
		t.Fatalf("trois en attente : %d", got)
	}
	// Un trou (nonce 4 sans 3) ne compte pas : la suite s'arrête au premier manquant.
	if err := m.Add(tx(t, net, a, b.Addr, 1000, 4), net.State, now()); err != nil {
		t.Fatal(err)
	}
	if got := m.NextNonce(net.State, a.Addr); got != 3 {
		t.Fatalf("avec un trou : %d", got)
	}
}
