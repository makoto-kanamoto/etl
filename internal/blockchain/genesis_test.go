package blockchain

import "testing"

func TestGenesis(t *testing.T) {
	p := DefaultParams()
	g := testGenesis(t, p)
	h := g.Header
	if h.Height != 0 || len(g.Txs) != 0 || h.IPC == nil || h.IPC.Value != 103070 {
		t.Fatalf("genesis inattendu : %+v", h)
	}
	if g2 := testGenesis(t, p); g2.Header.Hash() != h.Hash() {
		t.Fatal("genesis non déterministe")
	}
}

func TestGenesisCommitsToChainAndMessage(t *testing.T) {
	ref := GenesisPrevHash("ETL-FR", GenesisMessage)
	if GenesisPrevHash("ETL-DE", GenesisMessage) == ref {
		t.Error("le chain_id ne change pas le genesis")
	}
	if GenesisPrevHash("ETL-FR", GenesisMessage+".") == ref {
		t.Error("le message ne change pas le genesis")
	}
	// Séparation par longueur : ("ETL-F", "R…") ≠ ("ETL-FR", "…").
	if GenesisPrevHash("ETL-F", "R"+GenesisMessage) == ref {
		t.Error("ambiguïté entre chain_id et message")
	}
}

func TestGenesisRejects(t *testing.T) {
	p := DefaultParams()
	if _, err := Genesis(p, GenesisMessage, IPCRecord{}, day(0)); err != ErrGenesisIPC {
		t.Errorf("IPC absent : err = %v", err)
	}
	if _, err := Genesis(p, GenesisMessage, IPCRecord{Value: 103070, Period: "2026-Q3"}, day(0)+1); err != ErrBlockTime {
		t.Errorf("heure ≠ 23:00 : err = %v", err)
	}
}
