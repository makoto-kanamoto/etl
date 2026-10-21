package api

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/makoto-kanamoto/etl/internal/blockchain"
	"github.com/makoto-kanamoto/etl/internal/blockchain/chaintest"
	"github.com/makoto-kanamoto/etl/internal/consensus"
	"github.com/makoto-kanamoto/etl/internal/crypto"
	"github.com/makoto-kanamoto/etl/internal/escrow"
	"github.com/makoto-kanamoto/etl/internal/oracle"
)

const port = 3001

// --- doublures ---

type chain struct {
	mu     sync.Mutex
	net    *chaintest.Net
	blocks map[uint64]blockchain.Block
	pruned map[uint64]bool
	state  *blockchain.State // non nil : état présenté à la place de celui de la chaîne de test
}

func (c *chain) Tip() blockchain.Header { c.mu.Lock(); defer c.mu.Unlock(); return c.net.Tip }
func (c *chain) HeaderAt(h uint64) (blockchain.Header, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if h == 0 {
		return c.net.Genesis.Header, nil
	}
	b, ok := c.blocks[h]
	if !ok {
		return blockchain.Header{}, errors.New("inconnu")
	}
	return b.Header, nil
}
func (c *chain) BlockAt(h uint64) (blockchain.Block, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, ok := c.blocks[h]
	if !ok || c.pruned[h] {
		return blockchain.Block{}, errors.New("élagué")
	}
	return b, nil
}
func (c *chain) ReadState(f func(*blockchain.State)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state != nil {
		f(c.state)
		return
	}
	f(c.net.State)
}
func (c *chain) next(t *testing.T, d int, edit func(*blockchain.Block)) blockchain.Block {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, _ := c.net.Next(t, d, edit)
	c.blocks[b.Header.Height] = b
	return b
}

type engine struct {
	arbiter   *crypto.PrivateKey // clé du nœud arbitre (doublure)
	p         blockchain.Params
	err       error
	txs       []blockchain.Transaction
	ops       []blockchain.EscrowOp
	params    []consensus.ParamTally
	exclusion []crypto.Address
	addr      crypto.Address
}

func (e *engine) SubmitTransaction(tx blockchain.Transaction) error {
	e.txs = append(e.txs, tx)
	return e.err
}
func (e *engine) SubmitEscrowOp(op blockchain.EscrowOp) error {
	e.ops = append(e.ops, op)
	return e.err
}
func (e *engine) SubmitParamVote(id blockchain.ParamID, v uint64) error {
	e.params = append(e.params, consensus.ParamTally{ID: id, Value: v, Votes: 1})
	return e.err
}
func (e *engine) SubmitExclusionVote(a crypto.Address) error {
	e.exclusion = append(e.exclusion, a)
	return e.err
}
func (e *engine) IPCTallies() []consensus.IPCTally {
	return []consensus.IPCTally{{Record: blockchain.IPCRecord{Value: 104120, Period: "2026-Q4"}, Votes: 5}}
}
func (e *engine) ParamTallies() []consensus.ParamTally { return e.params }
func (e *engine) Address() crypto.Address              { return e.addr }
func (e *engine) NextNonce(crypto.Address) uint64      { return 7 }
func (e *engine) CosignEscrow(op *blockchain.EscrowOp) error {
	if e.err != nil {
		return e.err
	}
	return op.Sign(e.arbiter, blockchain.SignerArbiter, e.p)
}
func (e *engine) ArbiterSign(op *blockchain.EscrowOp) error {
	if e.err != nil {
		return e.err
	}
	return op.Sign(e.arbiter, blockchain.SignerArbiter, e.p)
}
func (e *engine) ArbiterAct(kind blockchain.EscrowKind, id crypto.Hash) (blockchain.EscrowOp, error) {
	if e.err != nil {
		return blockchain.EscrowOp{}, e.err
	}
	return escrow.Act(e.arbiter, kind, id, 0, blockchain.SignerArbiter, e.p)
}

type netStub struct{}

func (netStub) PeerCount() (int, int) { return 3, 8 }
func (netStub) Halted() bool          { return false }

type oracleStub struct {
	err  error
	rec  *blockchain.IPCRecord
	read int
}

func (o *oracleStub) Status() oracle.Status {
	return oracle.Status{LastAttempt: time.Unix(1_800_000_000, 0), Read: &blockchain.IPCRecord{Value: 104120, Period: "2026-Q4"}, Voted: true}
}
func (o *oracleStub) ReadNow(context.Context, time.Time) (*blockchain.IPCRecord, error) {
	o.read++
	return o.rec, o.err
}

// --- banc ---

type bench struct {
	t     *testing.T
	c     *chain
	e     *engine
	o     *oracleStub
	s     *Server
	token string
	dir   string
}

