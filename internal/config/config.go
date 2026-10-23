// Package config lit la configuration LOCALE du nœud (fichier TOML) : ports, répertoires, pruning,
// pairs d'amorçage, limites du mempool, source de l'IPC, dashboard.
//
// Les règles de consensus (identifiant du réseau, préfixe d'adresse, récompense, difficulté, frais,
// heure du bloc…) n'y figurent PAS : elles viennent de la définition du réseau dans le logiciel et du
// genesis, puis évoluent par vote on-chain. Un opérateur ne peut pas forker sa chaîne en modifiant
// ce fichier.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config est la configuration locale.
type Config struct {
	Node struct {
		Network         string // réseau rejoint : « ETL-FR » (ou « test », réseau de test explicite)
		DataDir         string // chaîne (base bbolt)
		Wallet          string // coffre de la clé du nœud ; vide : nœud passif
		Genesis         string // fichier genesis.json publié avec la version
		Port            int    // port P2P ; 0 : pas de connexions entrantes
		Pruning         bool   // false : nœud archive
		PruneKeepBlocks int
	}
	Network struct {
		Bootstrap   []string
		MaxOutbound int
		MaxInbound  int
		BanHours    int
	}
	Mempool struct {
		MaxPerAccount int
		MaxTotal      int
		MaxAgeDays    int
	}
	Oracle struct {
		IPCURL       string
		IPCCountry   string
		IPCFrequency string
	}
	Dashboard struct {
		Port  int
		Token string // fichier du jeton opérateur (régénéré à chaque démarrage)
	}
	// Public : site public (explorateur, portefeuille) servi derrière un proxy HTTPS, à la place du
	// dashboard de l'opérateur. Nœud archive et passif seulement : aucune clé sur une machine exposée.
	Public struct {
		Enabled        bool
		Host           string // nom d'hôte du site (etalon-etl.fr), sans schéma ni port
		ReadPerMinute  int    // requêtes de lecture de l'API par adresse IP et par minute
		WritePerMinute int    // soumissions (transferts, échanges) par adresse IP et par minute
	}
}

// Default renvoie la configuration par défaut d'un nœud ETL-FR dont les fichiers sont sous home.
func Default(home string) Config {
	var c Config
	c.Node.Network = "ETL-FR"
	c.Node.DataDir = filepath.Join(home, "chain")
	c.Node.Wallet = filepath.Join(home, "wallet.key")
	c.Node.Genesis = filepath.Join(home, "genesis.json")
	c.Node.Port = 3000
	c.Node.Pruning = true
	c.Node.PruneKeepBlocks = 90
	c.Network.Bootstrap = []string{"etl1.etalon-etl.fr:3000"}
	c.Network.MaxOutbound, c.Network.MaxInbound, c.Network.BanHours = 8, 64, 24
	c.Mempool.MaxPerAccount, c.Mempool.MaxTotal, c.Mempool.MaxAgeDays = 16, 200_000, 7
	c.Oracle.IPCURL = "https://api.insee.fr/series/BDM/V1/data/SERIES_BDM/011814630"
	c.Oracle.IPCCountry = "FR"
	c.Oracle.IPCFrequency = "quarterly"
	c.Dashboard.Port = 3001
	c.Dashboard.Token = filepath.Join(home, "dashboard.token")
	c.Public.ReadPerMinute, c.Public.WritePerMinute = 300, 6
	return c
}

// field associe une clé du fichier à un champ de Config.
type field struct {
	section, key string
	str          *string
	num          *int
	flag         *bool
	list         *[]string
	path         bool // chemin : « ~ » développé
}

func (c *Config) fields() []field {
	return []field{
		{section: "node", key: "network", str: &c.Node.Network},
		{section: "node", key: "datadir", str: &c.Node.DataDir, path: true},
		{section: "node", key: "wallet", str: &c.Node.Wallet, path: true},
		{section: "node", key: "genesis", str: &c.Node.Genesis, path: true},
		{section: "node", key: "port", num: &c.Node.Port},
		{section: "node", key: "pruning", flag: &c.Node.Pruning},
		{section: "node", key: "prune_keep_blocks", num: &c.Node.PruneKeepBlocks},
		{section: "network", key: "bootstrap", list: &c.Network.Bootstrap},
		{section: "network", key: "max_outbound", num: &c.Network.MaxOutbound},
		{section: "network", key: "max_inbound", num: &c.Network.MaxInbound},
		{section: "network", key: "ban_hours", num: &c.Network.BanHours},
		{section: "mempool", key: "max_per_account", num: &c.Mempool.MaxPerAccount},
		{section: "mempool", key: "max_total", num: &c.Mempool.MaxTotal},
		{section: "mempool", key: "max_age_days", num: &c.Mempool.MaxAgeDays},
		{section: "oracle", key: "ipc_url", str: &c.Oracle.IPCURL},
		{section: "oracle", key: "ipc_country", str: &c.Oracle.IPCCountry},
		{section: "oracle", key: "ipc_frequency", str: &c.Oracle.IPCFrequency},
		{section: "dashboard", key: "port", num: &c.Dashboard.Port},
		{section: "dashboard", key: "token", str: &c.Dashboard.Token, path: true},
		{section: "public", key: "enabled", flag: &c.Public.Enabled},
		{section: "public", key: "host", str: &c.Public.Host},
		{section: "public", key: "read_per_minute", num: &c.Public.ReadPerMinute},
		{section: "public", key: "write_per_minute", num: &c.Public.WritePerMinute},
	}
}

