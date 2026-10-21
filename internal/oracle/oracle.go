package oracle

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	mrand "math/rand/v2"
	"sync"
	"time"

	"github.com/makoto-kanamoto/etl/internal/blockchain"
)

// Calendrier : une lecture par jour à une minute tirée au hasard entre 20:00 et 22:30 UTC (avant le
// bloc de 23:00, sans que tous les nœuds sollicitent la source à la même seconde), au plus 3
// tentatives espacées de 5 puis 15 minutes. Le vote est rediffusé chaque jour tant que sa période
// n'est pas inscrite dans la chaîne (pertes réseau, nœuds arrivés après).
const (
	windowStart = 20 * time.Hour
	windowLen   = 150 * time.Minute
	maxAttempts = 3
)

var backoff = [maxAttempts - 1]time.Duration{5 * time.Minute, 15 * time.Minute}

// Fetcher lit les observations mensuelles (Source en production).
type Fetcher interface {
	Fetch(ctx context.Context) ([]Observation, error)
}

// Chain donne accès à l'état (storage.Store) : période IPC déjà inscrite.
type Chain interface {
	ReadState(func(*blockchain.State))
}

// Voter signe et diffuse le vote du nœud (consensus.Engine).
type Voter interface {
	SubmitIPCVote(blockchain.IPCRecord) error
}

// Config d'un oracle. À ne démarrer que sur un nœud qui vote (clé présente).
type Config struct {
	Source Fetcher
	Chain  Chain
	Voter  Voter
	Rand   *mrand.Rand          // tirage de la minute de lecture ; nil = graine aléatoire
	Logf   func(string, ...any) // journal (jamais de secret) ; nil = muet
}

// Oracle déclenche au plus une lecture réussie par jour UTC.
type Oracle struct {
	cfg Config

	mu       sync.Mutex
	day      int64     // jour UTC (secondes depuis l'époque, à minuit) du calendrier en cours
	next     time.Time // prochaine tentative
	attempts int
	done     bool // lecture réussie aujourd'hui
	status   Status
}

// Status décrit la dernière lecture (page IPC du dashboard).
type Status struct {
	LastAttempt time.Time             // zéro : jamais lu
	LastError   string                // vide si la dernière lecture a réussi
	Read        *blockchain.IPCRecord // dernier trimestre publié lu à la source (voté ou non)
	Voted       bool                  // un vote a été émis pour Read
	NextRead    time.Time             // prochaine lecture prévue (calendrier quotidien)
}

// MinManualInterval espace les lectures manuelles (courtoisie envers la source et sa limite de requêtes).
const MinManualInterval = 5 * time.Minute

// ErrTooSoon : lecture manuelle demandée moins de MinManualInterval après la précédente.
var ErrTooSoon = errors.New("oracle: lecture trop rapprochée de la précédente (5 minutes minimum)")

// New crée un oracle.
func New(cfg Config) *Oracle {
	if cfg.Rand == nil {
		cfg.Rand = mrand.New(mrand.NewPCG(mrand.Uint64(), mrand.Uint64()))
	}
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	return &Oracle{cfg: cfg, day: -1}
}

// Run appelle Tick toutes les 30 secondes jusqu'à l'annulation du contexte.
func (o *Oracle) Run(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			o.Tick(ctx, now.UTC())
		}
	}
}

// Tick lit la source si c'est l'heure, puis vote. Renvoie le triplet voté, ou nil.
func (o *Oracle) Tick(ctx context.Context, now time.Time) *blockchain.IPCRecord {
	o.mu.Lock()
	defer o.mu.Unlock()
	day := now.Unix() - now.Unix()%86400
	if day != o.day { // nouveau jour : nouvelle minute de lecture
		o.day, o.attempts, o.done = day, 0, false
		o.next = time.Unix(day, 0).UTC().Add(windowStart + time.Duration(o.cfg.Rand.Int64N(int64(windowLen))))
	}
	if o.done || o.attempts >= maxAttempts || now.Before(o.next) {
		return nil
	}
	o.attempts++
	rec, err := o.read(ctx, now)
	if err != nil {
		if o.attempts < maxAttempts {
			o.next = now.Add(backoff[o.attempts-1])
		}
		o.cfg.Logf("oracle : lecture IPC échouée (tentative %d/%d) : %v", o.attempts, maxAttempts, err)
		return nil
	}
	o.done = true
	return rec
}