func newBench(t *testing.T, mod func(*Config)) *bench {
	t.Helper()
	net := chaintest.New(t, chaintest.Params())
	c := &chain{net: net, blocks: map[uint64]blockchain.Block{}, pruned: map[uint64]bool{}}
	c.next(t, 1, nil) // chaque fondateur : 1 ETL
	b := &bench{t: t, c: c, e: &engine{addr: net.Founders[0].Addr, arbiter: net.Founders[1].Key, p: net.P}, o: &oracleStub{}, dir: t.TempDir()}
	cfg := Config{Params: net.P, Port: port, TokenPath: filepath.Join(b.dir, "etl", "dashboard.token"), Version: "test",
		Now: func() time.Time { return time.Unix(chaintest.Day(1)+3600, 0) }, Poll: 10 * time.Millisecond, MaxEvents: 2,
		Chain: c, Engine: b.e, Network: netStub{}, Oracle: b.o,
		Static: fstest.MapFS{
			"dashboard/index.html":   {Data: []byte("<!doctype html>")},
			"wallet/keys.js":         {Data: []byte("export {}")},
			"wallet/keys.test.js":    {Data: []byte("test")},
			"wallet/testdata/v.json": {Data: []byte("{}")},
			"wallet/.cache.js":       {Data: []byte("x")},
			"wallet/notes.md":        {Data: []byte("x")},
		}}
	if mod != nil {
		mod(&cfg)
	}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	b.s = s
	if raw, err := os.ReadFile(cfg.TokenPath); err == nil {
		b.token = strings.TrimSpace(string(raw))
	}
	return b
}

type req struct {
	method, path, body string
	host, origin, ct   string
	token              string
}

func (b *bench) do(r req) (int, map[string]any, http.Header) {
	b.t.Helper()
	if r.method == "" {
		r.method = http.MethodGet
	}
	hr := httptest.NewRequest(r.method, r.path, strings.NewReader(r.body))
	hr.Host = fmt.Sprintf("localhost:%d", port)
	if r.host != "" {
		hr.Host = r.host
	}
	if r.method == http.MethodPost {
		hr.Header.Set("Content-Type", "application/json")
	}
	if r.ct != "" {
		hr.Header.Set("Content-Type", r.ct)
	}
	if r.origin != "" {
		hr.Header.Set("Origin", r.origin)
	}
	if r.token != "" {
		hr.Header.Set("Authorization", "Bearer "+r.token)
	}
	w := httptest.NewRecorder()
	b.s.ServeHTTP(w, hr)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out, w.Header()
}

func (b *bench) get(path string) map[string]any {
	b.t.Helper()
	code, out, _ := b.do(req{path: path})
	if code != http.StatusOK {
		b.t.Fatalf("GET %s : %d %v", path, code, out)
	}
	return out
}

// --- protections ---

func TestHostOriginAndContentType(t *testing.T) {
	b := newBench(t, nil)
	for _, h := range []string{"localhost:3001", "127.0.0.1:3001"} {
		if code, _, _ := b.do(req{path: "/api/status", host: h}); code != http.StatusOK {
			t.Errorf("hôte %s refusé", h)
		}
	}
	for _, h := range []string{"evil.example:3001", "localhost:3002", "localhost", "[::1]:3001", "attaquant.localhost:3001"} {
		if code, _, _ := b.do(req{path: "/api/status", host: h}); code != http.StatusForbidden {
			t.Errorf("hôte %s accepté (rebond DNS)", h)
		}
	}
	tx := hex.EncodeToString(blockchain.EncodeTransaction(ptrTx(b.validTx())))
	body := `{"tx":"` + tx + `"}`
	if code, _, _ := b.do(req{method: "POST", path: "/api/tx", body: body, origin: "https://evil.example"}); code != http.StatusForbidden {
		t.Fatalf("origine hostile : %d", code)
	}
	if code, _, _ := b.do(req{method: "POST", path: "/api/tx", body: body, origin: "http://localhost:3001"}); code != http.StatusOK {
		t.Fatalf("origine du dashboard : %d", code)
	}
	if code, _, _ := b.do(req{method: "POST", path: "/api/tx", body: body}); code != http.StatusOK {
		t.Fatalf("sans origine (client local hors navigateur) : %d", code)
	}
	for _, ct := range []string{"text/plain", "application/x-www-form-urlencoded", "multipart/form-data"} {
		if code, _, _ := b.do(req{method: "POST", path: "/api/tx", body: body, ct: ct}); code != http.StatusUnsupportedMediaType {
			t.Errorf("Content-Type %s accepté (formulaire d'un autre site)", ct)
		}
	}
	// Requête valide suivie de 70 Kio d'espaces : JSON correct, mais corps au-delà de 64 Kio.
	if code, _, _ := b.do(req{method: "POST", path: "/api/tx", body: body + strings.Repeat(" ", 70<<10)}); code != http.StatusBadRequest {
		t.Fatalf("corps démesuré : %d", code)
	}
	if code, _, _ := b.do(req{method: "POST", path: "/", body: `{}`}); code != http.StatusMethodNotAllowed {
		t.Fatalf("POST sur l'interface : %d", code)
	}
	if code, _, _ := b.do(req{method: "POST", path: "/api/tx", body: `{"tx":"00","autre":1}`}); code != http.StatusBadRequest {
		t.Fatalf("champ inconnu : %d", code)
	}
	if code, _, _ := b.do(req{method: "POST", path: "/api/status", body: `{}`}); code != http.StatusMethodNotAllowed && code != http.StatusNotFound {
		t.Fatalf("méthode non prévue : %d", code)
	}
}

