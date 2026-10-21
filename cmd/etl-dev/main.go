//go:build dev

// Serveur de développement du dashboard — JAMAIS livré (étiquette de compilation « dev »).
// Il sert l'interface depuis le disque et l'API réelle sur une chaîne simulée de 10 fondateurs :
// un bloc du « jour » suivant est produit chaque minute, avec les transactions reçues.
//
//	go run -tags dev ./cmd/etl-dev            puis http://localhost:3001
//
// Au démarrage, la phrase d'un fondateur (clé jetable, propre à cette session) est affichée : la
// restaurer dans le navigateur donne un portefeuille qui possède des ETL.
// Seules les transactions sont prises en charge (escrow, votes : sous-étape suivante).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/makoto-kanamoto/etl/internal/api"
	"github.com/makoto-kanamoto/etl/internal/blockchain"
	"github.com/makoto-kanamoto/etl/internal/blockchain/chaintest"
	"github.com/makoto-kanamoto/etl/internal/consensus"
	"github.com/makoto-kanamoto/etl/internal/crypto"
	"github.com/makoto-kanamoto/etl/internal/mempool"
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
func (c *chain) ReadState(f func(*blockchain.State)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	f(c.net.State)
}

// next produit le bloc du jour suivant avec les transactions du mempool.
func (c *chain) next(pool *mempool.Pool) blockchain.Block {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.day++
	ts := chaintest.Day(c.day)
	pool.SetParams(c.net.State.ParamsAt(c.net.P, ts))
	txs := pool.Select(c.net.State, ts, 1000)
	b, _ := c.net.Next(tb{}, c.day, func(b *blockchain.Block) { b.Txs = txs })
	c.blocks[b.Header.Height] = b
	pool.Update(c.net.State, time.Now())
	return b
}

// engine : la part du moteur dont l'API a besoin, sur un vrai mempool.
type engine struct {
	c    *chain
	pool *mempool.Pool
	me   crypto.Address
}

var errDev = errors.New("non pris en charge par le serveur de développement")

func (e *engine) SubmitTransaction(tx blockchain.Transaction) error {
	e.c.mu.Lock()
	defer e.c.mu.Unlock()
	e.pool.SetParams(e.c.net.State.ParamsAt(e.c.net.P, chaintest.Day(e.c.day+1)))
	return e.pool.Add(tx, e.c.net.State, time.Now())
}
func (e *engine) SubmitEscrowOp(blockchain.EscrowOp) error         { return errDev }
func (e *engine) SubmitParamVote(blockchain.ParamID, uint64) error { return errDev }
func (e *engine) SubmitExclusionVote(crypto.Address) error         { return errDev }
func (e *engine) IPCTallies() []consensus.IPCTally                 { return nil }
func (e *engine) ParamTallies() []consensus.ParamTally             { return nil }
func (e *engine) Address() crypto.Address                          { return e.me }
func (e *engine) NextNonce(a crypto.Address) uint64 {
	e.c.mu.Lock()
	defer e.c.mu.Unlock()
	return e.pool.NextNonce(e.c.net.State, a)
}

type network struct{}

func (network) PeerCount() (int, int) { return 4, 5 }
func (network) Halted() bool          { return false }

func main() {
	port := flag.Int("port", 3001, "port local")
	static := flag.String("web", "web", "répertoire de l'interface (servie depuis le disque)")
	every := flag.Duration("every", time.Minute, "intervalle entre deux blocs simulés")
	flag.Parse()

	p := chaintest.Params()
	c := &chain{net: chaintest.New(tb{}, p), blocks: map[uint64]blockchain.Block{}}
	pool := mempool.New(p, mempool.DefaultOptions())
	// Historique jusqu'à hier : la tête de chaîne précède l'heure réelle (horodatages du navigateur).
	for chaintest.Day(c.day+1) < time.Now().Unix()-86400 {
		c.next(pool)
	}
	founder := c.net.Founders[0]
	words, _ := wallet.EncodePhrase(founder.Key.Bytes(), wallet.French)
	dir, _ := os.MkdirTemp("", "etl-dev-")
	defer os.RemoveAll(dir)
	srv, err := api.New(api.Config{Params: p, Port: *port, TokenPath: filepath.Join(dir, "dashboard.token"), Version: "dev",
		Static: os.DirFS(*static), Poll: 500 * time.Millisecond, Chain: c,
		Engine: &engine{c: c, pool: pool, me: founder.Addr}, Network: network{}})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Serveur de développement : http://localhost:%d (un bloc toutes les %s)\n", *port, *every)
	fmt.Printf("Fondateur %s — phrase (clé jetable de cette session) :\n  %s\n", founder.Addr, strings.Join(words, " "))

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
				b := c.next(pool)
				log.Printf("bloc %d (%s), %d transaction(s)", b.Header.Height, time.Unix(b.Header.Timestamp, 0).UTC().Format("2006-01-02"), len(b.Txs))
			}
		}
	}()
	if err := srv.ListenAndServe(ctx); err != nil {
		log.Fatal(err)
	}
}
