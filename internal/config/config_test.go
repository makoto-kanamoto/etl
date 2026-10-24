package config

import (
	"errors"
	"fmt"
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

// Site public : nœud archive et passif seulement, nom d'hôte seul, limites positives.
func TestPublic(t *testing.T) {
	const ok = "[node]\nwallet = \"\"\npruning = false\n[public]\nenabled = true\nhost = \"etalon-etl.fr\"\n"
	c, err := Parse(ok+"read_per_minute = 120\nwrite_per_minute = 2", "/h")
	if err != nil || !c.Public.Enabled || c.Public.Host != "etalon-etl.fr" || c.Public.ReadPerMinute != 120 || c.Public.WritePerMinute != 2 {
		t.Fatalf("site public : %+v %v", c.Public, err)
	}
	if d := Default("/h"); d.Public.Enabled || d.Public.ReadPerMinute != 300 || d.Public.WritePerMinute != 6 {
		t.Fatalf("défaut : %+v", d.Public)
	}
	for name, src := range map[string]string{
		"avec clé":         strings.Replace(ok, "wallet = \"\"\n", "", 1),
		"élagué":           strings.Replace(ok, "pruning = false\n", "", 1),
		"sans hôte":        strings.Replace(ok, "host = \"etalon-etl.fr\"\n", "", 1),
		"URL pour hôte":    strings.Replace(ok, "etalon-etl.fr", "https://etalon-etl.fr", 1),
		"lectures nulles":  ok + "read_per_minute = 0",
		"écritures nulles": ok + "write_per_minute = 0",
	} {
		if _, err := Parse(src, "/h"); err == nil {
			t.Errorf("%s : accepté", name)
		}
	}
	for _, h := range []string{"https://etalon-etl.fr", "etalon-etl.fr:443", "etalon-etl.fr/", "Etalon-etl.fr", "localhost",
		"-a.fr", "a-.fr", "a..fr", ".fr", "fr.", "é.fr", "a_b.fr", strings.Repeat("a", 64) + ".fr", strings.Repeat("a.", 127) + "fr"} {
		if validHost(h) {
			t.Errorf("hôte %q accepté", h)
		}
	}
	for _, h := range []string{"etalon-etl.fr", "www.etalon-etl.fr", "a1.b2.c3", strings.Repeat("a", 63) + ".fr"} {
		if !validHost(h) {
			t.Errorf("hôte %q refusé", h)
		}
	}
	// Désactivé : les autres clés de la section sont ignorées par les contrôles.
	if _, err := Parse("[public]\nhost = \"\"\nread_per_minute = 0", "/h"); err != nil {
		t.Fatalf("section désactivée : %v", err)
	}
}

// Configuration livrée pour le serveur du site public (deploy/config.toml) : valide une fois le nom du
// site renseigné ; nœud archive, passif, site public activé, chemins du serveur.
func TestDeployConfig(t *testing.T) {
	data, err := os.ReadFile("../../deploy/config.toml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(string(data), "/h"); err == nil {
		t.Fatal("espace réservé <HOSTNAME> accepté tel quel")
	}
	c, err := Parse(strings.ReplaceAll(string(data), "<HOSTNAME>", "etalon-etl.fr"), "/h")
	if err != nil {
		t.Fatal(err)
	}
	if !c.Public.Enabled || c.Public.Host != "etalon-etl.fr" || c.Node.Wallet != "" || c.Node.Pruning ||
		c.Node.Network != "ETL-FR" || c.Dashboard.Port != 3001 || c.Node.Port != 3000 {
		t.Fatalf("configuration du serveur : %+v", c)
	}
	for _, p := range []string{c.Node.DataDir, c.Node.Genesis, c.Dashboard.Token} {
		if !strings.HasPrefix(p, "/var/lib/etl/") {
			t.Errorf("chemin hors de /var/lib/etl : %s", p)
		}
	}
	// Le reste est la configuration par défaut : mêmes limites, même source de l'IPC, mêmes pairs.
	d := Default("/h")
	if c.Network.Bootstrap[0] != d.Network.Bootstrap[0] || c.Mempool != d.Mempool || c.Oracle != d.Oracle || c.Public.ReadPerMinute != d.Public.ReadPerMinute {
		t.Fatalf("écart avec la configuration par défaut : %+v", c)
	}
	for _, f := range []string{"../../deploy/Caddyfile", "../../deploy/etl.service"} {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), "<HOSTNAME>") {
			t.Errorf("%s : espace réservé <HOSTNAME> absent", f)
		}
	}
	unit, _ := os.ReadFile("../../deploy/etl.service")
	if !strings.Contains(string(unit), "-config /etc/etl/config.toml run") || !strings.Contains(string(unit), "ReadWritePaths=/var/lib/etl") {
		t.Fatal("service : configuration ou répertoire de données inattendus")
	}
	caddy, _ := os.ReadFile("../../deploy/Caddyfile")
	if !strings.Contains(string(caddy), fmt.Sprintf("reverse_proxy 127.0.0.1:%d", c.Dashboard.Port)) {
		t.Fatal("Caddyfile : le proxy ne vise pas le port du nœud")
	}
}
