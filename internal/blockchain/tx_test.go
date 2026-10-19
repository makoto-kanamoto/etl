package blockchain

import (
	"testing"

	"github.com/makoto-kanamoto/etl/internal/crypto"
)

func TestTxSignVerify(t *testing.T) {
	p := testParams()
	marie, paul := newWallet(t, p), newWallet(t, p)
	tx := makeTx(t, p, marie, paul.addr, 5*UnitsPerETL, 0, day(1))
	if len(tx.Body()) != txBodySize {
		t.Fatalf("taille du corps = %d", len(tx.Body()))
	}
	if err := tx.Verify(p); err != nil {
		t.Fatal(err)
	}
}

func TestTxSignWrongKey(t *testing.T) {
	p := testParams()
	marie, paul := newWallet(t, p), newWallet(t, p)
	tx := Transaction{From: marie.addr, To: paul.addr, Amount: 1}
	if err := tx.Sign(paul.key, p.ChainID); err != ErrTxSigner {
		t.Fatalf("err = %v", err)
	}
}

func TestTxVerifyRejects(t *testing.T) {
	p := testParams()
	marie, paul, eve := newWallet(t, p), newWallet(t, p), newWallet(t, p)
	base := makeTx(t, p, marie, paul.addr, 5*UnitsPerETL, 0, day(1))

	resign := func(tx Transaction) Transaction {
		if err := tx.Sign(marie.key, p.ChainID); err != nil {
			t.Fatal(err)
		}
		return tx
	}
	other := testParams()
	other.ChainID, other.AddressHRP = "ETL-DE", "etlde"

	cases := map[string]struct {
		tx   Transaction
		p    Params
		want error
	}{
		"montant modifié après signature": {func() Transaction {
			x := base
			x.Amount++
			x.Fee, _ = Fee(x.Amount, p.FeeBps)
			x.ID = x.ComputeID()
			return x
		}(), p, crypto.ErrSignerMismatch},
		"destinataire modifié": {func() Transaction { x := base; x.To = eve.addr; x.ID = x.ComputeID(); return x }(), p, crypto.ErrSignerMismatch},
		"nonce modifié":        {func() Transaction { x := base; x.Nonce = 7; x.ID = x.ComputeID(); return x }(), p, crypto.ErrSignerMismatch},
		"ID falsifié":          {func() Transaction { x := base; x.ID[0] ^= 1; return x }(), p, ErrTxID},
		"frais trop bas":       {resign(func() Transaction { x := base; x.Fee--; return x }()), p, ErrTxFee},
		"montant nul":          {resign(func() Transaction { x := base; x.Amount, x.Fee = 0, 0; return x }()), p, ErrTxZeroAmount},
		"envoi à soi-même":     {resign(func() Transaction { x := base; x.To = marie.addr; return x }()), p, ErrTxSelf},
		"rejouée sur ETL-DE":   {base, other, ErrTxNetwork},
	}
	for name, c := range cases {
		if err := c.tx.Verify(c.p); err != c.want {
			t.Errorf("%s : err = %v, attendu %v", name, err, c.want)
		}
	}
}

// Même avec les bons HRP, une signature ETL-FR ne vaut rien sur un autre chain_id.
func TestTxReplayOtherChainID(t *testing.T) {
	p := testParams()
	marie, paul := newWallet(t, p), newWallet(t, p)
	tx := makeTx(t, p, marie, paul.addr, UnitsPerETL, 0, day(1))
	fork := p
	fork.ChainID = "ETL-FR-FORK"
	if err := tx.Verify(fork); err == nil {
		t.Fatal("transaction rejouée sur un autre chain_id")
	}
}

// Une micro-transaction doit payer le minimum : 0 ou 1 unité de frais est refusé.
func TestTxMinFeeEnforced(t *testing.T) {
	p := testParams()
	marie, paul := newWallet(t, p), newWallet(t, p)
	tx := Transaction{From: marie.addr, To: paul.addr, Amount: 1, Fee: 0, Timestamp: day(1)}
	if err := tx.Sign(marie.key, p.ChainID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Verify(p); err != ErrTxFee {
		t.Fatalf("frais nuls : err = %v", err)
	}
	ok := makeTx(t, p, marie, paul.addr, 1, 0, day(1))
	if ok.Fee != p.MinFee || ok.Verify(p) != nil {
		t.Fatalf("frais minimum : %d", ok.Fee)
	}
}
