package api

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/makoto-kanamoto/etl/internal/blockchain"
	"github.com/makoto-kanamoto/etl/internal/blockchain/chaintest"
	"github.com/makoto-kanamoto/etl/internal/crypto"
	"github.com/makoto-kanamoto/etl/internal/escrow"
	"github.com/makoto-kanamoto/etl/internal/storage"
)

const site = "etalon.test"

func publicCfg(read, write int) func(*Config) {
	return func(c *Config) { c.Public = &Public{Host: site, ReadPerMinute: read, WritePerMinute: write} }
}

// call envoie une requête au site public depuis remote (adresse de la connexion), avec en-têtes.
func (b *bench) call(method, path, body, remote string, hdr map[string]string) (*httptest.ResponseRecorder, map[string]any) {
	b.t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Host = site
	r.RemoteAddr = remote
	if method == http.MethodPost {
		r.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	b.s.ServeHTTP(w, r)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w, out
}

const visitor = "198.51.100.7:40000"

// Site public : aucun jeton écrit, aucune route de l'opérateur ni flux SSE, hôte et origine du
// site seulement, HSTS partout ; transferts et échanges acceptés.
func TestPublicMode(t *testing.T) {
	b := newBench(t, publicCfg(1000, 1000))
	if _, err := os.Stat(filepath.Join(b.dir, "etl", "dashboard.token")); err == nil || b.token != "" {
		t.Fatal("jeton opérateur écrit par le site public")
	}
	w, st := b.call("GET", "/api/status", "", visitor, nil)
	if w.Code != 200 || st["public"] != true || st["launched"] != true || st["read_only"] != false {
		t.Fatalf("statut : %d %v", w.Code, st)
	}
	if got := w.Header().Get("Strict-Transport-Security"); got != "max-age=63072000" {
		t.Fatalf("HSTS : %q", got)
	}
	if w, _ := b.call("GET", "/", "", visitor, nil); w.Code != 200 || w.Header().Get("Strict-Transport-Security") == "" ||
		w.Header().Get("Content-Security-Policy") == "" {
		t.Fatalf("page : %d %v", w.Code, w.Header())
	}
	for _, host := range []string{"localhost:3001", "127.0.0.1:3001", "etalon.test:443", "www.etalon.test", "autre.test"} {
		code, _, _ := b.do(req{path: "/api/status", host: host})
		if code != http.StatusForbidden {
			t.Errorf("hôte %s : %d", host, code)
		}
	}
	ops := []string{"param-vote", "exclusion-vote", "ipc-read", "escrow/cosign", "escrow/sign", "escrow/act",
		"registration/request", "registration/sponsor", "registration/reactivate"}
	for _, op := range ops {
		w, _ := b.call("POST", "/api/operator/"+op, `{}`, visitor, map[string]string{"Authorization": "Bearer x"})
		if w.Code != http.StatusNotFound {
			t.Errorf("route opérateur %s : %d", op, w.Code)
		}
	}
	if w, _ := b.call("GET", "/api/events", "", visitor, nil); w.Code != http.StatusNotFound {
		t.Fatalf("flux SSE : %d", w.Code)
	}

	tx := b.validTx()
	body := `{"tx":"` + hex.EncodeToString(blockchain.EncodeTransaction(&tx)) + `"}`
	for origin, want := range map[string]int{"https://" + site: 200, "": 200, "http://" + site: 403,
		"http://localhost:3001": 403, "https://autre.test": 403} {
		h := map[string]string{}
		if origin != "" {
			h["Origin"] = origin
		}
		if w, out := b.call("POST", "/api/tx", body, visitor, h); w.Code != want {
			t.Errorf("origine %q : %d %v", origin, w.Code, out)
		}
	}
	f := b.c.net.Founders
	terms, _ := escrow.NewTerms(f[0].Addr, f[3].Addr, crypto.Address{}, 1000, 0, 3, chaintest.Day(1), b.c.net.P)
	op, _ := escrow.Open(f[0].Key, terms, b.c.net.P)
	if w, out := b.call("POST", "/api/escrow", `{"op":"`+escrow.EncodeText(&op)+`"}`, visitor,
		map[string]string{"Origin": "https://" + site}); w.Code != 200 || len(b.e.ops) != 1 {
		t.Fatalf("échange : %d %v", w.Code, out)
	}
	// Configuration incomplète refusée.
	for _, p := range []Public{{ReadPerMinute: 1, WritePerMinute: 1}, {Host: site, WritePerMinute: 1}, {Host: site, ReadPerMinute: 1}} {
		if _, err := New(Config{Params: b.c.net.P, Port: port, Chain: b.c, Public: &p}); err == nil {
			t.Errorf("site public accepté : %+v", p)
		}
	}
}

// Seau à jetons : perMinute requêtes d'affilée, puis une toutes les 60/perMinute secondes.
func TestLimiter(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	l := newLimiter(3, func() time.Time { return now })
	for i := 0; i < 3; i++ {
		if ok, _ := l.allow("a"); !ok {
			t.Fatalf("requête %d refusée", i)
		}
	}
	ok, wait := l.allow("a")
	if ok || wait != 20*time.Second {
		t.Fatalf("4e requête : %v, attente %v", ok, wait)
	}
	if ok, _ := l.allow("b"); !ok {
		t.Fatal("autre client limité")
	}
	now = now.Add(19 * time.Second)
	if ok, wait := l.allow("a"); ok || wait != time.Second {
		t.Fatalf("après 19 s : %v %v", ok, wait)
	}
	now = now.Add(time.Second)
	if ok, _ := l.allow("a"); !ok {
		t.Fatal("après 20 s : jeton non rendu")
	}
	if ok, _ := l.allow("a"); ok {
		t.Fatal("deux jetons rendus en 20 s")
	}
	// Une heure plus tard : seau plein (plafonné), clients inactifs oubliés.
	now = now.Add(time.Hour)
	for i := 0; i < 3; i++ {
		if ok, _ := l.allow("a"); !ok {
			t.Fatalf("seau non rempli : requête %d", i)
		}
	}
	if ok, _ := l.allow("a"); ok {
		t.Fatal("seau rempli au-delà de sa capacité")
	}
	if _, kept := l.buckets["b"]; kept {
		t.Fatal("client inactif conservé")
	}
	// Entre deux balayages aussi, le seau ne dépasse pas sa capacité : 2 jetons + 50 s → 3, pas 4,5.
	l.allow("c")
	now = now.Add(50 * time.Second)
	for i := 0; i < 3; i++ {
		if ok, _ := l.allow("c"); !ok {
			t.Fatalf("seau c : requête %d refusée", i)
		}
	}
	if ok, _ := l.allow("c"); ok {
		t.Fatal("seau rempli au-delà de sa capacité entre deux balayages")
	}
	// Mémoire bornée : au-delà de maxClients, les nouveaux clients partagent un seau.
	for i := len(l.buckets); i < maxClients; i++ {
		l.buckets[fmt.Sprint("x", i)] = &bucket{at: now}
	}
	for i := 0; i < 3; i++ {
		if ok, _ := l.allow(fmt.Sprint("nouveau", i)); !ok {
			t.Fatalf("seau commun : requête %d refusée", i)
		}
	}
	if ok, _ := l.allow("encore un autre"); ok || len(l.buckets) != maxClients+1 {
		t.Fatalf("seau commun non partagé : %d seaux", len(l.buckets))
	}
}

func TestClientIP(t *testing.T) {
	for _, c := range []struct {
		remote string
		xff    []string
		want   string
	}{
		{"203.0.113.9:5000", nil, "203.0.113.9"},
		{"203.0.113.9:5000", []string{"1.1.1.1"}, "203.0.113.9"}, // en-tête ignoré hors boucle locale
		{"127.0.0.1:5000", nil, "127.0.0.1"},
		{"127.0.0.1:5000", []string{"1.1.1.1"}, "1.1.1.1"},
		{"127.0.0.1:5000", []string{"9.9.9.9, 1.1.1.1"}, "1.1.1.1"}, // dernière valeur : celle du proxy
		{"127.0.0.1:5000", []string{"9.9.9.9", "2.2.2.2"}, "2.2.2.2"},
		{"[::1]:5000", []string{"2001:db8:1:2:3:4:5:6"}, "2001:db8:1:2::"},
		{"[2001:db8:1:2:aaaa::1]:5000", nil, "2001:db8:1:2::"},
		{"127.0.0.1:5000", []string{"pas une ip"}, "127.0.0.1"},
		{"::ffff:198.51.100.1", nil, "198.51.100.1"},
		{"n'importe quoi", nil, "?"},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = c.remote
		for _, v := range c.xff {
			r.Header.Add("X-Forwarded-For", v)
		}
		if got := clientIP(r); got != c.want {
			t.Errorf("%s %v : %s, attendu %s", c.remote, c.xff, got, c.want)
		}
	}
}

// Limitation par client, lectures et écritures séparées ; l'interface n'est pas limitée.
func TestPublicRateLimit(t *testing.T) {
	b := newBench(t, publicCfg(2, 1))
	proxy := "127.0.0.1:6000"
	from := func(ip string) map[string]string { return map[string]string{"X-Forwarded-For": ip} }
	for i := 0; i < 2; i++ {
		if w, _ := b.call("GET", "/api/status", "", proxy, from("1.1.1.1")); w.Code != 200 {
			t.Fatalf("lecture %d : %d", i, w.Code)
		}
	}
	w, out := b.call("GET", "/api/params", "", proxy, from("1.1.1.1"))
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "30" || !strings.Contains(fmt.Sprint(out["error"]), "30 s") {
		t.Fatalf("3e lecture : %d %v %v", w.Code, w.Header(), out)
	}
	if w, _ := b.call("GET", "/api/status", "", proxy, from("2.2.2.2")); w.Code != 200 {
		t.Fatal("autre visiteur limité")
	}
	for i := 0; i < 5; i++ {
		if w, _ := b.call("GET", []string{"/", "/wallet/keys.js", "/dashboard/fonts/f.woff2"}[i%3], "", proxy, from("1.1.1.1")); w.Code != 200 {
			t.Fatalf("interface limitée : %d", w.Code)
		}
	}
	tx := b.validTx()
	body := `{"tx":"` + hex.EncodeToString(blockchain.EncodeTransaction(&tx)) + `"}`
	if w, _ := b.call("POST", "/api/tx", body, proxy, from("1.1.1.1")); w.Code != 200 {
		t.Fatalf("écriture après lectures épuisées : %d", w.Code)
	}
	if w, _ := b.call("POST", "/api/tx", body, proxy, from("1.1.1.1")); w.Code != http.StatusTooManyRequests {
		t.Fatalf("2e écriture : %d", w.Code)
	}
	// Hors boucle locale, X-Forwarded-For ne permet pas de changer de seau.
	for i, ip := range []string{"3.3.3.3", "4.4.4.4"} {
		if w, _ := b.call("GET", "/api/status", "", visitor, from(ip)); w.Code != 200 {
			t.Fatalf("lecture directe %d : %d", i, w.Code)
		}
	}
	if w, _ := b.call("GET", "/api/status", "", visitor, from("5.5.5.5")); w.Code != http.StatusTooManyRequests {
		t.Fatalf("en-tête falsifié accepté : %d", w.Code)
	}
	// Attente arrondie à la seconde supérieure : 7 par minute, un jeton toutes les 8,57 s.
	seven := newBench(t, publicCfg(7, 1))
	for i := 0; i < 7; i++ {
		seven.call("GET", "/api/params", "", visitor, nil)
	}
	if w, _ := seven.call("GET", "/api/params", "", visitor, nil); w.Header().Get("Retry-After") != "9" {
		t.Fatalf("Retry-After : %q", w.Header().Get("Retry-After"))
	}
	// Le dashboard de l'opérateur n'est pas limité.
	op := newBench(t, nil)
	for i := 0; i < 20; i++ {
		op.get("/api/params")
	}
}

// Réseau pas encore lancé : pages de présentation et statut minimal, rien d'autre.
func TestPrelaunch(t *testing.T) {
	static := fstest.MapFS{"dashboard/index.html": {Data: []byte("<!doctype html>")}}
	s, err := New(Config{Params: chaintest.Params(), Port: port, Static: static, Version: "v",
		Public: &Public{Host: site, ReadPerMinute: 100, WritePerMinute: 1}})
	if err != nil {
		t.Fatal(err)
	}
	b := &bench{t: t, s: s}
	w, st := b.call("GET", "/api/status", "", visitor, nil)
	if w.Code != 200 || st["launched"] != false || st["public"] != true || st["chain_id"] != chaintest.Params().ChainID || len(st) != 5 {
		t.Fatalf("statut : %d %v", w.Code, st)
	}
	if w.Header().Get("Strict-Transport-Security") == "" {
		t.Fatal("HSTS absent")
	}
	if w, _ := b.call("GET", "/", "", visitor, nil); w.Code != 200 {
		t.Fatalf("page : %d", w.Code)
	}
	for _, p := range []string{"/api/blocks", "/api/params", "/api/ipc", "/api/search?q=1", "/api/events"} {
		if w, _ := b.call("GET", p, "", visitor, nil); w.Code != http.StatusNotFound {
			t.Errorf("%s : %d", p, w.Code)
		}
	}
	if w, _ := b.call("POST", "/api/tx", `{}`, visitor, nil); w.Code != http.StatusNotFound {
		t.Fatalf("soumission : %d", w.Code)
	}
	if _, err := New(Config{Params: chaintest.Params(), Port: port, TokenPath: filepath.Join(t.TempDir(), "t")}); err == nil {
		t.Fatal("dashboard sans chaîne accepté")
	}
}

// --- explorateur ---

// mixed alterne minuscules et majuscules (adresse ou empreinte recopiée à la main).
func mixed(s string) string {
	r := []rune(s)
	for i := range r {
		if i%2 == 1 {
			r[i] = []rune(strings.ToUpper(string(r[i])))[0]
		}
	}
	return string(r)
}

// newExplorer : banc dont la chaîne et l'index sont un vrai stockage archive indexé.
func newExplorer(t *testing.T, mod func(*Config)) *bench {
	t.Helper()
	var st *storage.Store
	b := newBench(t, func(c *Config) {
		chain := c.Chain.(*chain)
		var err error
		st, err = storage.Open(filepath.Join(t.TempDir(), "chain.db"), chain.net.P, storage.Options{Index: true})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { st.Close() })
		if err := st.Init(&chain.net.Genesis); err != nil {
			t.Fatal(err)
		}
		b1, c1 := chain.blocks[1], chain.certs[1]
		if err := st.Append(&b1, &c1); err != nil {
			t.Fatal(err)
		}
		chain.store = st
		c.Chain, c.Index = st, st
		if mod != nil {
			mod(c)
		}
	})
	return b
}

