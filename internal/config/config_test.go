package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Le fichier livré (config/default.toml) donne exactement la configuration par défaut.
func TestShippedFileIsDefault(t *testing.T) {
	data, err := os.ReadFile("../../config/default.toml")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := os.UserHomeDir()
	home := filepath.Join(u, ".etl")
	got, err := Parse(string(data), home)
	if err != nil {
		t.Fatal(err)
	}
	if want := Default(home); !reflect.DeepEqual(got, want) {
		t.Fatalf("fichier livré ≠ défaut :\n%+v\n%+v", got, want)
	}
}

func TestParseValues(t *testing.T) {
	src := `
# commentaire
[node]
network = "test"   # réseau de test
datadir = "/tmp/a#b"
wallet = ""
port = 0
pruning = false
prune_keep_blocks = 1_000

[network]
bootstrap = [
  "a.example:3000",  # premier
  "10.0.0.2:3000",
]
max_outbound = 4

[dashboard]
port = 4001
token = "~/jeton"
`
	c, err := Parse(src, "/home/x/.etl")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := os.UserHomeDir()
	switch {
	case c.Node.Network != "test", c.Node.DataDir != "/tmp/a#b", c.Node.Wallet != "", c.Node.Port != 0, c.Node.Pruning,
		c.Node.PruneKeepBlocks != 1000, !reflect.DeepEqual(c.Network.Bootstrap, []string{"a.example:3000", "10.0.0.2:3000"}),
		c.Network.MaxOutbound != 4, c.Network.MaxInbound != 64, c.Dashboard.Port != 4001, c.Dashboard.Token != filepath.Join(u, "jeton"),
		c.Node.Genesis != "/home/x/.etl/genesis.json":
		t.Fatalf("%+v", c)
	}
	if s, err := parseValue(`"a\"b\\c\nd\te"`); err != nil || s != "a\"b\\c\nd\te" {
		t.Fatalf("échappements : %q %v", s, err)
	}
}

func TestParseRejects(t *testing.T) {
	for name, c := range map[string]struct {
		src  string
		line int
	}{
		"clé inconnue":         {"[node]\nport = 1\nprot = 2", 3},
		"section inconnue":     {"[nodes]\nport = 1", 2},
		"clé en double":        {"[node]\nport = 1\nport = 2", 3},
		"section en double":    {"[node]\n[node]", 2},
		"type : chaîne":        {"[node]\nport = \"3000\"", 2},
		"type : entier":        {"[node]\nnetwork = 3", 2},
		"type : booléen":       {"[node]\npruning = 1", 2},
		"type : liste":         {"[network]\nbootstrap = \"a\"", 2},
		"entier négatif":       {"[node]\nport = -1", 2},
		"flottant":             {"[mempool]\nmax_total = 1.5", 2},
		"chaîne non fermée":    {"[node]\nnetwork = \"ETL", 2},
		"échappement inconnu":  {"[node]\nnetwork = \"a\\x\"", 2},
		"texte après chaîne":   {"[node]\nnetwork = \"a\" b", 2},
		"liste non fermée":     {"[network]\nbootstrap = [\n\"a\"", 2},
		"liste de nombres":     {"[network]\nbootstrap = [1]", 2},
		"virgule manquante":    {"[network]\nbootstrap = [\"a\" \"b\"]", 2},
		"séparateur invalide":  {"[network]\nbootstrap = [\"a\" x \"b\"]", 2},
		"en-tête invalide":     {"[Node]", 1},
		"en-tête imbriqué":     {"[node.sub]", 1},
		"sans valeur":          {"[node]\nport =", 2},
		"sans égal":            {"[node]\nport 3000", 2},
		"souligné final":       {"[node]\nport = 3000_", 2},
		"contrôle dans chaîne": {"[node]\nnetwork = \"a\tb\"", 2},
	} {
		_, err := Parse(c.src, "/h")
		var pe *ParseError
		if !errors.As(err, &pe) || pe.Line != c.line {
			t.Errorf("%s : err = %v (ligne attendue %d)", name, err, c.line)
		}
	}
}

// Règles de consensus : refusées avec une explication (elles ne se règlent pas localement).
func TestConsensusKeysRefused(t *testing.T) {
	for _, src := range []string{
		"[network]\nchain_id = \"ETL-DE\"",
		"[network]\naddress_hrp = \"x\"",
		"[block]\ndaily_reward = 20",
		"[block]\ntarget_bits = 16",
		"[transaction]\nfee_bps = 20",
		"[block]\ntime_utc = \"22:00\"",
	} {
		if _, err := Parse(src, "/h"); !errors.Is(err, ErrConsensusKey) {
			t.Errorf("%q : err = %v", src, err)
		}
	}
}

func TestCheck(t *testing.T) {
	for name, src := range map[string]string{
		"ports identiques":     "[node]\nport = 3001",
		"port trop grand":      "[node]\nport = 70000",
		"dashboard port nul":   "[dashboard]\nport = 0",
		"réseau vide":          "[node]\nnetwork = \"\"",
		"élagage trop court":   "[node]\nprune_keep_blocks = 1",
		"amorçage sans sortie": "[network]\nmax_outbound = 0",
		"chemin vide":          "[node]\ndatadir = \"\"",
	} {
		if _, err := Parse(src, "/h"); err == nil || strings.Contains(err.Error(), "ligne") {
			t.Errorf("%s : err = %v", name, err)
		}
	}
	if _, err := Parse("[node]\npruning = false\nprune_keep_blocks = 0", "/h"); err != nil {
		t.Fatalf("archive sans élagage : %v", err)
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	if c, err := Load(filepath.Join(dir, "absent.toml"), dir); err != nil || c.Node.DataDir != filepath.Join(dir, "chain") {
		t.Fatalf("fichier absent : %v", err)
	}
	p := filepath.Join(dir, "c.toml")
	_ = os.WriteFile(p, []byte("[node]\nport = 4000"), 0o600)
	if c, err := Load(p, dir); err != nil || c.Node.Port != 4000 {
		t.Fatalf("lecture : %v", err)
	}
	_ = os.WriteFile(p, []byte("[node]\nport = x"), 0o600)
	if _, err := Load(p, dir); err == nil {
		t.Fatal("fichier invalide accepté")
	}
	if _, err := Load(dir, dir); err == nil {
		t.Fatal("répertoire lu comme fichier")
	}
}

// Fuzzing : jamais de panique ; toute configuration acceptée est cohérente.
func FuzzParse(f *testing.F) {
	data, _ := os.ReadFile("../../config/default.toml")
	f.Add(string(data))
	f.Add("[network]\nbootstrap = [\"a\", \"b\",]\n")
	f.Add("[node]\nnetwork = \"a\\\"#b\" # c\n")
	f.Fuzz(func(t *testing.T, src string) {
		c, err := Parse(src, "/h")
		if err == nil && c.check() != nil {
			t.Fatal("configuration incohérente acceptée")
		}
	})
}
