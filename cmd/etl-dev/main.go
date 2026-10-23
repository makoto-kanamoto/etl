//go:build dev

// Serveur de développement du dashboard — JAMAIS livré (étiquette de compilation « dev »).
// Il sert l'interface depuis le disque et l'API réelle sur une chaîne simulée de 10 fondateurs :
// un bloc du « jour » suivant est produit chaque minute, avec les transactions reçues.
//
//	go run -tags dev ./cmd/etl-dev            puis http://localhost:3001
//
// Au démarrage, la phrase d'un fondateur (clé jetable, propre à cette session) est affichée : la
// restaurer dans le navigateur donne un portefeuille qui possède des ETL.
// Transactions, escrow (le nœud de développement peut être arbitre) et parrainage sont réels ;
// votes de paramètres et d'exclusion acceptés sans effet ; oracle et vote IPC simulés.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/makoto-kanamoto/etl/internal/api"
	"github.com/makoto-kanamoto/etl/internal/blockchain"
	"github.com/makoto-kanamoto/etl/internal/blockchain/chaintest"
	"github.com/makoto-kanamoto/etl/internal/consensus"
	"github.com/makoto-kanamoto/etl/internal/crypto"
	"github.com/makoto-kanamoto/etl/internal/escrow"
	"github.com/makoto-kanamoto/etl/internal/mempool"
	ioracle "github.com/makoto-kanamoto/etl/internal/oracle"
	"github.com/makoto-kanamoto/etl/internal/wallet"
)

// tb permet d'utiliser chaintest hors des tests : un échec arrête le serveur.
type tb struct{ testing.TB }

func (tb) Helper()                   {}
func (tb) Fatal(a ...any)            { log.Fatal(a...) }
func (tb) Fatalf(f string, a ...any) { log.Fatalf(f, a...) }

// chain : la chaîne simulée, protégée par un verrou (lue par l'API, avancée par la boucle).
type chain struct {
	mu     sync.Mutex
	net    *chaintest.Net
	blocks map[uint64]blockchain.Block
	day    int
}

func (c *chain) Tip() blockchain.Header { c.mu.Lock(); defer c.mu.Unlock(); return c.net.Tip }
func (c *chain) HeaderAt(h uint64) (blockchain.Header, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if h == 0 {
		return c.net.Genesis.Header, nil
	}
	if b, ok := c.blocks[h]; ok {
		return b.Header, nil
	}
	return blockchain.Header{}, errors.New("bloc inconnu")
}
func (c *chain) BlockAt(h uint64) (blockchain.Block, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if b, ok := c.blocks[h]; ok {
		return b, nil
	}
	return blockchain.Block{}, errors.New("bloc inconnu")
}
func (c *chain) RewardAt(h uint64) (uint64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if h == 0 {
		return 0, true
	}
	r, ok := c.net.Rewards[h]
	return r, ok
}

func (c *chain) HeightOf(h crypto.Hash) (uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if h == c.net.Genesis.Header.Hash() {
		return 0, nil
	}
	for height, b := range c.blocks {
		if b.Header.Hash() == h {
			return height, nil
		}
	}
	return 0, errors.New("bloc inconnu")
}
func (c *chain) ReadState(f func(*blockchain.State)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	f(c.net.State)
}

// next produit le bloc du jour suivant avec les transactions et opérations d'escrow en attente.
func (c *chain) next(e *engine) blockchain.Block {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.day++
	ts := chaintest.Day(c.day)
	e.pool.SetParams(c.net.State.ParamsAt(c.net.P, ts))
	txs := e.pool.Select(c.net.State, ts, 1000)
	ops := c.net.State.FilterEscrowOps(txs, e.ops, ts, 1000-len(txs), c.net.P)
	var regs []blockchain.Registration
	for _, r := range e.regs {
		if c.net.State.CheckRegistration(r, ts, c.net.P) == nil {
			regs = append(regs, r)
		}
	}
	sort.Slice(regs, func(i, j int) bool { return bytes.Compare(regs[i].Candidate.Hash[:], regs[j].Candidate.Hash[:]) < 0 })
	e.regs = nil
	b, _ := c.net.Next(tb{}, c.day, func(b *blockchain.Block) { b.Txs, b.EscrowOps, b.Registrations = txs, ops, regs })
	c.blocks[b.Header.Height] = b
	e.pool.Update(c.net.State, time.Now())
	var kept []blockchain.EscrowOp
	for i := range e.ops {
		if c.net.State.CheckEscrowOp(&e.ops[i], chaintest.Day(c.day+1), c.net.P) == nil {
			kept = append(kept, e.ops[i])
		}
	}
	e.ops = kept
	return b
}