func TestSecurityHeadersEverywhere(t *testing.T) {
	b := newBench(t, nil)
	for _, r := range []req{{path: "/api/status"}, {path: "/api/inconnu"}, {path: "/"}, {path: "/api/status", host: "evil:1"}} {
		_, _, h := b.do(r)
		csp := h.Get("Content-Security-Policy")
		want := "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; font-src 'self'; " +
			"manifest-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"
		if csp != want ||
			h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Referrer-Policy") != "no-referrer" {
			t.Errorf("%s (hôte %q) : en-têtes %v", r.path, r.host, h)
		}
		if h.Get("Access-Control-Allow-Origin") != "" {
			t.Errorf("%s : en-tête CORS présent", r.path)
		}
	}
}

func TestOperatorToken(t *testing.T) {
	b := newBench(t, nil)
	fi, err := os.Stat(b.s.cfg.TokenPath)
	if err != nil || fi.Mode().Perm() != 0o600 || len(b.token) != 64 {
		t.Fatalf("fichier du jeton : %v, %v, %d caractères", err, fi.Mode().Perm(), len(b.token))
	}
	if di, _ := os.Stat(filepath.Dir(b.s.cfg.TokenPath)); di.Mode().Perm() != 0o700 {
		t.Fatalf("répertoire : %v", di.Mode().Perm())
	}
	for path, body := range map[string]string{
		"/api/operator/param-vote":     `{"param":"fee_bps","value":"20"}`,
		"/api/operator/exclusion-vote": `{"target":"` + b.c.net.Founders[4].Addr.String() + `"}`,
		"/api/operator/ipc-read":       `{}`,
		"/api/operator/escrow/cosign":  `{"op":"x"}`,
		"/api/operator/escrow/sign":    `{"op":"x"}`,
		"/api/operator/escrow/act":     `{"escrow":"x","kind":"release"}`,
	} {
		if code, _, _ := b.do(req{method: "POST", path: path, body: body}); code != http.StatusUnauthorized {
			t.Errorf("%s sans jeton : %d", path, code)
		}
	}
	if len(b.e.exclusion) != 0 || b.o.read != 0 {
		t.Fatal("action exécutée sans jeton")
	}
	body := `{"param":"fee_bps","value":"20"}`
	for name, tok := range map[string]string{"absent": "", "faux": strings.Repeat("0", 64), "tronqué": b.token[:63]} {
		if code, _, _ := b.do(req{method: "POST", path: "/api/operator/param-vote", body: body, token: tok}); code != http.StatusUnauthorized {
			t.Errorf("jeton %s : %d", name, code)
		}
	}
	if code, _, _ := b.do(req{method: "POST", path: "/api/operator/param-vote", body: body, token: b.token}); code != http.StatusOK {
		t.Fatalf("bon jeton : %d", code)
	}
	if len(b.e.params) != 1 {
		t.Fatal("vote non transmis au moteur")
	}
	// Nouveau démarrage : nouveau jeton, l'ancien ne vaut plus rien.
	old := b.token
	b2 := newBench(t, func(c *Config) { c.TokenPath = b.s.cfg.TokenPath })
	if b2.token == old {
		t.Fatal("jeton non renouvelé au démarrage")
	}
	if code, _, _ := b2.do(req{method: "POST", path: "/api/operator/param-vote", body: body, token: old}); code != http.StatusUnauthorized {
		t.Fatal("ancien jeton accepté")
	}
}

func TestReadOnly(t *testing.T) {
	b := newBench(t, func(c *Config) { c.ReadOnly = true; c.TokenPath = "" })
	if _, err := os.Stat(filepath.Join(b.dir, "etl", "dashboard.token")); err == nil {
		t.Fatal("jeton écrit en lecture seule")
	}
	for _, p := range []string{"/api/tx", "/api/escrow", "/api/operator/param-vote", "/api/operator/exclusion-vote", "/api/operator/ipc-read"} {
		if code, _, _ := b.do(req{method: "POST", path: p, body: `{}`}); code != http.StatusNotFound && code != http.StatusMethodNotAllowed {
			t.Errorf("%s disponible en lecture seule : %d", p, code)
		}
	}
	if b.get("/api/status")["read_only"] != true {
		t.Fatal("statut : lecture seule non signalée")
	}
	if _, err := New(Config{Params: b.s.cfg.Params, Port: port}); err == nil {
		t.Fatal("serveur modifiable sans chemin de jeton")
	}
}

