package blockchain

import (
	"testing"

	"github.com/makoto-kanamoto/etl/internal/crypto"
)

func testIPC() IPCRecord { return IPCRecord{Value: 103070, Period: "2026-Q3"} }

func TestGenesis(t *testing.T) {
	p := DefaultParams()
	founders := addrs(newWallets(t, p, 10))
	g, err := Genesis(p, GenesisMessage, testIPC(), day(0), founders)
	if err != nil {
		t.Fatal(err)
	}
	h := g.Header
	if h.Height != 0 || len(g.Txs) != 0 || len(g.Participants) != 0 || h.IPC == nil || h.IPC.Value != 103070 {
		t.Fatalf("genesis inattendu : %+v", h)
	}
	g2, _ := Genesis(p, GenesisMessage, testIPC(), day(0), founders)
	if g2.Header.Hash() != h.Hash() {
		t.Fatal("genesis non déterministe")
	}
	s := NewState()
	if err := s.ApplyBlock(&g, p); err != nil {
		t.Fatal(err)
	}
	if s.ActiveCount() != 10 || len(s.accounts) != 0 {
		t.Fatalf("après genesis : %d actifs, %d comptes", s.ActiveCount(), len(s.accounts))
	}
	for _, f := range founders {
		if n := s.Node(f); n.Status != StatusAdmitted || !n.Founder || n.Sponsor != (addrKey{}) {
			t.Errorf("fondateur mal inscrit : %+v", n)
		}
	}
}

func TestGenesisCommitments(t *testing.T) {
	p := DefaultParams()
	founders := addrs(newWallets(t, p, 10))
	ref := GenesisPrevHash("ETL-FR", GenesisMessage, founders)
	other := append(append([]crypto.Address(nil), founders[:9]...), newWallet(t, p).addr)
	for name, h := range map[string]crypto.Hash{
		"chain_id":        GenesisPrevHash("ETL-DE", GenesisMessage, founders),
		"message":         GenesisPrevHash("ETL-FR", GenesisMessage+".", founders),
		"fondateurs":      GenesisPrevHash("ETL-FR", GenesisMessage, other),
		"ambiguïté":       GenesisPrevHash("ETL-F", "R"+GenesisMessage, founders),
		"sans fondateurs": GenesisPrevHash("ETL-FR", GenesisMessage, nil),
	} {
		if h == ref {
			t.Errorf("%s : ne change pas le genesis", name)
		}
	}
}

func TestGenesisRejects(t *testing.T) {
	p := DefaultParams()
	ten := addrs(newWallets(t, p, 10))
	dup := append(append([]crypto.Address(nil), ten[:9]...), ten[0])
	foreign := append([]crypto.Address(nil), ten...)
	foreign[3].HRP = "etlde"

	cases := map[string]struct {
		founders []crypto.Address
		ipc      IPCRecord
		ts       int64
		want     error
	}{
		"9 fondateurs":     {ten[:9], testIPC(), day(0), ErrFounders},
		"fondateur doublé": {dup, testIPC(), day(0), ErrFounders},
		"autre réseau":     {foreign, testIPC(), day(0), ErrFounders},
		"IPC absent":       {ten, IPCRecord{}, day(0), ErrGenesisIPC},
		"heure ≠ 23:00":    {ten, testIPC(), day(0) + 1, ErrBlockTime},
	}
	for name, c := range cases {
		if _, err := Genesis(p, GenesisMessage, c.ipc, c.ts, c.founders); err != c.want {
			t.Errorf("%s : err = %v, attendu %v", name, err, c.want)
		}
	}
}