func TestExplorerTxAndSearch(t *testing.T) {
	b := newExplorer(t, nil)
	f := b.c.net.Founders
	tx := b.validTx()
	b.c.next(t, 2, func(blk *blockchain.Block) { blk.Txs = []blockchain.Transaction{tx} })
	terms, _ := escrow.NewTerms(f[0].Addr, f[3].Addr, crypto.Address{}, 1000, 0, 3, chaintest.Day(2), b.c.net.P)
	op, _ := escrow.Open(f[0].Key, terms, b.c.net.P)
	b3 := b.c.next(t, 3, func(blk *blockchain.Block) { blk.EscrowOps = []blockchain.EscrowOp{op} })

	got := b.get("/api/tx/" + hexOf(tx.ID))
	if got["tx"].(map[string]any)["height"].(float64) != 2 || got["tx"].(map[string]any)["id"] != hexOf(tx.ID) ||
		got["block_time"].(float64) != float64(b.c.blocks[2].Header.Timestamp) {
		t.Fatalf("transaction : %v", got)
	}
	for path, want := range map[string]int{"/api/tx/" + hexOf(crypto.Hash{9}): 404, "/api/tx/zz": 400, "/api/tx/" + hexOf(tx.ID)[2:]: 400} {
		if code, _, _ := b.do(req{path: path}); code != want {
			t.Errorf("%s : %d", path, code)
		}
	}
	search := func(q string) (int, map[string]any) {
		code, out, _ := b.do(req{path: "/api/search?q=" + url.QueryEscape(q)})
		return code, out
	}
	for q, want := range map[string]string{
		"2":                                  "block 2",
		" 0 ":                                "block 0",
		f[1].Addr.String():                   "address " + f[1].Addr.String(),
		strings.ToUpper(f[1].Addr.String()):  "address " + f[1].Addr.String(),
		hexOf(b3.Header.Hash()):              "block 3",
		hexOf(b.c.net.Genesis.Header.Hash()): "block 0",
		hexOf(tx.ID):                         "tx 2",
		strings.ToUpper(hexOf(tx.ID)):        "tx 2",
		mixed(hexOf(tx.ID)):                  "tx 2",
		mixed(f[1].Addr.String()):            "address " + f[1].Addr.String(),
		hexOf(terms.ID()):                    "escrow " + hexOf(terms.ID()),
	} {
		code, out := search(q)
		var desc string
		switch out["type"] {
		case "block", "tx":
			desc = fmt.Sprint(out["type"], " ", out["height"])
		case "address":
			desc = fmt.Sprint(out["type"], " ", out["address"])
		case "escrow":
			desc = fmt.Sprint(out["type"], " ", out["id"])
		}
		if code != 200 || desc != want {
			t.Errorf("recherche %q : %d %v, attendu %s", q, code, out, want)
		}
	}
	for q, want := range map[string]int{"": 400, strings.Repeat("a", 129): 400, "4": 404, "etlde" + f[1].Addr.String()[3:]: 404,
		hexOf(crypto.Hash{7}): 404, "bonjour": 404, hexOf(tx.ID)[1:]: 404} {
		if code, out := search(q); code != want {
			t.Errorf("recherche %q : %d %v", q, code, out)
		}
	}
	// Sans résultat : motif donné à l'interface.
	other := f[1].Key.PublicKey().Address("etlt").String()
	for q, want := range map[string]string{
		"4":                               "future_block tip=3",
		other:                             "other_network hrp=etlt",
		strings.ToUpper(other):            "other_network hrp=etlt",
		"3f7a91c2e8":                      "short_hash length=10",
		hexOf(tx.ID)[1:]:                  "short_hash length=63",
		hexOf(crypto.Hash{7}):             "unknown_hash",
		"bonjour":                         "unrecognized",
		hexOf(tx.ID) + "00":               "unrecognized",
		"etl1" + f[1].Addr.String()[4:30]: "unrecognized",
	} {
		code, out := search(q)
		got := fmt.Sprint(out["reason"])
		for _, k := range []string{"tip", "hrp", "length"} {
			if v, ok := out[k]; ok {
				got += fmt.Sprintf(" %s=%v", k, v)
			}
		}
		if code != 404 || got != want {
			t.Errorf("recherche %q : %d %v, attendu %s", q, code, out, want)
		}
	}
	// Échange en cours : étapes retrouvées par l'index.
	e := b.get("/api/escrows/" + hexOf(terms.ID()))
	if h := e["history"].([]any); len(h) != 1 || fmt.Sprintf("%v %v", h[0].(map[string]any)["kind"], h[0].(map[string]any)["height"]) != "open 3" || e["archived"] != nil {
		t.Fatalf("échange en cours : %v", e)
	}
	// Sans index : pas de recherche de transaction, le reste fonctionne.
	plain := newBench(t, nil)
	if code, _, _ := plain.do(req{path: "/api/tx/" + hexOf(tx.ID)}); code != 404 {
		t.Fatalf("transaction sans index : %d", code)
	}
	if code, out, _ := plain.do(req{path: "/api/search?q=1"}); code != 200 || out["type"] != "block" {
		t.Fatalf("recherche sans index : %d %v", code, out)
	}
}

