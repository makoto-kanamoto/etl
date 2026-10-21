package consensus

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/makoto-kanamoto/etl/internal/blockchain"
	"github.com/makoto-kanamoto/etl/internal/blockchain/chaintest"
	"github.com/makoto-kanamoto/etl/internal/oracle"
)

// Source simulée : la même réponse SDMX pour tous, ou une panne.
type sourceFunc func() ([]oracle.Observation, error)

func (f sourceFunc) Fetch(context.Context) ([]oracle.Observation, error) { return f() }

// De bout en bout : chaque nœud lit l'indice de son côté ; 3 lectures sur 10 échouent, les 7 autres
// votent le même triplet (quorum 7/10) et l'IPC est inscrit dans le bloc du jour.
func TestOracleEndToEnd(t *testing.T) {
	s := newSim(t)
	el := `<Obs TIME_PERIOD="2026-12" OBS_VALUE="104.12" OBS_STATUS="A" OBS_QUAL="DEF" OBS_TYPE="A" DATE_JO="2027-01-15"/>`
	resp := []byte(`<?xml version='1.0' encoding='UTF-8'?><DataSet><Series IDBANK="011814630" FREQ="M" DECIMALS="2">` + el + `</Series></DataSet>`)
	ok := sourceFunc(func() ([]oracle.Observation, error) { return oracle.Parse(resp, "011814630") })
	down := sourceFunc(func() ([]oracle.Observation, error) { return nil, errors.New("panne") })
	var oracles []*oracle.Oracle
	for i, n := range s.nodes {
		src := oracle.Fetcher(ok)
		if i >= 7 {
			src = down
		}
		oracles = append(oracles, oracle.New(oracle.Config{Source: src, Chain: n.store, Voter: n.e}))
	}
	// Juste avant le bloc du jour 1 : toutes les minutes de lecture (20:00–22:30) sont passées.
	at := time.Unix(chaintest.Day(1), 0).Add(-15 * time.Minute)
	s.at(at)
	for _, o := range oracles {
		o.Tick(context.Background(), at)
	}
	s.flush()
	s.runDay(1)
	s.requireHeight(1)
	want := blockchain.IPCRecord{Value: 104120, Period: "2026-Q4", SourceSHA256: sha256.Sum256([]byte(el))}
	for i, n := range s.nodes {
		var ipc blockchain.IPCRecord
		n.store.ReadState(func(st *blockchain.State) { ipc = st.IPC() })
		if ipc != want {
			t.Fatalf("nœud %d : IPC %+v, attendu %+v", i, ipc, want)
		}
	}
	if b := mustBlock(t, s, 1); len(b.IPCVotes) != 7 {
		t.Fatalf("%d votes IPC dans le bloc, attendu 7", len(b.IPCVotes))
	}
}

// Décomptes des votes en attente (page IPC et panneau opérateur du dashboard).
func TestTallies(t *testing.T) {
	s := newSim(t)
	s.at(time.Unix(chaintest.Day(1), 0).Add(-time.Hour))
	a := blockchain.IPCRecord{Value: 104000, Period: "2026-Q4", SourceSHA256: [32]byte{1}}
	b := blockchain.IPCRecord{Value: 104100, Period: "2026-Q4", SourceSHA256: [32]byte{2}}
	for i, n := range s.nodes[:5] {
		rec := a
		if i >= 3 {
			rec = b
		}
		_ = n.e.SubmitIPCVote(rec)
		if i < 2 {
			_ = n.e.SubmitParamVote(blockchain.ParamFeeBps, 20)
		}
	}
	s.flush()
	got := s.nodes[9].e.IPCTallies()
	if len(got) != 2 || got[0].Record != a || got[0].Votes != 3 || got[1].Votes != 2 {
		t.Fatalf("IPC : %+v", got)
	}
	pt := s.nodes[9].e.ParamTallies()
	if len(pt) != 1 || pt[0].ID != blockchain.ParamFeeBps || pt[0].Value != 20 || pt[0].Votes != 2 {
		t.Fatalf("paramètres : %+v", pt)
	}
	if s.nodes[3].e.Address() != s.nodes[3].wallet.Addr {
		t.Fatal("adresse du nœud")
	}
}
