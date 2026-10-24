package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/makoto-kanamoto/etl/internal/blockchain"
	"github.com/makoto-kanamoto/etl/internal/config"
	"github.com/makoto-kanamoto/etl/internal/crypto"
	"github.com/makoto-kanamoto/etl/internal/oracle"
	"github.com/makoto-kanamoto/etl/internal/storage"
)

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// testGenesis : réseau de test, genesis le 20/10/2026, n fondateurs.
func testGenesis(t *testing.T, n int) (blockchain.Block, []*crypto.PrivateKey, GenesisFile) {
	t.Helper()
	net, _ := Lookup("test")
	return testGenesisOn(t, n, net)
}

// testGenesisOn : genesis du réseau net, le 20/10/2026, n fondateurs.
func testGenesisOn(t *testing.T, n int, net Network) (blockchain.Block, []*crypto.PrivateKey, GenesisFile) {
	t.Helper()
	var keys []*crypto.PrivateKey
	var addrs []string
	for i := 0; i < n; i++ {
		k, _ := crypto.GenerateKey()
		keys = append(keys, k)
		addrs = append(addrs, k.PublicKey().Address(net.Params.AddressHRP).String())
	}
	g, _, err := NewGenesisFile(net, time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC),
		blockchain.IPCRecord{Value: 103070, Period: "2026-Q3", SourceSHA256: [32]byte{0xed}}, addrs)
	if err != nil {
		t.Fatal(err)
	}
	b, err := g.Build(net)
	if err != nil {
		t.Fatal(err)
	}
	return b, keys, g
}

func TestGenesisFile(t *testing.T) {
	dir := t.TempDir()
	b, _, g := testGenesis(t, 4)
	path := filepath.Join(dir, "genesis.json")
	data, _ := json.MarshalIndent(g, "", "  ")
	_ = os.WriteFile(path, data, 0o644)
	test, _ := Lookup("test")
	got, err := LoadGenesis(path, test)
	if err != nil || got.Header.Hash() != b.Header.Hash() {
		t.Fatalf("réseau de test : %v", err)
	}
	if b.Header.Timestamp != time.Date(2026, 10, 20, 23, 0, 0, 0, time.UTC).Unix() {
		t.Fatal("genesis : 23:00 UTC du jour donné")
	}
	// ETL-FR n'est pas lancé : refusé, même avec un fichier valide pour lui.
	fr, _ := Lookup("ETL-FR")
	if _, err := LoadGenesis(path, fr); !errors.Is(err, ErrNotLaunched) {
		t.Fatalf("ETL-FR avant lancement : %v", err)
	}
	// Réseau « lancé » (empreinte gravée) : seul le fichier exact est accepté.
	pinned := test
	pinned.Test, pinned.GenesisHash = false, b.Header.Hash()
	if _, err := LoadGenesis(path, pinned); err != nil {
		t.Fatalf("empreinte exacte : %v", err)
	}
	pinned.GenesisHash[0] ^= 1
	if _, err := LoadGenesis(path, pinned); !errors.Is(err, ErrGenesisMismatch) {
		t.Fatalf("empreinte différente : %v", err)
	}
	bad := g
	bad.ChainID = "ETL-FR"
	if _, err := bad.Build(test); err == nil {
		t.Fatal("genesis d'un autre réseau accepté")
	}
	bad = g
	bad.Founders = append([]string{"etl1qinvalide"}, g.Founders[1:]...)
	if _, err := bad.Build(test); err == nil {
		t.Fatal("fondateur invalide accepté")
	}
	bad = g
	bad.Founders = g.Founders[:3]
	if _, err := bad.Build(test); err == nil {
		t.Fatal("moins de fondateurs que le minimum")
	}
	bad = g
	bad.IPC.SourceSHA256 = "zz"
	if _, err := bad.Build(test); err == nil {
		t.Fatal("empreinte IPC invalide acceptée")
	}
	_ = os.WriteFile(path, []byte("{"), 0o644)
	if _, err := LoadGenesis(path, test); err == nil {
		t.Fatal("fichier illisible accepté")
	}
	if _, err := LoadGenesis(filepath.Join(dir, "absent.json"), test); err == nil {
		t.Fatal("fichier absent accepté")
	}
	if _, err := Lookup(""); !errors.Is(err, ErrUnknownNetwork) {
		t.Fatal("réseau vide accepté")
	}
	if _, err := Lookup("ETL-XX"); !errors.Is(err, ErrUnknownNetwork) {
		t.Fatal("réseau inconnu accepté")
	}
	if p := TestParams(); p.ChainID == "ETL-FR" || p.AddressHRP == "etl" {
		t.Fatal("le réseau de test doit être distinct d'ETL-FR")
	}
}