func TestBlocksPaginationAndDetail(t *testing.T) {
	b := newBench(t, nil)
	for d := 2; d <= 5; d++ {
		b.c.next(t, d, nil)
	}
	list := func(q string) (int, []uint64) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/api/blocks"+q, nil)
		r.Host = "localhost:3001"
		b.s.ServeHTTP(w, r)
		var hs []map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &hs)
		var out []uint64
		for _, h := range hs {
			out = append(out, uint64(h["height"].(float64)))
		}
		return w.Code, out
	}
	for q, want := range map[string]string{"?limit=2": "[5 4]", "?before=4&limit=2": "[3 2]", "?before=2&limit=5": "[1 0]",
		"?before=1": "[0]", "?before=0": "[]", "?before=99&limit=1": "[5]", "?before=18446744073709551615&limit=1": "[5]"} {
		code, got := list(q)
		if code != 200 || fmt.Sprint(got) != want && !(want == "[]" && got == nil) {
			t.Errorf("%s : %d %v, attendu %s", q, code, got, want)
		}
	}
	for _, q := range []string{"?before=x", "?before=-1", "?before=1.5"} {
		if code, _ := list(q); code != 400 {
			t.Errorf("%s : %d", q, code)
		}
	}
	blk := b.get("/api/blocks/3")
	miners := blk["participant_addresses"].([]any)
	if len(miners) != len(b.c.blocks[3].Participants) || miners[0] != b.c.blocks[3].Participants[0].Miner.String() {
		t.Fatalf("participants : %v", miners)
	}
	if blk["reward_share"] != units(b.c.net.Rewards[3]) {
		t.Fatalf("part de récompense : %v", blk["reward_share"])
	}
}