// --- lecture ---

func TestStatus(t *testing.T) {
	b := newBench(t, nil)
	st := b.get("/api/status")
	if st["chain_id"] != "ETL-FR" || st["height"].(float64) != 1 || st["active_nodes"].(float64) != 10 {
		t.Fatalf("statut : %v", st)
	}
	node := st["node"].(map[string]any)
	if node["address"] != b.c.net.Founders[0].Addr.String() || node["status"] != "admitted" {
		t.Fatalf("nœud : %v", node)
	}
	if st["next_block"].(float64) != float64(chaintest.Day(2)) {
		t.Fatalf("prochain bloc : %v (bloc du jour 1 déjà finalisé)", st["next_block"])
	}
	if p := st["peers"].(map[string]any); p["in"].(float64) != 3 || p["out"].(float64) != 8 {
		t.Fatalf("pairs : %v", p)
	}
	passive := newBench(t, func(c *Config) { c.Engine, c.Network, c.Oracle = nil, nil, nil })
	if n := passive.get("/api/status")["node"].(map[string]any); n["address"] != "" {
		t.Fatalf("nœud passif : %v", n)
	}
}

func (b *bench) validTx() blockchain.Transaction {
	f := b.c.net.Founders
	return b.c.net.Tx(b.t, f[1], f[2].Addr, blockchain.UnitsPerETL/4, 0, chaintest.Day(1))
}

func ptrTx(tx blockchain.Transaction) *blockchain.Transaction { return &tx }

func TestBlocksAccountsHistory(t *testing.T) {
	b := newBench(t, nil)
	f := b.c.net.Founders
	tx := b.validTx()
	b.c.next(t, 2, func(blk *blockchain.Block) { blk.Txs = []blockchain.Transaction{tx} })
	b.c.next(t, 3, nil)

	code, list, _ := b.do(req{path: "/api/blocks?limit=2"})
	_ = list
	if code != http.StatusOK {
		t.Fatalf("liste : %d", code)
	}
	var headers []map[string]any
	{
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/api/blocks?limit=10", nil)
		r.Host = "localhost:3001"
		b.s.ServeHTTP(w, r)
		_ = json.Unmarshal(w.Body.Bytes(), &headers)
	}
	if len(headers) != 4 || headers[0]["height"].(float64) != 3 || headers[3]["height"].(float64) != 0 {
		t.Fatalf("en-têtes : %v", headers)
	}
	for _, bad := range []string{"0", "101", "x", "-1"} {
		if code, _, _ := b.do(req{path: "/api/blocks?limit=" + bad}); code != http.StatusBadRequest {
			t.Errorf("limit=%s : %d", bad, code)
		}
	}
	blk := b.get("/api/blocks/2")
	txs := blk["txs"].([]any)
	if len(txs) != 1 || txs[0].(map[string]any)["amount"] != "25000000" || txs[0].(map[string]any)["id"] != hexOf(tx.ID) {
		t.Fatalf("bloc 2 : %v", blk)
	}
	b.c.pruned[2] = true
	if blk := b.get("/api/blocks/2"); blk["pruned"] != true || blk["header"] == nil {
		t.Fatalf("bloc élagué : %v", blk)
	}
	delete(b.c.pruned, 2)
	for path, want := range map[string]int{"/api/blocks/99": 404, "/api/blocks/x": 400, "/api/blocks/-1": 400} {
		if code, _, _ := b.do(req{path: path}); code != want {
			t.Errorf("%s : %d", path, code)
		}
	}
	acc := b.get("/api/accounts/" + f[2].Addr.String())
	if acc["balance"] == "" || acc["nonce"] != "0" || acc["next_nonce"] != "7" || acc["node"] != "admitted" {
		t.Fatalf("compte : %v", acc)
	}
	hist := b.get("/api/accounts/" + f[1].Addr.String() + "/history")
	if got := hist["transactions"].([]any); len(got) != 1 || got[0].(map[string]any)["height"].(float64) != 2 {
		t.Fatalf("historique : %v", hist)
	}
	if got := b.get("/api/accounts/" + f[2].Addr.String() + "/history")["transactions"].([]any); len(got) != 1 {
		t.Fatalf("historique du destinataire : %v", got)
	}
	if hist["blocks_scanned"].(float64) != 3 {
		t.Fatalf("blocs parcourus : %v", hist["blocks_scanned"])
	}
	for _, bad := range []string{"etl1qinvalide", "etlde" + f[1].Addr.String()[3:], "x"} {
		if code, _, _ := b.do(req{path: "/api/accounts/" + bad}); code != http.StatusBadRequest {
			t.Errorf("adresse %s : %d", bad, code)
		}
	}
}