func testConfig(t *testing.T) config.Config {
	t.Helper()
	dir := t.TempDir()
	c := config.Default(dir)
	c.Node.Network = "test"
	c.Node.Port = freePort(t)
	c.Network.Bootstrap = nil
	c.Dashboard.Port = freePort(t)
	return c
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// Démarrage réel d'un nœud passif : stockage, API, arrêt propre, redémarrage sur la même chaîne.
func TestPassiveNodeStartStop(t *testing.T) {
	g, _, _ := testGenesis(t, 4)
	c := testConfig(t)
	test, _ := Lookup("test")
	ctx, cancel := context.WithCancel(context.Background())
	n, err := Start(ctx, Options{Config: c, Network: test, Genesis: g, Log: quiet(), Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	var st map[string]any
	for i := 0; i < 50; i++ {
		req, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/api/status", c.Dashboard.Port), nil)
		req.Host = fmt.Sprintf("localhost:%d", c.Dashboard.Port)
		if resp, err := http.DefaultClient.Do(req); err == nil {
			_ = json.NewDecoder(resp.Body).Decode(&st)
			resp.Body.Close()
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if st["chain_id"] != "ETL-TEST" || st["height"].(float64) != 0 || st["node"].(map[string]any)["address"] != "" {
		t.Fatalf("statut : %v", st)
	}
	if n.Oracle != nil {
		t.Fatal("oracle démarré sur un nœud passif")
	}
	cancel()
	done := make(chan struct{})
	go func() { n.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("arrêt non effectué")
	}
	// Redémarrage : même chaîne acceptée ; autre genesis refusé.
	ctx2, cancel2 := context.WithCancel(context.Background())
	n2, err := Start(ctx2, Options{Config: c, Network: test, Genesis: g, Log: quiet()})
	if err != nil {
		t.Fatalf("redémarrage : %v", err)
	}
	cancel2()
	n2.Wait()
	other, _, _ := testGenesis(t, 4)
	if _, err := Start(context.Background(), Options{Config: c, Network: test, Genesis: other, Log: quiet()}); !errors.Is(err, storage.ErrWrongNetwork) {
		t.Fatalf("autre genesis sur la même base : %v", err)
	}
	// Fréquence IPC non trimestrielle : refusée au démarrage d'un nœud avec clé.
	k, _ := crypto.GenerateKey()
	c.Oracle.IPCFrequency = "monthly"
	if _, err := Start(context.Background(), Options{Config: c, Network: test, Genesis: g, Key: k, Log: quiet()}); !errors.Is(err, oracle.ErrFrequency) {
		t.Fatalf("fréquence mensuelle : %v", err)
	}
}

// clock : horloge virtuelle partagée, qui avance 30 fois plus vite que le temps réel et peut sauter
// à la veille de 23:00 du jour suivant (tests seulement).
type clock struct {
	mu        sync.Mutex
	base      time.Time
	realStart time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.base.Add(time.Since(c.realStart) * 30)
}

func (c *clock) jump(to time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.base, c.realStart = to, time.Now()
}

type noSource struct{}

func (noSource) Fetch(context.Context) ([]oracle.Observation, error) {
	return nil, errors.New("pas de source dans les tests")
}

// Quatre nœuds réels (TCP + TLS, bbolt) sur localhost finalisent des blocs ; un cinquième, passif et
// démarré après coup, rattrape la chaîne par synchronisation.
func TestFourNodesFinalizeBlocks(t *testing.T) {
	if testing.Short() {
		t.Skip("réseau réel : plusieurs secondes")
	}
	// Horloge 30 fois plus rapide : la fenêtre de participation de 30 s ne dure qu'une seconde réelle.
	// Preuve de travail allégée (8 bits au lieu de 16) pour que les 4 nœuds la minent dans cette seconde
	// même sous le détecteur de courses (16 bits : jusqu'à 0,8 s par nœud avec -race). Ce test vérifie
	// le réseau, le consensus et la synchronisation, pas la difficulté.
	test, _ := Lookup("test")
	test.Params.TargetBits, test.Params.Target = 8, blockchain.TargetFromBits(8)
	g, keys, _ := testGenesisOn(t, 4, test)
	clk := &clock{}
	genesisDay := time.Unix(g.Header.Timestamp, 0).UTC()
	clk.jump(genesisDay.Add(24*time.Hour - 3*time.Second))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfgs := make([]config.Config, 4)
	for i := range cfgs {
		cfgs[i] = testConfig(t)
	}
	var nodes []*Node
	for i := range cfgs {
		for j := range cfgs {
			if j != i {
				cfgs[i].Network.Bootstrap = append(cfgs[i].Network.Bootstrap, fmt.Sprintf("127.0.0.1:%d", cfgs[j].Node.Port))
			}
		}
		n, err := Start(ctx, Options{Config: cfgs[i], Network: test, Genesis: g, Key: keys[i], Now: clk.Now, OracleSource: noSource{}, Log: quiet()})
		if err != nil {
			t.Fatal(err)
		}
		nodes = append(nodes, n)
	}
	waitHeight := func(ns []*Node, h uint64, within time.Duration) {
		t.Helper()
		end := time.Now().Add(within)
		for time.Now().Before(end) {
			ok := true
			for _, n := range ns {
				ok = ok && n.Store.Tip().Height >= h
			}
			if ok {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		var hs []uint64
		for _, n := range ns {
			hs = append(hs, n.Store.Tip().Height)
		}
		t.Fatalf("hauteur %d non atteinte : %v", h, hs)
	}
	for d := 1; d <= 3; d++ {
		if d > 1 {
			clk.jump(genesisDay.Add(time.Duration(d)*24*time.Hour - 3*time.Second))
		}
		waitHeight(nodes, uint64(d), 60*time.Second)
	}
	tipOf := func(n *Node) crypto.Hash { h := n.Store.Tip(); return h.Hash() }
	tip := tipOf(nodes[0])
	for i, n := range nodes {
		if tipOf(n) != tip {
			t.Fatalf("nœud %d : tête différente", i)
		}
	}
	// Récompenses : chaque fondateur touche exactement sa part des blocs où figure sa participation.
	want := map[crypto.Address]uint64{}
	for h := uint64(1); h <= 3; h++ {
		b, err := nodes[0].Store.BlockAt(h)
		if err != nil || len(b.Participants) < test.Params.MinActiveNodes {
			t.Fatalf("bloc %d : %v, %d participants", h, err, len(b.Participants))
		}
		for _, pt := range b.Participants {
			want[pt.Miner] += blockchain.RewardShare(h, len(b.Participants), test.Params)
		}
	}
	for i, k := range keys {
		a := k.PublicKey().Address("etlt")
		if got := nodes[0].Store.Account(a).Balance; got != want[a] {
			t.Fatalf("récompenses du fondateur %d : %d, attendu %d", i, got, want[a])
		}
	}
	// Part enregistrée par le stockage (table rewards) : identique sur chaque nœud, et son total
	// sur les participants égale les ETL en circulation.
	var issued uint64
	for h := uint64(1); h <= 3; h++ {
		share, ok := nodes[0].Store.RewardAt(h)
		blk, _ := nodes[0].Store.BlockAt(h)
		if !ok || share != blockchain.RewardShare(h, len(blk.Participants), test.Params) {
			t.Fatalf("part du bloc %d : %d %v", h, share, ok)
		}
		for _, n := range nodes[1:] {
			if other, _ := n.Store.RewardAt(h); other != share {
				t.Fatalf("part du bloc %d différente selon le nœud", h)
			}
		}
		issued += share * uint64(len(blk.Participants))
	}
	var supply uint64
	nodes[0].Store.ReadState(func(st *blockchain.State) { supply = st.Supply() })
	if supply != issued {
		t.Fatalf("circulation %d, parts × participants %d", supply, issued)
	}
	// Cinquième nœud, passif, démarré après coup : il rattrape la chaîne.
	late := testConfig(t)
	late.Network.Bootstrap = []string{fmt.Sprintf("127.0.0.1:%d", cfgs[0].Node.Port)}
	ln, err := Start(ctx, Options{Config: late, Network: test, Genesis: g, Now: clk.Now, Log: quiet()})
	if err != nil {
		t.Fatal(err)
	}
	waitHeight([]*Node{ln}, 3, 60*time.Second)
	if tipOf(ln) != tip {
		t.Fatal("nœud tardif : tête différente")
	}
	cancel()
	for _, n := range append(nodes, ln) {
		n.Wait()
	}
}

// statusVia lit /api/status sur le port donné, avec l'en-tête Host donné.
func statusVia(t *testing.T, port int, host string) map[string]any {
	t.Helper()
	var st map[string]any
	for i := 0; i < 50; i++ {
		req, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/api/status", port), nil)
		req.Host = host
		if resp, err := http.DefaultClient.Do(req); err == nil {
			_ = json.NewDecoder(resp.Body).Decode(&st)
			resp.Body.Close()
			return st
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("serveur injoignable")
	return nil
}

// Site public : nœud archive passif, index de l'explorateur, aucun jeton ; avant le lancement,
// pages de présentation seules.
func TestPublicSite(t *testing.T) {
	g, _, _ := testGenesis(t, 4)
	c := testConfig(t)
	c.Node.Wallet, c.Node.Pruning = "", false
	c.Public.Enabled, c.Public.Host = true, "etalon.test"
	test, _ := Lookup("test")
	ctx, cancel := context.WithCancel(context.Background())
	n, err := Start(ctx, Options{Config: c, Network: test, Genesis: g, Log: quiet(), Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if st := statusVia(t, c.Dashboard.Port, "etalon.test"); st["public"] != true || st["launched"] != true || st["chain_id"] != "ETL-TEST" {
		t.Fatalf("statut : %v", st)
	}
	if st := statusVia(t, c.Dashboard.Port, fmt.Sprintf("localhost:%d", c.Dashboard.Port)); st["chain_id"] != nil {
		t.Fatalf("hôte local accepté : %v", st)
	}
	if _, err := os.Stat(c.Dashboard.Token); err == nil {
		t.Fatal("jeton opérateur écrit")
	}
	if _, _, err := n.Store.AddressHeights(crypto.Address{}, 1, 1); err != nil {
		t.Fatalf("index absent : %v", err)
	}
	cancel()
	n.Wait()

	// Réseau pas encore lancé : site de présentation, rendu à l'annulation.
	fr, _ := Lookup("ETL-FR")
	ctx2, cancel2 := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ServePrelaunch(ctx2, c, fr, nil, "test", quiet()) }()
	if st := statusVia(t, c.Dashboard.Port, "etalon.test"); st["launched"] != false || st["chain_id"] != "ETL-FR" {
		t.Fatalf("avant le lancement : %v", st)
	}
	cancel2()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("arrêt non effectué")
	}
}