// Historique complet par l'index, page par page ; un échange réglé et élagué de l'état garde ses
// mouvements (conditions relues dans le bloc d'ouverture).
func TestIndexedHistory(t *testing.T) {
	b := newExplorer(t, nil)
	f := b.c.net.Founders
	seller, arbiter, buyer := f[0], f[1], chaintest.NewWallet(t, b.c.net.P)
	terms, _ := escrow.NewTerms(seller.Addr, buyer.Addr, arbiter.Addr, blockchain.UnitsPerETL/2, 1000, 3, chaintest.Day(1), b.c.net.P)
	open, _ := escrow.Open(seller.Key, terms, b.c.net.P)
	_ = escrow.Cosign(arbiter.Key, &open, time.Unix(chaintest.Day(1), 0), b.c.net.P)
	// Autre échange ouvert avant dans le même bloc : les conditions relues sont celles du bon échange.
	other, _ := escrow.NewTerms(f[2].Addr, f[4].Addr, crypto.Address{}, blockchain.UnitsPerETL/10, 0, 3, chaintest.Day(1), b.c.net.P)
	otherOpen, _ := escrow.Open(f[2].Key, other, b.c.net.P)
	b.c.next(t, 2, func(blk *blockchain.Block) { blk.EscrowOps = []blockchain.EscrowOp{otherOpen, open} })
	paid, _ := escrow.Act(buyer.Key, blockchain.EscrowPaid, terms.ID(), 0, blockchain.SignerBuyer, b.c.net.P)
	b.c.next(t, 3, func(blk *blockchain.Block) { blk.EscrowOps = []blockchain.EscrowOp{paid} })
	rel, _ := escrow.Act(buyer.Key, blockchain.EscrowRelease, terms.ID(), 1, blockchain.SignerBuyer, b.c.net.P)
	_ = rel.Sign(arbiter.Key, blockchain.SignerArbiter, b.c.net.P)
	b.c.next(t, 4, func(blk *blockchain.Block) { blk.EscrowOps = []blockchain.EscrowOp{rel} })
	last := 4 + blockchain.EscrowRetentionDays // échange élagué de l'état
	b.c.next(t, last, nil)
	var inState bool
	b.c.store.ReadState(func(st *blockchain.State) { _, inState = st.Escrow(terms.ID()) })
	if inState {
		t.Fatal("échange encore dans l'état")
	}
	hist := b.get("/api/accounts/" + buyer.Addr.String() + "/history")
	moves := hist["escrow_movements"].([]any)
	if hist["indexed"] != true || len(moves) != 1 || moves[0].(map[string]any)["kind"] != "release" ||
		moves[0].(map[string]any)["height"].(float64) != 4 || moves[0].(map[string]any)["amount"] != units(terms.Amount) ||
		hist["next_before"] != nil {
		t.Fatalf("acheteur : %v", hist)
	}
	if fees := b.get("/api/accounts/" + arbiter.Addr.String() + "/history?before=5")["escrow_movements"].([]any); len(fees) != 1 ||
		fees[0].(map[string]any)["kind"] != "fee" {
		t.Fatalf("arbitre : %v", fees)
	}
	// Vendeur : récompenses des blocs 1 à 5, blocage au bloc 2 ; pages de 2 blocs.
	var heights []string
	before := ""
	for page := 0; ; page++ {
		h := b.get("/api/accounts/" + seller.Addr.String() + "/history?limit=2" + before)
		for _, r := range h["rewards"].([]any) {
			heights = append(heights, fmt.Sprint(r.(map[string]any)["height"]))
		}
		if h["blocks_scanned"].(float64) > 2 || page > 3 {
			t.Fatalf("page trop longue : %v", h)
		}
		next, more := h["next_before"]
		if !more {
			break
		}
		before = fmt.Sprintf("&before=%.0f", next.(float64))
	}
	if got := strings.Join(heights, " "); got != "5 4 3 2 1" { // jour last : hauteur 5
		t.Fatalf("récompenses du vendeur : %s", got)
	}
	// Page de l'échange archivé : conditions du bloc d'ouverture, étapes retrouvées par l'index (aussi
	// page par page : deux blocs à la fois).
	defer func(n int) { stepsPage = n }(stepsPage)
	stepsPage = 2
	arch := b.get("/api/escrows/" + hexOf(terms.ID()))
	var steps []string
	for _, st := range arch["history"].([]any) {
		m := st.(map[string]any)
		steps = append(steps, fmt.Sprint(m["kind"], "@", m["height"], "/", m["signers"]))
	}
	if arch["archived"] != true || arch["outcome"] != "released" || arch["amount"] != units(terms.Amount) ||
		arch["arbiter"] != arbiter.Addr.String() || arch["seller"] != seller.Addr.String() || arch["paid"] != true || arch["disputed"] != nil ||
		strings.Join(steps, " ") != "open@2/5 paid@3/2 release@4/6" ||
		arch["deadline"].(float64) != float64((b.c.blocks[2].Header.Timestamp/86400+int64(terms.Days))*86400) ||
		arch["closed_day"].(float64) != float64(b.c.blocks[4].Header.Timestamp/86400*86400) {
		t.Fatalf("échange archivé : %v (%s)", arch, strings.Join(steps, " "))
	}
	if code, out, _ := b.do(req{path: "/api/search?q=" + hexOf(terms.ID())}); code != 200 || out["type"] != "escrow" {
		t.Fatalf("recherche de l'échange archivé : %d %v", code, out)
	}
	if code, _, _ := b.do(req{path: "/api/escrows/" + hexOf(other.ID())}); code != 200 {
		t.Fatalf("autre échange du même bloc : %d", code)
	}
	plain := newBench(t, nil)
	if code, _, _ := plain.do(req{path: "/api/escrows/" + hexOf(terms.ID())}); code != 404 {
		t.Fatalf("échange inconnu sans index : %d", code)
	}
	for _, q := range []string{"?limit=0", "?limit=101", "?before=x"} {
		if code, _, _ := b.do(req{path: "/api/accounts/" + seller.Addr.String() + "/history" + q}); code != 400 {
			t.Errorf("%s : %d", q, code)
		}
	}
	if h := b.get("/api/accounts/" + seller.Addr.String() + "/history?before=1"); len(h["rewards"].([]any)) != 0 || h["next_before"] != nil {
		t.Fatalf("avant le bloc 1 : %v", h)
	}
}

