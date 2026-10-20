package oracle

import (
	"context"
	"errors"
	mrand "math/rand/v2"
	"testing"
	"time"

	"github.com/makoto-kanamoto/etl/internal/blockchain"
)

type fakeSource struct {
	data  []byte
	err   error
	calls []time.Time
	now   *time.Time
}

func (f *fakeSource) Fetch(context.Context) ([]Observation, error) {
	f.calls = append(f.calls, *f.now)
	if f.err != nil {
		return nil, f.err
	}
	return Parse(f.data, "011814630")
}

type fakeChain struct{ st *blockchain.State }

func (c fakeChain) ReadState(fn func(*blockchain.State)) { fn(c.st) }

func chainAt(period string) fakeChain {
	rec := blockchain.IPCRecord{Value: 100000, Period: period, SourceSHA256: [32]byte{1}}
	return fakeChain{blockchain.NewStateFrom(nil, nil, rec, rec, nil, nil, blockchain.EscrowData{})}
}

type fakeVoter struct {
	votes []blockchain.IPCRecord
	err   error
}

func (v *fakeVoter) SubmitIPCVote(r blockchain.IPCRecord) error {
	if v.err != nil {
		return v.err
	}
	v.votes = append(v.votes, r)
	return nil
}

type rig struct {
	now   time.Time
	src   *fakeSource
	voter *fakeVoter
	o     *Oracle
	logs  []string
}

func newRig(t *testing.T, chainPeriod string, data []byte) *rig {
	r := &rig{now: time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC), voter: &fakeVoter{}}
	r.src = &fakeSource{data: data, now: &r.now}
	r.o = New(Config{Source: r.src, Chain: chainAt(chainPeriod), Voter: r.voter,
		Rand: mrand.New(mrand.NewPCG(1, 2)), Logf: func(f string, a ...any) { r.logs = append(r.logs, f) }})
	return r
}

// tickDay appelle Tick toutes les minutes pendant 24 h.
func (r *rig) tickDay() {
	end := r.now.Add(24 * time.Hour)
	for ; r.now.Before(end); r.now = r.now.Add(time.Minute) {
		r.o.Tick(context.Background(), r.now)
	}
}

func TestOneReadPerDayInWindowThenVote(t *testing.T) {
	r := newRig(t, "2026-Q2", readFixture(t))
	r.tickDay()
	if len(r.src.calls) != 1 {
		t.Fatalf("%d lectures en un jour, attendu 1", len(r.src.calls))
	}
	at := r.src.calls[0]
	start := time.Date(2026, 10, 20, 20, 0, 0, 0, time.UTC)
	if at.Before(start) || !at.Before(start.Add(150*time.Minute)) {
		t.Fatalf("lecture à %s, hors de 20:00–22:30 UTC", at.Format("15:04"))
	}
	if len(r.voter.votes) != 1 || r.voter.votes[0].Period != "2026-Q3" || r.voter.votes[0].Value != 103070 {
		t.Fatalf("votes : %+v", r.voter.votes)
	}
	// Le lendemain : nouvelle lecture, vote rediffusé (période toujours pas inscrite), minute différente.
	r.tickDay()
	if len(r.src.calls) != 2 || len(r.voter.votes) != 2 || r.voter.votes[1] != r.voter.votes[0] {
		t.Fatalf("lendemain : %d lectures, votes %+v", len(r.src.calls), r.voter.votes)
	}
	if r.src.calls[1].Sub(r.src.calls[0]) == 24*time.Hour {
		t.Error("même minute deux jours de suite : tirage non renouvelé")
	}
}

// Sur de nombreux jours, la minute de lecture couvre la fenêtre et n'en sort jamais.
func TestReadMinuteSpread(t *testing.T) {
	r := newRig(t, "2026-Q3", readFixture(t))
	minutes := map[int]bool{}
	for d := 0; d < 200; d++ {
		day := r.now
		for r.now = day; len(r.src.calls) == d; r.now = r.now.Add(time.Minute) {
			r.o.Tick(context.Background(), r.now)
		}
		slot := r.o.next.Sub(day) // minute tirée (la lecture a lieu au premier Tick qui la suit)
		if slot < 20*time.Hour || slot >= 22*time.Hour+30*time.Minute {
			t.Fatalf("jour %d : minute tirée %s", d, slot)
		}
		m := int(slot.Minutes())
		minutes[m] = true
		r.now = day.Add(24 * time.Hour)
	}
	if len(minutes) < 50 {
		t.Fatalf("%d minutes distinctes en 200 jours : tirage trop concentré", len(minutes))
	}
}