// read (verrou tenu) lit la source, note le résultat dans le statut, et vote si un trimestre plus
// récent que la chaîne est publié. Renvoie le triplet voté, ou nil.
func (o *Oracle) read(ctx context.Context, now time.Time) (*blockchain.IPCRecord, error) {
	o.status.LastAttempt = now
	fctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	obs, err := o.cfg.Source.Fetch(fctx)
	if err != nil {
		o.status.LastError = err.Error()
		return nil, err
	}
	o.status.LastError, o.status.Read, o.status.Voted = "", nil, false
	if latest, ok := Latest(obs, ""); ok {
		o.status.Read = &latest
	}
	var current blockchain.IPCRecord
	o.cfg.Chain.ReadState(func(st *blockchain.State) { current = st.IPC() })
	rec, ok := Latest(obs, current.Period)
	if !ok {
		return nil, nil // rien de plus récent que la chaîne : pas de vote
	}
	if err := o.cfg.Voter.SubmitIPCVote(rec); err != nil {
		o.cfg.Logf("oracle : vote IPC %s refusé : %v", rec.Period, err)
		return nil, nil
	}
	o.status.Voted = true
	o.cfg.Logf("oracle : vote IPC %s = %d", rec.Period, rec.Value)
	return &rec, nil
}

// ReadNow lit la source immédiatement (bouton « relire la source maintenant »), au plus une fois
// toutes les MinManualInterval. N'altère pas le calendrier quotidien.
func (o *Oracle) ReadNow(ctx context.Context, now time.Time) (*blockchain.IPCRecord, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.status.LastAttempt.IsZero() && now.Sub(o.status.LastAttempt) < MinManualInterval {
		return nil, ErrTooSoon
	}
	return o.read(ctx, now)
}

// Status renvoie l'état de la dernière lecture.
func (o *Oracle) Status() Status {
	o.mu.Lock()
	defer o.mu.Unlock()
	s := o.status
	if !o.done && o.attempts < maxAttempts {
		s.NextRead = o.next
	}
	return s
}

var errNoQuarter = errors.New("oracle: aucun trimestre publié")

// Latest renvoie le triplet du trimestre le plus récent dont le dernier mois est publié en valeur
// définitive, s'il est strictement postérieur à la période after (« » : aucune). Un trimestre manqué
// n'est pas rattrapé : la valeur précédente reste en vigueur, puis on passe au plus récent.
func Latest(obs []Observation, after string) (blockchain.IPCRecord, bool) {
	var best blockchain.IPCRecord
	for _, o := range obs {
		period, err := quarterOf(o.Month)
		if err != nil || !o.Definitive || period <= best.Period {
			continue
		}
		best = blockchain.IPCRecord{Value: o.Value, Period: period, SourceSHA256: sha256.Sum256(o.Raw)}
	}
	// Format AAAA-Qn : l'ordre lexicographique est l'ordre chronologique.
	if best.Period == "" || best.Period <= after || best.Validate() != nil {
		return blockchain.IPCRecord{}, false
	}
	return best, true
}

// quarterOf renvoie le trimestre dont month est le dernier mois (« 2026-09 » → « 2026-Q3 »).
func quarterOf(month string) (string, error) {
	if !monthRe.MatchString(month) {
		return "", errNoQuarter
	}
	switch month[5:] {
	case "03", "06", "09", "12":
		m := (int(month[5]-'0')*10 + int(month[6]-'0')) / 3
		return fmt.Sprintf("%s-Q%d", month[:4], m), nil
	}
	return "", errNoQuarter
}