// --- derrière un proxy HTTPS local (comme Caddy sur le serveur) ---

func TestPublicBehindTLSProxy(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	b := newBench(t, func(c *Config) {
		c.Port = p
		c.Public = &Public{Host: site, ReadPerMinute: 3, WritePerMinute: 1}
		c.Now = time.Now
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- b.s.ListenAndServe(ctx) }()
	target, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", p))
	proxy := httptest.NewUnstartedServer(&httputil.ReverseProxy{Rewrite: func(r *httputil.ProxyRequest) {
		r.SetURL(target)
		r.SetXForwarded() // X-Forwarded-For : adresse du client, valeurs reçues écartées
		r.Out.Host = site
	}})
	proxy.StartTLS()
	defer proxy.Close()
	client := proxy.Client()
	get := func(method, path string, hdr map[string]string) (*http.Response, string) {
		var resp *http.Response
		var err error
		for i := 0; i < 50; i++ {
			r, _ := http.NewRequest(method, proxy.URL+path, strings.NewReader(`{}`))
			for k, v := range hdr {
				r.Header.Set(k, v)
			}
			if resp, err = client.Do(r); err == nil && resp.StatusCode != http.StatusBadGateway {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp, string(body)
	}
	resp, body := get("GET", "/api/status", nil)
	if resp.StatusCode != 200 || resp.TLS == nil || resp.Header.Get("Strict-Transport-Security") == "" || !strings.Contains(body, `"public":true`) {
		t.Fatalf("statut : %d %s %v", resp.StatusCode, body, resp.Header)
	}
	if resp, _ := get("POST", "/api/operator/ipc-read", map[string]string{"Content-Type": "application/json", "Origin": "https://" + site}); resp.StatusCode != 404 {
		t.Fatalf("route opérateur : %d", resp.StatusCode)
	}
	// Le client ne choisit pas son adresse : ses X-Forwarded-For sont écartés par le proxy.
	get("GET", "/api/params", map[string]string{"X-Forwarded-For": "7.7.7.7"})
	get("GET", "/api/ipc", map[string]string{"X-Forwarded-For": "6.6.6.6"}) // 3e lecture : limite atteinte
	if resp, _ := get("GET", "/api/params", map[string]string{"X-Forwarded-For": "8.8.8.8"}); resp.StatusCode != http.StatusTooManyRequests ||
		resp.Header.Get("Retry-After") == "" {
		t.Fatalf("limitation contournée : %d", resp.StatusCode)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("arrêt non effectué")
	}
}
