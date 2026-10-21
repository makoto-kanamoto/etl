package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/makoto-kanamoto/etl/internal/config"
	"github.com/makoto-kanamoto/etl/internal/crypto"
	"github.com/makoto-kanamoto/etl/internal/node"
)

// testApp : saisies secrètes successives fournies par le test, sorties capturées.
type testApp struct {
	*app
	out, errw bytes.Buffer
	answers   []string
}

func newApp(t *testing.T, ctx context.Context, answers ...string) *testApp {
	ta := &testApp{answers: answers}
	ta.app = &app{out: &ta.out, errw: &ta.errw, ctx: ctx, secret: func(string) (string, error) {
		if len(ta.answers) == 0 {
			return "", errors.New("plus de saisie prévue")
		}
		a := ta.answers[0]
		ta.answers = ta.answers[1:]
		return a, nil
	}}
	return ta
}

const pass = "phrase de passe du nœud"

func writeTestConfig(t *testing.T, home string, extra string) {
	t.Helper()
	_ = os.MkdirAll(home, 0o700)
	src := strings.Replace(defaultConfig(home), `network = "ETL-FR"`, `network = "test"`, 1) + extra
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
}

// La configuration écrite par « etl init » est la configuration par défaut, chemins sous home.
func TestInitConfigIsDefault(t *testing.T) {
	home := t.TempDir()
	c, err := config.Parse(defaultConfig(home), home)
	if err != nil || !reflect.DeepEqual(c, config.Default(home)) {
		t.Fatalf("%v\n%+v\n%+v", err, c, config.Default(home))
	}
}

func TestInitAddressPassphraseRestore(t *testing.T) {
	home := filepath.Join(t.TempDir(), "etl")
	a := newApp(t, context.Background(), pass, pass)
	if err := a.run([]string{"-home", home, "init"}); err != nil {
		t.Fatal(err)
	}
	out := a.out.String()
	for _, f := range []string{"config.toml", "wallet.key"} {
		fi, err := os.Stat(filepath.Join(home, f))
		if err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("%s : %v %v", f, err, fi)
		}
	}
	if !strings.Contains(out, "Bitcoin") || !strings.Contains(out, "24.") || !strings.Contains(out, "Adresse : etl1q") {
		t.Fatalf("sortie d'init : %s", out)
	}
	if !strings.Contains(out, "\n 5. ") || !strings.Contains(out, "\n21. ") {
		t.Fatalf("phrase : quatre mots par ligne attendus : %s", out)
	}
	words := phraseFrom(out)
	if len(words) != 24 {
		t.Fatalf("%d mots affichés", len(words))
	}
	addr := strings.TrimSpace(strings.SplitN(strings.Split(out, "Adresse : ")[1], "\n", 2)[0])

	// init une seconde fois : la clé existante n'est jamais écrasée.
	if err := newApp(t, context.Background(), pass, pass).run([]string{"-home", home, "init"}); err == nil {
		t.Fatal("clé écrasée par un second init")
	}
	b := newApp(t, context.Background())
	if err := b.run([]string{"-home", home, "address"}); err != nil || strings.TrimSpace(b.out.String()) != addr {
		t.Fatalf("address : %v %q", err, b.out.String())
	}
	// Changement de phrase de passe : l'ancienne refusée, la nouvelle confirmée deux fois.
	if err := newApp(t, context.Background(), "mauvaise phrase de passe", "nouvelle phrase longue", "nouvelle phrase longue").run([]string{"-home", home, "passphrase"}); err == nil {
		t.Fatal("ancienne phrase de passe fausse acceptée")
	}
	if err := newApp(t, context.Background(), pass, "nouvelle phrase longue", "autre chose").run([]string{"-home", home, "passphrase"}); err == nil {
		t.Fatal("confirmation différente acceptée")
	}
	if err := newApp(t, context.Background(), pass, "nouvelle phrase longue", "nouvelle phrase longue").run([]string{"-home", home, "passphrase"}); err != nil {
		t.Fatal(err)
	}
	// Restauration ailleurs à partir de la phrase : même adresse.
	other := filepath.Join(t.TempDir(), "etl")
	_ = os.MkdirAll(other, 0o700)
	r := newApp(t, context.Background(), strings.Join(words, " "), pass, pass)
	if err := r.run([]string{"-home", other, "restore"}); err != nil || !strings.Contains(r.out.String(), addr) {
		t.Fatalf("restore : %v %s", err, r.out.String())
	}
	if err := newApp(t, context.Background(), "mots faux", pass, pass).run([]string{"-home", t.TempDir(), "restore"}); err == nil {
		t.Fatal("phrase invalide acceptée")
	}
	if err := newApp(t, context.Background(), pass, pass).run([]string{"-home", t.TempDir(), "init", "-lang", "de"}); err == nil {
		t.Fatal("langue inconnue acceptée")
	}
	en := newApp(t, context.Background(), pass, pass)
	if err := en.run([]string{"-home", filepath.Join(t.TempDir(), "en"), "init", "-lang", "en"}); err != nil || len(phraseFrom(en.out.String())) != 24 {
		t.Fatalf("init en anglais : %v", err)
	}
}