func TestEscrowIPCParams(t *testing.T) {
	b := newBench(t, nil)
	f := b.c.net.Founders
	buyer := chaintest.NewWallet(t, b.c.net.P)
	terms, err := escrow.NewTerms(f[0].Addr, buyer.Addr, f[1].Addr, blockchain.UnitsPerETL/2, 1000, 3, chaintest.Day(1), b.c.net.P)
	if err != nil {
		t.Fatal(err)
	}
	op, _ := escrow.Open(f[0].Key, terms, b.c.net.P)
	_ = escrow.Cosign(f[1].Key, &op, time.Unix(chaintest.Day(1), 0), b.c.net.P)
	b.c.next(t, 2, func(blk *blockchain.Block) { blk.EscrowOps = []blockchain.EscrowOp{op} })
	id := terms.ID()
	e := b.get("/api/escrows/" + hexOf(id))
	if e["seller"] != f[0].Addr.String() || e["arbiter"] != f[1].Addr.String() || e["amount"] != "50000000" || e["outcome"] != "pending" {
		t.Fatalf("échange : %v", e)
	}
	req := e["required"].(map[string]any)
	if fmt.Sprint(req["paid"]) != "[2]" || fmt.Sprint(req["release"]) != "[1 6]" {
		t.Fatalf("signatures possibles : %v", req)
	}
	if acc := b.get("/api/accounts/" + buyer.Addr.String()); fmt.Sprint(acc["escrows"]) != "["+hexOf(id)+"]" {
		t.Fatalf("échanges de l'acheteur : %v", acc["escrows"])
	}
	if blk := b.get("/api/blocks/2"); len(blk["escrow_ops"].([]any)) != 1 {
		t.Fatalf("opération dans le bloc : %v", blk)
	}
	for path, want := range map[string]int{"/api/escrows/" + strings.Repeat("ab", 32): 404, "/api/escrows/xyz": 400, "/api/escrows/abcd": 400} {
		if code, _, _ := b.do(r(path)); code != want {
			t.Errorf("%s : %d", path, code)
		}
	}
	ipc := b.get("/api/ipc")
	if ipc["quorum"].(float64) != 7 || len(ipc["pending"].([]any)) != 1 || ipc["oracle"].(map[string]any)["voted"] != true {
		t.Fatalf("IPC : %v", ipc)
	}
	b.e.params = []consensus.ParamTally{}
	b.e.params = append(b.e.params, consensus.ParamTally{ID: blockchain.ParamFeeBps, Value: 20, Votes: 4})
	ps := b.get("/api/params")["params"].([]any)
	first := ps[0].(map[string]any)
	if len(ps) != len(blockchain.GovernableParams()) || first["name"] != "fee_bps" || first["value"] != "10" || first["votes"] == nil {
		t.Fatalf("paramètres : %v", first)
	}
}

func r(path string) req { return req{path: path} }

// --- soumissions et actions ---