// engine : la part du moteur dont l'API a besoin, sur un vrai mempool.
type engine struct {
	c    *chain
	pool *mempool.Pool
	ops  []blockchain.EscrowOp // sous le verrou de la chaîne
	regs []blockchain.Registration
	key  *crypto.PrivateKey // clé du nœud de développement (fondateur jetable)
	me   crypto.Address
}

var errDev = errors.New("non pris en charge par le serveur de développement")

func (e *engine) SubmitTransaction(tx blockchain.Transaction) error {
	e.c.mu.Lock()
	defer e.c.mu.Unlock()
	e.pool.SetParams(e.c.net.State.ParamsAt(e.c.net.P, chaintest.Day(e.c.day+1)))
	return e.pool.Add(tx, e.c.net.State, time.Now())
}
func (e *engine) SubmitEscrowOp(op blockchain.EscrowOp) error {
	if err := op.VerifySignatures(e.c.net.P); err != nil {
		return err
	}
	e.c.mu.Lock()
	defer e.c.mu.Unlock()
	if err := e.c.net.State.CheckEscrowOp(&op, chaintest.Day(e.c.day+1), e.c.net.P); err != nil {
		return err
	}
	e.ops = append(e.ops, op)
	return nil
}
func (e *engine) CosignEscrow(op *blockchain.EscrowOp) error {
	return escrow.Cosign(e.key, op, time.Now(), e.c.net.P)
}
func (e *engine) ArbiterSign(op *blockchain.EscrowOp) error {
	e.c.mu.Lock()
	st, ok := e.c.net.State.Escrow(op.Escrow)
	e.c.mu.Unlock()
	if !ok {
		return blockchain.ErrEscrowUnknown
	}
	return escrow.AddSignature(e.key, op, blockchain.SignerArbiter, op.Escrow, st, e.c.net.P)
}
func (e *engine) ArbiterAct(kind blockchain.EscrowKind, id crypto.Hash) (blockchain.EscrowOp, error) {
	e.c.mu.Lock()
	st, ok := e.c.net.State.Escrow(id)
	e.c.mu.Unlock()
	if !ok {
		return blockchain.EscrowOp{}, blockchain.ErrEscrowUnknown
	}
	return escrow.Act(e.key, kind, id, st.Seq, blockchain.SignerArbiter, e.c.net.P)
}
func (e *engine) SubmitParamVote(id blockchain.ParamID, v uint64) error {
	e.c.mu.Lock()
	cur := e.c.net.State.ParamsAt(e.c.net.P, chaintest.Day(e.c.day+1)).Get(id)
	e.c.mu.Unlock()
	return blockchain.CheckParamChange(id, cur, v) // sans effet : vérifié seulement
}
func (e *engine) SubmitExclusionVote(crypto.Address) error { return nil }
func (e *engine) IPCTallies() []consensus.IPCTally {
	return []consensus.IPCTally{{Record: blockchain.IPCRecord{Value: 104120, Period: "2026-Q4", SourceSHA256: [32]byte{0xed, 0xa2}}, Votes: 4}}
}
func (e *engine) PrepareRegistration(sponsor crypto.Address) (blockchain.Registration, error) {
	return blockchain.Registration{}, blockchain.ErrRegistrationStatus // le nœud de développement est déjà admis
}
func (e *engine) SponsorRegistration(r *blockchain.Registration) error {
	if r.IsReactivation() || r.Sponsor.Hash != e.me.Hash {
		return blockchain.ErrSponsorNotEligible
	}
	if err := r.SignSponsor(e.key, e.c.net.P.ChainID); err != nil {
		return err
	}
	e.c.mu.Lock()
	defer e.c.mu.Unlock()
	return e.c.net.State.CheckRegistration(*r, chaintest.Day(e.c.day+1), e.c.net.P)
}
func (e *engine) Reactivate() (blockchain.Registration, error) {
	return blockchain.Registration{}, blockchain.ErrRegistrationStatus
}
func (e *engine) SubmitRegistration(r blockchain.Registration) error {
	e.c.mu.Lock()
	defer e.c.mu.Unlock()
	e.regs = append(e.regs, r)
	return nil
}
func (e *engine) ParamTallies() []consensus.ParamTally { return nil }
func (e *engine) Address() crypto.Address              { return e.me }
func (e *engine) NextNonce(a crypto.Address) uint64 {
	e.c.mu.Lock()
	defer e.c.mu.Unlock()
	return e.pool.NextNonce(e.c.net.State, a)
}