// phraseFrom extrait les 24 mots affichés après l'avertissement (numéros retirés).
func phraseFrom(out string) []string {
	i := strings.LastIndex(out, "Étalon.")
	return strings.Fields(strings.NewReplacer(".", " ").Replace(strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return ' '
		}
		return r
	}, out[i+len("Étalon."):])))
}

func TestPassphraseFile(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "pass")
	_ = os.WriteFile(f, []byte(pass+"\n"), 0o644)
	a := newApp(t, context.Background())
	if _, err := a.passphrase(f, ""); err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Fatalf("permissions 0644 : %v", err)
	}
	_ = os.Chmod(f, 0o640) // lisible par le groupe : refusé aussi
	if _, err := a.passphrase(f, ""); err == nil {
		t.Fatal("permissions 0640 acceptées")
	}
	_ = os.Chmod(f, 0o600)
	if p, err := a.passphrase(f, ""); err != nil || p != pass {
		t.Fatalf("fichier 0600 : %q %v", p, err)
	}
	if _, err := a.passphrase(filepath.Join(dir, "absent"), ""); err == nil {
		t.Fatal("fichier absent accepté")
	}
}

func freePort(t *testing.T) int {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// genesis puis run : le vrai nœud démarre (réseau de test), le dashboard répond, Ctrl-C l'arrête.
func TestGenesisAndRun(t *testing.T) {
	home := filepath.Join(t.TempDir(), "etl")
	p2p, dash := freePort(t), freePort(t)
	writeTestConfig(t, home, "")
	cfg := strings.NewReplacer("port    = 3000", fmt.Sprintf("port    = %d", p2p), "port  = 3001", fmt.Sprintf("port  = %d", dash),
		`"etl1.etalon-etl.fr:3000",`, "").Replace(readFile(t, filepath.Join(home, "config.toml")))
	_ = os.WriteFile(filepath.Join(home, "config.toml"), []byte(cfg), 0o600)
	passFile := filepath.Join(home, "pass")
	_ = os.WriteFile(passFile, []byte(pass), 0o600)
	if err := newApp(t, context.Background()).run([]string{"-home", home, "init", "-passphrase-file", passFile}); err != nil {
		t.Fatal(err)
	}
	var addrs []string
	for i := 0; i < 3; i++ {
		k, _ := crypto.GenerateKey()
		addrs = append(addrs, k.PublicKey().Address("etlt").String())
	}
	a := newApp(t, context.Background())
	if err := a.run([]string{"-home", home, "address"}); err != nil {
		t.Fatal(err)
	}
	addrs = append(addrs, strings.TrimSpace(a.out.String()))
	founders := filepath.Join(home, "founders.txt")
	_ = os.WriteFile(founders, []byte("# fondateurs\n"+strings.Join(addrs, "\n")+"\n"), 0o644)
	g := newApp(t, context.Background())
	err := g.run([]string{"genesis", "-network", "test", "-date", "2026-10-20", "-founders", founders, "-ipc-value", "103070",
		"-ipc-period", "2026-Q3", "-ipc-sha256", strings.Repeat("ed", 32), "-out", filepath.Join(home, "genesis.json")})
	if err != nil || !strings.Contains(g.out.String(), "Empreinte à graver") {
		t.Fatalf("genesis : %v %s", err, g.out.String())
	}
	for name, args := range map[string][]string{
		"date":      {"genesis", "-network", "test", "-date", "20/10/2026"},
		"réseau":    {"genesis", "-network", "ETL-XX", "-date", "2026-10-20"},
		"empreinte": {"genesis", "-network", "test", "-date", "2026-10-20", "-founders", founders, "-ipc-value", "1", "-ipc-sha256", "zz"},
		"valeur":    {"genesis", "-network", "test", "-date", "2026-10-20", "-founders", founders, "-ipc-value", "x"},
	} {
		if err := newApp(t, context.Background()).run(args); err == nil {
			t.Errorf("genesis (%s invalide) accepté", name)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	r := newApp(t, ctx)
	done := make(chan error, 1)
	go func() { done <- r.run([]string{"-home", home, "run", "-passphrase-file", passFile}) }()
	var st map[string]any
	for i := 0; i < 100 && st == nil; i++ {
		req, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/api/status", dash), nil)
		req.Host = fmt.Sprintf("localhost:%d", dash)
		if resp, err := http.DefaultClient.Do(req); err == nil {
			_ = json.NewDecoder(resp.Body).Decode(&st)
			resp.Body.Close()
		}
		time.Sleep(50 * time.Millisecond)
	}
	if st == nil || st["chain_id"] != "ETL-TEST" || st["node"].(map[string]any)["address"] != addrs[3] || st["node"].(map[string]any)["status"] != "admitted" {
		t.Fatalf("statut du nœud : %v", st)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("arrêt : %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("arrêt non effectué")
	}
	if !strings.Contains(r.errw.String(), "nœud démarré") || !strings.Contains(r.errw.String(), "nœud arrêté") {
		t.Fatalf("journal : %s", r.errw.String())
	}
	if strings.Contains(r.errw.String(), pass) {
		t.Fatal("phrase de passe dans le journal")
	}
	// Mauvaise phrase de passe : refus.
	_ = os.WriteFile(passFile, []byte("une autre phrase de passe"), 0o600)
	if err := newApp(t, context.Background()).run([]string{"-home", home, "run", "-passphrase-file", passFile}); err == nil {
		t.Fatal("mauvaise phrase de passe acceptée")
	}
	// Clé configurée mais absente : message qui indique quoi faire.
	_ = os.Rename(filepath.Join(home, "wallet.key"), filepath.Join(home, "ailleurs.key"))
	if err := newApp(t, context.Background()).run([]string{"-home", home, "run", "-passphrase-file", passFile}); err == nil || !strings.Contains(err.Error(), "etl init") {
		t.Fatalf("clé absente : %v", err)
	}
}

func readFile(t *testing.T, p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRunRefusals(t *testing.T) {
	// ETL-FR : pas encore lancé dans cette version.
	home := t.TempDir()
	if err := newApp(t, context.Background()).run([]string{"-home", home, "run"}); !errors.Is(err, node.ErrNotLaunched) {
		t.Fatalf("ETL-FR : %v", err)
	}
	writeTestConfig(t, home, "")
	_ = os.WriteFile(filepath.Join(home, "genesis.json"), []byte(`{}`), 0o644)
	if err := newApp(t, context.Background()).run([]string{"-home", home, "run"}); err == nil {
		t.Fatal("genesis vide accepté")
	}
	for name, args := range map[string][]string{
		"commande inconnue": {"-home", home, "voler"},
		"drapeau inconnu":   {"-inconnu"},
		"option inconnue":   {"-home", home, "address", "-x"},
		"adresse sans clé":  {"-home", t.TempDir(), "address"},
	} {
		if err := newApp(t, context.Background()).run(args); err == nil {
			t.Errorf("%s : accepté", name)
		}
	}
	bad := t.TempDir()
	_ = os.WriteFile(filepath.Join(bad, "config.toml"), []byte("[block]\ndaily_reward = 20\n"), 0o600)
	if err := newApp(t, context.Background()).run([]string{"-home", bad, "address"}); !errors.Is(err, config.ErrConsensusKey) {
		t.Fatalf("règle de consensus dans la config : %v", err)
	}
	v := newApp(t, context.Background())
	if err := v.run([]string{"version"}); err != nil || !strings.Contains(v.out.String(), Version) {
		t.Fatal("version")
	}
}