func TestSubmissions(t *testing.T) {
	b := newBench(t, nil)
	tx := b.validTx()
	enc := hex.EncodeToString(blockchain.EncodeTransaction(&tx))
	code, out, _ := b.do(req{method: "POST", path: "/api/tx", body: `{"tx":"` + enc + `"}`})
	if code != http.StatusOK || out["id"] != hexOf(tx.ID) || len(b.e.txs) != 1 {
		t.Fatalf("transaction : %d %v", code, out)
	}
	for body, want := range map[string]int{`{"tx":"zz"}`: 400, `{"tx":"00"}`: 400, `[]`: 400, `{"tx":"` + enc + `"}{}`: 400} {
		if code, _, _ := b.do(req{method: "POST", path: "/api/tx", body: body}); code != want {
			t.Errorf("%s : %d", body, code)
		}
	}
	b.e.err = errors.New("nonce incorrect")
	if code, out, _ := b.do(req{method: "POST", path: "/api/tx", body: `{"tx":"` + enc + `"}`}); code != http.StatusConflict || out["error"] != "nonce incorrect" {
		t.Fatalf("refus du moteur : %d %v", code, out)
	}
	b.e.err = nil

	f := b.c.net.Founders
	terms, _ := escrow.NewTerms(f[0].Addr, f[3].Addr, crypto.Address{}, 1000, 0, 3, chaintest.Day(1), b.c.net.P)
	op, _ := escrow.Open(f[0].Key, terms, b.c.net.P)
	code, out, _ = b.do(req{method: "POST", path: "/api/escrow", body: `{"op":"` + escrow.EncodeText(&op) + `"}`})
	if code != http.StatusOK || out["escrow"] != hexOf(terms.ID()) || len(b.e.ops) != 1 {
		t.Fatalf("escrow : %d %v", code, out)
	}
	if code, _, _ := b.do(req{method: "POST", path: "/api/escrow", body: `{"op":"etl-escrow:@@"}`}); code != http.StatusBadRequest {
		t.Fatalf("texte invalide : %d", code)
	}

	tok := b.token
	if code, _, _ := b.do(req{method: "POST", path: "/api/operator/exclusion-vote", body: `{"target":"` + f[4].Addr.String() + `"}`, token: tok}); code != 200 || len(b.e.exclusion) != 1 {
		t.Fatalf("exclusion : %d", code)
	}
	for body, want := range map[string]int{
		`{"target":"x"}`:                    400,
		`{"param":"inconnu","value":"1"}`:   400,
		`{"param":"fee_bps","value":"-1"}`:  400,
		`{"param":"fee_bps","value":"1e3"}`: 400,
	} {
		path := "/api/operator/param-vote"
		if strings.Contains(body, "target") {
			path = "/api/operator/exclusion-vote"
		}
		if code, _, _ := b.do(req{method: "POST", path: path, body: body, token: tok}); code != want {
			t.Errorf("%s : %d", body, code)
		}
	}
	// Relire la source : succès, trop tôt, échec de la source.
	b.o.rec = &blockchain.IPCRecord{Value: 104120, Period: "2026-Q4"}
	if code, out, _ := b.do(req{method: "POST", path: "/api/operator/ipc-read", body: `{}`, token: tok}); code != 200 || out["voted"] != true {
		t.Fatalf("relecture : %d %v", code, out)
	}
	b.o.err = oracle.ErrTooSoon
	if code, _, _ := b.do(req{method: "POST", path: "/api/operator/ipc-read", body: `{}`, token: tok}); code != http.StatusTooManyRequests {
		t.Fatalf("trop tôt : %d", code)
	}
	b.o.err = errors.New("délai dépassé")
	if code, _, _ := b.do(req{method: "POST", path: "/api/operator/ipc-read", body: `{}`, token: tok}); code != http.StatusBadGateway {
		t.Fatalf("source en échec : %d", code)
	}
	if code, _, _ := b.do(req{method: "POST", path: "/api/operator/ipc-read", body: `{"value":103}`, token: tok}); code != http.StatusBadRequest {
		t.Fatal("une valeur saisie à la main ne doit pas être acceptée")
	}
	// Nœud passif ou sans oracle : 503.
	p := newBench(t, func(c *Config) { c.Engine, c.Oracle = nil, nil })
	if code, _, _ := p.do(req{method: "POST", path: "/api/tx", body: `{"tx":"` + enc + `"}`}); code != http.StatusServiceUnavailable {
		t.Fatalf("passif : %d", code)
	}
	if code, _, _ := p.do(req{method: "POST", path: "/api/operator/ipc-read", body: `{}`, token: p.token}); code != http.StatusServiceUnavailable {
		t.Fatalf("sans oracle : %d", code)
	}
}

// --- notifications ---

func TestEventsStream(t *testing.T) {
	b := newBench(t, nil)
	srv := httptest.NewUnstartedServer(b.s)
	srv.Start()
	defer srv.Close()
	open := func() (*http.Response, error) {
		rq, _ := http.NewRequest("GET", srv.URL+"/api/events", nil)
		rq.Host = "localhost:3001"
		return srv.Client().Do(rq)
	}
	resp, err := open()
	if err != nil || resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("flux : %v %v", err, resp)
	}
	defer resp.Body.Close()
	second, _ := open()
	defer second.Body.Close()
	if third, err := open(); err != nil || third.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("troisième client (max 2) : %v %v", err, third.StatusCode)
	}
	tx := b.validTx()
	b.c.next(t, 2, func(blk *blockchain.Block) { blk.Txs = []blockchain.Transaction{tx} })
	rd := bufio.NewReader(resp.Body)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		line, err := rd.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if data, ok := strings.CutPrefix(line, "data: "); ok {
			var ev map[string]any
			if err := json.Unmarshal([]byte(data), &ev); err != nil || ev["height"].(float64) != 2 ||
				ev["txs"].([]any)[0].(map[string]any)["id"] != hexOf(tx.ID) {
				t.Fatalf("événement : %v %s", err, data)
			}
			return
		}
	}
	t.Fatal("aucun événement reçu")
}

// --- interface ---

func TestStatic(t *testing.T) {
	b := newBench(t, nil)
	get := func(p string) (int, string, string) {
		w := httptest.NewRecorder()
		rq := httptest.NewRequest("GET", p, nil)
		rq.Host = "localhost:3001"
		b.s.ServeHTTP(w, rq)
		return w.Code, w.Header().Get("Content-Type"), w.Body.String()
	}
	if code, ct, body := get("/"); code != 200 || !strings.HasPrefix(ct, "text/html") || !strings.HasPrefix(body, "<!doctype html>") {
		t.Fatalf("accueil : %d %s", code, ct)
	}
	if code, ct, _ := get("/wallet/keys.js"); code != 200 || !strings.HasPrefix(ct, "text/javascript") {
		t.Fatalf("module : %d %s", code, ct)
	}
	for _, p := range []string{"/wallet/keys.test.js", "/wallet/testdata/v.json", "/wallet/.cache.js", "/wallet/notes.md", "/../etc/passwd", "/wallet/../wallet/keys.test.js", "/wallet/absent.js"} {
		if code, _, _ := get(p); code == 200 {
			t.Errorf("%s servi : %d", p, code)
		}
	}
}