// oracle simulé : une lecture réussie au démarrage, relecture immédiate.
type oracle struct {
	mu sync.Mutex
	st ioracle.Status
}

func (o *oracle) Status() ioracle.Status { o.mu.Lock(); defer o.mu.Unlock(); return o.st }
func (o *oracle) ReadNow(_ context.Context, now time.Time) (*blockchain.IPCRecord, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if now.Sub(o.st.LastAttempt) < ioracle.MinManualInterval {
		return nil, ioracle.ErrTooSoon
	}
	o.st.LastAttempt = now
	return o.st.Read, nil
}

type network struct{}

func (network) PeerCount() (int, int)             { return 4, 5 }
func (network) Halted() bool                      { return false }
func (network) ClockOffset() (time.Duration, int) { return 0, 9 }

func main() {
	port := flag.Int("port", 3001, "port local")
	static := flag.String("web", "web", "répertoire de l'interface (servie depuis le disque)")
	every := flag.Duration("every", time.Minute, "intervalle entre deux blocs simulés")
	flag.Parse()

	p := chaintest.Params()
	c := &chain{net: chaintest.New(tb{}, p), blocks: map[uint64]blockchain.Block{}}
	founder := c.net.Founders[0]
	eng := &engine{c: c, pool: mempool.New(p, mempool.DefaultOptions()), key: founder.Key, me: founder.Addr}
	// Historique jusqu'aux dernières 24 h : la tête de chaîne précède l'heure réelle (horodatages du
	// navigateur) et reste assez récente pour que l'accueil indique un réseau en bonne santé.
	for chaintest.Day(c.day+1) < time.Now().Unix() {
		c.next(eng)
	}
	words, _ := wallet.EncodePhrase(founder.Key.Bytes(), wallet.French)
	dir, _ := os.MkdirTemp("", "etl-dev-")
	defer os.RemoveAll(dir)
	// Prochain bloc simulé : affiché tel quel par l'accueil (« dans moins d'une minute »).
	var nextAt atomic.Int64
	nextAt.Store(time.Now().Add(*every).Unix())
	srv, err := api.New(api.Config{Params: p, Port: *port, IPCSource: "https://api.insee.fr/series/BDM/V1/data/SERIES_BDM/011814630", NextBlock: func() time.Time { return time.Unix(nextAt.Load(), 0) }, TokenPath: filepath.Join(dir, "dashboard.token"), Version: "dev",
		Static: os.DirFS(*static), Poll: 500 * time.Millisecond, Chain: c,
		Engine: eng, Network: network{}, Oracle: &oracle{st: ioracle.Status{LastAttempt: time.Now().Add(-time.Hour),
			Read: &blockchain.IPCRecord{Value: 104120, Period: "2026-Q4", SourceSHA256: [32]byte{0xed, 0xa2}}, Voted: true,
			NextRead: time.Now().Add(20 * time.Hour)}}})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Serveur de développement : http://localhost:%d (un bloc toutes les %s)\n", *port, *every)
	fmt.Printf("Fondateur %s — phrase (clé jetable de cette session) :\n  %s\n", founder.Addr, strings.Join(words, " "))
	other, _ := wallet.EncodePhrase(c.net.Founders[1].Key.Bytes(), wallet.French)
	fmt.Printf("Fondateur %s — phrase :\n  %s\n", c.net.Founders[1].Addr, strings.Join(other, " "))
	tok, _ := os.ReadFile(filepath.Join(dir, "dashboard.token"))
	fmt.Printf("Jeton opérateur : %s", tok)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go func() {
		t := time.NewTicker(*every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				nextAt.Store(time.Now().Add(*every).Unix())
				b := c.next(eng)
				log.Printf("bloc %d (%s), %d transaction(s), %d opération(s) d'escrow", b.Header.Height,
					time.Unix(b.Header.Timestamp, 0).UTC().Format("2006-01-02"), len(b.Txs), len(b.EscrowOps))
			}
		}
	}()
	if err := srv.ListenAndServe(ctx); err != nil {
		log.Fatal(err)
	}
}
