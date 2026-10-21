package node

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/makoto-kanamoto/etl/internal/api"
	"github.com/makoto-kanamoto/etl/internal/blockchain"
	"github.com/makoto-kanamoto/etl/internal/config"
	"github.com/makoto-kanamoto/etl/internal/consensus"
	"github.com/makoto-kanamoto/etl/internal/crypto"
	"github.com/makoto-kanamoto/etl/internal/mempool"
	"github.com/makoto-kanamoto/etl/internal/network"
	"github.com/makoto-kanamoto/etl/internal/oracle"
	"github.com/makoto-kanamoto/etl/internal/storage"
)

// MaxClockSkew : au-delà, l'opérateur est averti (le consensus exige une heure exacte, NTP).
const MaxClockSkew = 2 * time.Second

// Options d'un nœud.
type Options struct {
	Config  config.Config
	Network Network
	Genesis blockchain.Block
	Key     *crypto.PrivateKey // nil : nœud passif (ni vote, ni proposition, ni oracle)
	Static  fs.FS              // interface du dashboard (web.FS)
	Version string
	Log     *slog.Logger

	// Pour les tests : horloge (nil : time.Now) et source IPC (nil : HTTPS configuré).
	Now          func() time.Time
	OracleSource oracle.Fetcher
}

// Node est un nœud démarré.
type Node struct {
	Store  *storage.Store
	Host   *network.Host
	Engine *consensus.Engine
	API    *api.Server
	Oracle *oracle.Oracle // nil pour un nœud passif

	log *slog.Logger
	wg  sync.WaitGroup
}

// handler relie le réseau au moteur (créé après l'hôte, dont il a besoin pour diffuser).
type handler struct{ e *consensus.Engine }

func (h *handler) Handle(m network.Message) network.Verdict { return h.e.Handle(m) }

// Start ouvre le stockage, démarre réseau, consensus, oracle et dashboard. Stop (ou l'annulation
// de ctx) les arrête proprement.
func Start(ctx context.Context, o Options) (*Node, error) {
	c, p := o.Config, o.Network.Params
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	n := &Node{log: o.Log}
	var err error

	// Stockage : genesis inscrit au premier démarrage, vérifié ensuite.
	n.Store, err = storage.Open(filepath.Join(c.Node.DataDir, "chain.db"), p, storage.Options{Pruning: c.Node.Pruning, KeepBlocks: uint64(c.Node.PruneKeepBlocks)})
	if err != nil {
		return nil, fmt.Errorf("stockage : %w", err)
	}
	if err := n.Store.Init(&o.Genesis); err != nil {
		n.Store.Close()
		return nil, fmt.Errorf("genesis : %w", err)
	}

	// Réseau et consensus.
	pool := mempool.New(p, mempool.Options{MaxPerAccount: c.Mempool.MaxPerAccount, MaxTotal: c.Mempool.MaxTotal,
		MaxAge: time.Duration(c.Mempool.MaxAgeDays) * 24 * time.Hour})
	ncfg := network.DefaultConfig(p, o.Genesis.Header.Hash())
	if c.Node.Port != 0 {
		ncfg.ListenAddr = ":" + strconv.Itoa(c.Node.Port)
	}
	ncfg.Bootstrap = c.Network.Bootstrap
	ncfg.MaxOutbound, ncfg.MaxInbound = c.Network.MaxOutbound, c.Network.MaxInbound
	ncfg.BanDuration = time.Duration(c.Network.BanHours) * time.Hour
	ncfg.Chain, ncfg.Archive, ncfg.Checkpoints, ncfg.Now = n.Store, !c.Node.Pruning, o.Network.Checkpoints, o.Now
	ncfg.OnAlert = func(a network.Alert) {
		o.Log.Error("ARRÊT DU NŒUD : conflit de finalité — intervention humaine requise",
			"hauteur", a.Height, "nôtre", a.Ours.String(), "autre", a.Theirs.String(), "raison", a.Reason, "doubles votes", len(a.Evidence))
	}
	h := &handler{}
	if n.Host, err = network.New(ncfg, h); err != nil {
		n.Store.Close()
		return nil, err
	}
	n.Engine = consensus.New(consensus.Config{Params: p, Key: o.Key, Now: o.Now, Halted: n.Host.Halted}, n.Store, pool, n.Host)
	h.e = n.Engine
	if err := n.Host.Start(); err != nil {
		n.Store.Close()
		return nil, fmt.Errorf("réseau : %w", err)
	}

	// Oracle IPC : nœuds avec clé seulement.
	var orc api.Oracle
	if o.Key != nil {
		if err := oracle.CheckFrequency(c.Oracle.IPCFrequency); err != nil {
			n.stopEarly()
			return nil, err
		}
		src := o.OracleSource
		if src == nil {
			s, err := oracle.NewSource(c.Oracle.IPCURL)
			if err != nil {
				n.stopEarly()
				return nil, err
			}
			src = s
		}
		n.Oracle = oracle.New(oracle.Config{Source: src, Chain: n.Store, Voter: n.Engine,
			Logf: func(f string, a ...any) { o.Log.Info(fmt.Sprintf(f, a...)) }})
		orc = n.Oracle
	}

	// Dashboard (127.0.0.1 seulement).
	n.API, err = api.New(api.Config{Params: p, Port: c.Dashboard.Port, TokenPath: c.Dashboard.Token, Static: o.Static,
		Version: o.Version, Now: o.Now, Chain: n.Store, Engine: n.Engine, Network: n.Host, Oracle: orc})
	if err != nil {
		n.stopEarly()
		return nil, err
	}

	n.run(ctx, func(ctx context.Context) { n.Engine.Run(ctx) })
	if n.Oracle != nil {
		n.run(ctx, func(ctx context.Context) { n.Oracle.Run(ctx) })
	}
	n.run(ctx, func(ctx context.Context) {
		if err := n.API.ListenAndServe(ctx); err != nil {
			o.Log.Error("dashboard indisponible", "erreur", err)
		}
	})
	n.run(ctx, func(ctx context.Context) { n.watchClock(ctx) })
	addr := ""
	if o.Key != nil {
		addr = o.Key.PublicKey().Address(p.AddressHRP).String()
	}
	o.Log.Info("nœud démarré", "réseau", p.ChainID, "hauteur", n.Store.Tip().Height, "adresse", addr,
		"passif", o.Key == nil, "archive", !c.Node.Pruning, "dashboard", fmt.Sprintf("http://localhost:%d", c.Dashboard.Port))
	return n, nil
}

func (n *Node) run(ctx context.Context, f func(context.Context)) {
	n.wg.Add(1)
	go func() {
		defer n.wg.Done()
		f(ctx)
	}()
}

func (n *Node) stopEarly() {
	n.Host.Close()
	n.Store.Close()
}

// Wait attend la fin des tâches (après l'annulation du contexte) puis ferme réseau et stockage.
func (n *Node) Wait() {
	n.wg.Wait()
	n.Host.Close()
	n.Store.Close()
	n.log.Info("nœud arrêté")
}

// watchClock avertit si l'heure locale s'écarte de celle des pairs (M6) : 30 s après le démarrage,
// puis toutes les 10 minutes.
func (n *Node) watchClock(ctx context.Context) {
	wait := 30 * time.Second
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = 10 * time.Minute
		if off, peers := n.Host.ClockOffset(); peers >= 2 && (off > MaxClockSkew || off < -MaxClockSkew) {
			n.log.Warn("horloge décalée par rapport aux pairs : synchronisez l'heure du système (NTP), le consensus l'exige",
				"écart", off.Round(time.Millisecond).String(), "pairs", peers)
		}
	}
}