// --- écoute réelle ---

func TestListenLoopbackOnly(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	b := newBench(t, func(c *Config) { c.Port = p })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- b.s.ListenAndServe(ctx) }()
	var resp *http.Response
	for i := 0; i < 50; i++ {
		resp, err = http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/status", p))
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("écoute : %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	// Aucune écoute sur les autres interfaces.
	if addrs, _ := net.InterfaceAddrs(); len(addrs) > 0 {
		for _, a := range addrs {
			ip, _, _ := net.ParseCIDR(a.String())
			if ip == nil || ip.IsLoopback() || ip.To4() == nil {
				continue
			}
			if c, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", ip, p), 300*time.Millisecond); err == nil {
				c.Close()
				t.Fatalf("joignable sur %s", ip)
			}
		}
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

// Prochain bloc : 23:00 du jour, ou du lendemain si l'heure est passée ou le bloc du jour finalisé.
func TestNextBlockTime(t *testing.T) {
	b := newBench(t, nil)
	d1, d2, d3 := chaintest.Day(1), chaintest.Day(2), chaintest.Day(3)
	for _, c := range []struct {
		now, tip, want int64
	}{
		{d1 - 3600, chaintest.Day(0), d1}, // 22:00 le jour 1, bloc du jour 0 : ce soir
		{d1 + 60, chaintest.Day(0), d2},   // 23:01 : demain
		{d1 - 3600, d1, d2},               // bloc du jour 1 déjà finalisé (en avance) : demain
		{d1 - 3600, d2, d3},               // tête plus loin encore
	} {
		b.s.cfg.Now = func() time.Time { return time.Unix(c.now, 0) }
		if got := b.s.nextBlockTime(blockchain.Header{Timestamp: c.tip}); got != c.want {
			t.Errorf("maintenant %d, tête %d : %d, attendu %d", c.now, c.tip, got, c.want)
		}
	}
}

// État présenté sur mesure : arbitre exclu (plus de règlement avec lui) et paramètres votés en vigueur.
func TestExcludedArbiterAndParamsInForce(t *testing.T) {
	b := newBench(t, nil)
	f := b.c.net.Founders
	terms, _ := escrow.NewTerms(f[0].Addr, f[2].Addr, f[1].Addr, 1000, 10, 3, chaintest.Day(1), b.c.net.P)
	id := terms.ID()
	b.c.state = blockchain.NewStateFrom(nil,
		map[[crypto.AddressHashSize]byte]blockchain.Node{f[1].Addr.Hash: {Status: blockchain.StatusExcluded}},
		blockchain.IPCRecord{}, blockchain.IPCRecord{}, map[blockchain.ParamID]uint64{blockchain.ParamFeeBps: 20}, nil,
		blockchain.EscrowData{Escrows: map[crypto.Hash]blockchain.Escrow{id: {Terms: terms, Deadline: 1 << 40}}})
	req := b.get("/api/escrows/" + hexOf(id))["required"].(map[string]any)
	if fmt.Sprint(req["release"]) != "[1]" || fmt.Sprint(req["refund"]) != "[2]" {
		t.Fatalf("arbitre exclu : %v", req)
	}
	if first := b.get("/api/params")["params"].([]any)[0].(map[string]any); first["value"] != "20" {
		t.Fatalf("frais en vigueur : %v", first)
	}
}

// L'import map de la page est le seul script en ligne autorisé, par son empreinte exacte.
func TestImportMapHashInCSP(t *testing.T) {
	importMap := `{"integrity":{"/wallet/keys.js":"sha384-x"}}`
	page := "<!doctype html><script type=\"importmap\">" + importMap + "</script><script type=\"module\" src=\"/dashboard/app.js\"></script>"
	b := newBench(t, func(c *Config) { c.Static = fstest.MapFS{"dashboard/index.html": {Data: []byte(page)}} })
	_, _, h := b.do(req{path: "/"})
	sum := sha256.Sum256([]byte(importMap))
	want := "script-src 'self' 'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "';"
	if !strings.Contains(h.Get("Content-Security-Policy"), want) {
		t.Fatalf("CSP : %s", h.Get("Content-Security-Policy"))
	}
	// Une seconde import map (injectée) n'est jamais autorisée : refus au démarrage.
	double := page + "<script type=\"importmap\">{}</script>"
	if _, err := New(Config{Params: b.c.net.P, Port: port, TokenPath: filepath.Join(t.TempDir(), "t"),
		Static: fstest.MapFS{"dashboard/index.html": {Data: []byte(double)}}}); err == nil {
		t.Fatal("deux import maps acceptées")
	}
	if _, err := New(Config{Params: b.c.net.P, Port: port, TokenPath: filepath.Join(t.TempDir(), "t"),
		Static: fstest.MapFS{"dashboard/index.html": {Data: []byte("<script type=\"importmap\">{}")}}}); err == nil {
		t.Fatal("import map non fermée acceptée")
	}
}

// Arbitrage par le nœud (jeton requis) : cosigner une ouverture, compléter ou initier un règlement.
func TestArbiterEndpoints(t *testing.T) {
	b := newBench(t, nil)
	f := b.c.net.Founders
	tok := b.token
	terms, _ := escrow.NewTerms(f[0].Addr, f[2].Addr, f[1].Addr, 1000, 10, 3, chaintest.Day(1), b.c.net.P)
	open, _ := escrow.Open(f[0].Key, terms, b.c.net.P)
	post := func(path, body string) (int, map[string]any) {
		code, out, _ := b.do(req{method: "POST", path: path, body: body, token: tok})
		return code, out
	}
	code, out := post("/api/operator/escrow/cosign", `{"op":"`+escrow.EncodeText(&open)+`"}`)
	if code != 200 || out["escrow"] != hexOf(terms.ID()) || len(b.e.ops) != 1 || b.e.ops[0].Signers != blockchain.SignerSeller|blockchain.SignerArbiter {
		t.Fatalf("cosignature : %d %v", code, out)
	}
	got, err := escrow.DecodeText(out["op"].(string), b.c.net.P.AddressHRP)
	if err != nil || got.Signers != 5 {
		t.Fatalf("texte renvoyé : %v %+v", err, got)
	}
	rel, _ := escrow.Act(f[2].Key, blockchain.EscrowRelease, terms.ID(), 0, blockchain.SignerBuyer, b.c.net.P)
	if code, _ := post("/api/operator/escrow/cosign", `{"op":"`+escrow.EncodeText(&rel)+`"}`); code != http.StatusConflict {
		t.Fatalf("cosignature d'un règlement : %d", code)
	}
	if code, out := post("/api/operator/escrow/sign", `{"op":"`+escrow.EncodeText(&rel)+`"}`); code != 200 || len(b.e.ops) != 2 || b.e.ops[1].Signers != 6 {
		t.Fatalf("complément : %d %v", code, out)
	}
	code, out = post("/api/operator/escrow/act", `{"escrow":"`+hexOf(terms.ID())+`","kind":"refund"}`)
	if code != 200 {
		t.Fatalf("initiative : %d %v", code, out)
	}
	if op, err := escrow.DecodeText(out["op"].(string), b.c.net.P.AddressHRP); err != nil || op.Kind != blockchain.EscrowRefund || op.Signers != 4 {
		t.Fatalf("opération préparée : %v %+v", err, op)
	}
	for body, want := range map[string]int{
		`{"escrow":"` + hexOf(terms.ID()) + `","kind":"paid"}`: 400,
		`{"escrow":"` + hexOf(terms.ID()) + `","kind":"x"}`:    400,
		`{"escrow":"abcd","kind":"release"}`:                   400,
	} {
		if code, _ := post("/api/operator/escrow/act", body); code != want {
			t.Errorf("%s : %d", body, code)
		}
	}
	if code, _ := post("/api/operator/escrow/sign", `{"op":"etl-escrow:@@"}`); code != 400 {
		t.Fatalf("texte invalide : %d", code)
	}
	b.e.err = escrow.ErrRole
	for _, path := range []string{"/api/operator/escrow/cosign", "/api/operator/escrow/sign"} {
		if code, out := post(path, `{"op":"`+escrow.EncodeText(&open)+`"}`); code != http.StatusConflict || out["error"] != escrow.ErrRole.Error() {
			t.Errorf("%s refusé par le moteur : %d %v", path, code, out)
		}
	}
	if code, _ := post("/api/operator/escrow/act", `{"escrow":"`+hexOf(terms.ID())+`","kind":"release"}`); code != http.StatusConflict {
		t.Fatalf("initiative refusée : %d", code)
	}
	n := len(b.e.ops)
	if n != 2 {
		t.Fatalf("une opération refusée a été soumise (%d)", n)
	}
	p := newBench(t, func(c *Config) { c.Engine = nil })
	if code, _, _ := p.do(req{method: "POST", path: "/api/operator/escrow/act", body: `{"escrow":"` + hexOf(terms.ID()) + `","kind":"release"}`, token: p.token}); code != http.StatusServiceUnavailable {
		t.Fatalf("nœud passif : %d", code)
	}
}