// consensusKeys : anciennes clés de consensus, refusées avec une explication (M3).
var consensusKeys = map[string]bool{
	"network.chain_id": true, "network.address_hrp": true, "block.time_utc": true, "block.daily_reward": true,
	"block.target_bits": true, "transaction.fee_bps": true, "transaction.fee_min": true, "transaction.max_block_txs": true,
}

// ErrConsensusKey : une règle de consensus figure dans la configuration locale.
var ErrConsensusKey = errors.New("config : les règles de consensus ne se règlent pas ici (définition du réseau, genesis, puis votes on-chain)")

// Parse lit un fichier de configuration par-dessus Default(home). Toute clé inconnue est une erreur.
func Parse(src, home string) (Config, error) {
	c := Default(home)
	doc, err := parse(src)
	if err != nil {
		return c, err
	}
	known := map[string]field{}
	for _, f := range c.fields() {
		known[f.section+"."+f.key] = f
	}
	for section, kv := range doc {
		for key, v := range kv {
			name := section + "." + key
			if consensusKeys[name] || section == "block" || section == "transaction" {
				return c, fmt.Errorf("%w : %s (ligne %d)", ErrConsensusKey, name, v.line)
			}
			f, ok := known[name]
			if !ok {
				return c, &ParseError{v.line, "clé inconnue : " + name}
			}
			if err := assign(f, v); err != nil {
				return c, &ParseError{v.line, name + " : " + err.Error()}
			}
		}
	}
	return c, c.check()
}

func assign(f field, v value) error {
	switch {
	case f.str != nil:
		s, ok := v.v.(string)
		if !ok {
			return errors.New("chaîne attendue")
		}
		if f.path {
			s = expand(s)
		}
		*f.str = s
	case f.num != nil:
		n, ok := v.v.(int64)
		if !ok || n < 0 || n > 1<<31 {
			return errors.New("entier positif attendu")
		}
		*f.num = int(n)
	case f.flag != nil:
		b, ok := v.v.(bool)
		if !ok {
			return errors.New("true ou false attendu")
		}
		*f.flag = b
	case f.list != nil:
		l, ok := v.v.([]string)
		if !ok {
			return errors.New("liste de chaînes attendue")
		}
		*f.list = l
	}
	return nil
}

// expand remplace « ~/ » par le répertoire de l'utilisateur.
func expand(s string) string {
	if s == "~" || strings.HasPrefix(s, "~/") {
		if u, err := os.UserHomeDir(); err == nil {
			return filepath.Join(u, strings.TrimPrefix(s, "~"))
		}
	}
	return s
}

func (c *Config) check() error {
	switch {
	case c.Node.Network == "":
		return errors.New("config : node.network vide")
	case c.Node.Port > 65535 || c.Dashboard.Port == 0 || c.Dashboard.Port > 65535:
		return errors.New("config : port hors plage")
	case c.Node.Port != 0 && c.Node.Port == c.Dashboard.Port:
		return errors.New("config : node.port et dashboard.port doivent différer")
	case c.Node.DataDir == "" || c.Node.Genesis == "" || c.Dashboard.Token == "":
		return errors.New("config : chemin vide")
	case c.Node.Pruning && c.Node.PruneKeepBlocks < 2:
		return errors.New("config : node.prune_keep_blocks trop petit")
	case c.Network.MaxOutbound == 0 && len(c.Network.Bootstrap) > 0:
		return errors.New("config : network.max_outbound nul alors que des pairs d'amorçage sont donnés")
	}
	if c.Public.Enabled {
		switch {
		case !validHost(c.Public.Host):
			return errors.New("config : public.host invalide (nom d'hôte seul, par exemple etalon-etl.fr)")
		case c.Node.Wallet != "":
			return errors.New("config : site public : nœud passif seulement (wallet = \"\"), aucune clé sur une machine exposée")
		case c.Node.Pruning:
			return errors.New("config : site public : nœud archive seulement (pruning = false)")
		case c.Public.ReadPerMinute == 0 || c.Public.WritePerMinute == 0:
			return errors.New("config : public.read_per_minute et public.write_per_minute doivent être positifs")
		}
	}
	return nil
}

// validHost : nom d'hôte DNS en minuscules (lettres, chiffres, tirets, au moins un point).
func validHost(h string) bool {
	if len(h) == 0 || len(h) > 253 || !strings.Contains(h, ".") {
		return false
	}
	for _, label := range strings.Split(h, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				return false
			}
		}
	}
	return true
}

// Load lit le fichier path (absent : configuration par défaut).
func Load(path, home string) (Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Default(home), nil
	}
	if err != nil {
		return Config{}, err
	}
	return Parse(string(data), home)
}