func TestNoVoteWhenChainIsUpToDate(t *testing.T) {
	r := newRig(t, "2026-Q3", readFixture(t))
	r.tickDay()
	if len(r.src.calls) != 1 || len(r.voter.votes) != 0 {
		t.Fatalf("%d lectures, %d votes", len(r.src.calls), len(r.voter.votes))
	}
}

func TestRetriesThenGivesUpForTheDay(t *testing.T) {
	r := newRig(t, "2026-Q2", readFixture(t))
	r.src.err = errors.New("réseau indisponible")
	r.tickDay()
	c := r.src.calls
	if len(c) != 3 || c[1].Sub(c[0]) != 5*time.Minute || c[2].Sub(c[1]) != 15*time.Minute {
		t.Fatalf("tentatives : %v", c)
	}
	if len(r.voter.votes) != 0 || len(r.logs) != 3 {
		t.Fatalf("%d votes, %d journaux", len(r.voter.votes), len(r.logs))
	}
	r.src.err = nil // le lendemain, la source répond
	r.tickDay()
	if len(r.src.calls) != 4 || len(r.voter.votes) != 1 {
		t.Fatalf("lendemain : %d lectures, %d votes", len(r.src.calls), len(r.voter.votes))
	}
}

// Une seconde tentative réussie arrête les essais du jour.
func TestRetrySucceeds(t *testing.T) {
	r := newRig(t, "2026-Q2", readFixture(t))
	r.src.err = errors.New("délai dépassé")
	for r.now = r.now.Add(20 * time.Hour); len(r.src.calls) == 0; r.now = r.now.Add(time.Minute) {
		r.o.Tick(context.Background(), r.now)
	}
	r.src.err = nil
	r.tickDay()
	if len(r.src.calls) != 3 || len(r.voter.votes) != 2 { // échec, réussite, puis lecture du lendemain
		t.Fatalf("%d lectures, %d votes", len(r.src.calls), len(r.voter.votes))
	}
}

// Révision de l'INSEE : l'élément <Obs> change, le nœud vote pour le nouveau triplet.
func TestRevisionChangesVote(t *testing.T) {
	r := newRig(t, "2026-Q2", sdmx(series, obs("2026-09", "103.07", "DEF")))
	r.tickDay()
	r.src.data = sdmx(series, `<Obs TIME_PERIOD="2026-09" OBS_VALUE="103.07" OBS_REV="1" OBS_QUAL="DEF"/>`)
	r.tickDay()
	if len(r.voter.votes) != 2 {
		t.Fatalf("%d votes", len(r.voter.votes))
	}
	a, b := r.voter.votes[0], r.voter.votes[1]
	if a.Period != b.Period || a.Value != b.Value || a.SourceSHA256 == b.SourceSHA256 {
		t.Fatalf("révision : %+v puis %+v", a, b)
	}
}

func TestVoteRefusedIsLogged(t *testing.T) {
	r := newRig(t, "2026-Q2", readFixture(t))
	r.voter.err = errors.New("nœud passif")
	r.tickDay()
	if len(r.src.calls) != 1 || len(r.logs) != 1 {
		t.Fatalf("%d lectures, journaux %v", len(r.src.calls), r.logs)
	}
}

func TestQuarterOf(t *testing.T) {
	for in, want := range map[string]string{"2026-03": "2026-Q1", "2026-06": "2026-Q2", "2026-09": "2026-Q3", "2026-12": "2026-Q4"} {
		if got, err := quarterOf(in); err != nil || got != want {
			t.Errorf("%s → %s, %v", in, got, err)
		}
	}
	for _, in := range []string{"2026-01", "2026-08", "2026-13", "2026-00", "26-03", "2026-3", ""} {
		if _, err := quarterOf(in); err == nil {
			t.Errorf("%q accepté", in)
		}
	}
}

// Valeurs par défaut (tirage aléatoire, journal muet) et arrêt de Run à l'annulation.
func TestDefaultsAndRunStops(t *testing.T) {
	now := time.Date(2026, 10, 20, 23, 0, 0, 0, time.UTC)
	src := &fakeSource{data: readFixture(t), now: &now}
	o := New(Config{Source: src, Chain: chainAt("2026-Q2"), Voter: &fakeVoter{}})
	if rec := o.Tick(context.Background(), now); rec == nil || rec.Period != "2026-Q3" {
		t.Fatalf("23:00 : la minute tirée est passée, lecture et vote attendus : %+v", rec)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { o.Run(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run ne s'arrête pas à l'annulation")
	}
}
