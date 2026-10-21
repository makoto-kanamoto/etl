package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/makoto-kanamoto/etl/internal/blockchain"
	"github.com/makoto-kanamoto/etl/internal/config"
	"github.com/makoto-kanamoto/etl/internal/crypto"
	"github.com/makoto-kanamoto/etl/internal/node"
	"github.com/makoto-kanamoto/etl/internal/wallet"
)

// Version du logiciel.
const Version = "0.1.0-dev"

// app regroupe les entrées-sorties des commandes (injectées dans les tests).
type app struct {
	out    io.Writer
	errw   io.Writer
	secret func(prompt string) (string, error) // saisie sans affichage (terminal)
	static fs.FS
	ctx    context.Context
}

const usage = `Étalon (ETL) — nœud

Usage : etl [-home RÉPERTOIRE] [-config FICHIER] <commande> [options]

Commandes :
  init        crée la configuration et la clé du nœud (phrase de récupération de 24 mots)
  restore     recrée la clé du nœud à partir de sa phrase de récupération
  run         démarre le nœud (commande par défaut)
  address     affiche l'adresse du nœud
  passphrase  change la phrase de passe de la clé du nœud
  genesis     prépare le genesis d'un réseau (cérémonie de lancement)
  version     affiche la version

Phrase de passe : saisie au clavier sans affichage, ou -passphrase-file FICHIER (permissions 0600)
pour un serveur sans surveillance. Jamais en variable d'environnement ni en argument.
`

// run exécute la ligne de commande args (sans le nom du programme).
func (a *app) run(args []string) error {
	fl := flag.NewFlagSet("etl", flag.ContinueOnError)
	fl.SetOutput(a.errw)
	fl.Usage = func() { fmt.Fprint(a.errw, usage) }
	userHome, _ := os.UserHomeDir()
	home := fl.String("home", filepath.Join(userHome, ".etl"), "répertoire des fichiers du nœud")
	cfgPath := fl.String("config", "", "fichier de configuration (défaut : HOME/config.toml)")
	if err := fl.Parse(args); err != nil {
		return err
	}
	if *cfgPath == "" {
		*cfgPath = filepath.Join(*home, "config.toml")
	}
	cmd, rest := "run", fl.Args()
	if len(rest) > 0 {
		cmd, rest = rest[0], rest[1:]
	}
	switch cmd {
	case "version":
		fmt.Fprintln(a.out, "etl", Version)
		return nil
	case "genesis":
		return a.genesis(rest)
	}
	cfg, err := config.Load(*cfgPath, *home)
	if err != nil {
		return err
	}
	net, err := node.Lookup(cfg.Node.Network)
	if err != nil {
		return err
	}
	sub := flag.NewFlagSet(cmd, flag.ContinueOnError)
	sub.SetOutput(a.errw)
	passFile := sub.String("passphrase-file", "", "fichier contenant la phrase de passe (0600)")
	lang := sub.String("lang", "fr", "langue de la phrase de récupération : fr ou en")
	if err := sub.Parse(rest); err != nil {
		return err
	}
	switch cmd {
	case "init":
		return a.initNode(cfg, *cfgPath, *home, net, *lang, *passFile)
	case "restore":
		return a.restore(cfg, net, *passFile)
	case "address":
		addr, err := wallet.ReadAddress(cfg.Node.Wallet, net.Params.AddressHRP)
		if err != nil {
			return err
		}
		fmt.Fprintln(a.out, addr)
		return nil
	case "passphrase":
		old, err := a.secret("Phrase de passe actuelle : ")
		if err != nil {
			return err
		}
		next, err := a.newPassphrase()
		if err != nil {
			return err
		}
		if err := wallet.ChangePassphrase(cfg.Node.Wallet, old, next, net.Params.AddressHRP); err != nil {
			return err
		}
		fmt.Fprintln(a.out, "Phrase de passe changée.")
		return nil
	case "run":
		return a.runNode(cfg, net, *passFile)
	}
	fmt.Fprint(a.errw, usage)
	return fmt.Errorf("commande inconnue : %s", cmd)
}

// passphrase lit la phrase de passe : fichier (0600 exigé) ou saisie sans affichage.
func (a *app) passphrase(file, prompt string) (string, error) {
	if file == "" {
		return a.secret(prompt)
	}
	fi, err := os.Stat(file)
	if err != nil {
		return "", err
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("%s : permissions trop ouvertes (%v) : chmod 600", file, fi.Mode().Perm())
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(data), "\r\n"), nil
}

// newPassphrase demande une nouvelle phrase de passe deux fois.
func (a *app) newPassphrase() (string, error) {
	p1, err := a.secret(fmt.Sprintf("Nouvelle phrase de passe (%d caractères au moins) : ", wallet.MinPassphrase))
	if err != nil {
		return "", err
	}
	p2, err := a.secret("Confirmez : ")
	if err != nil {
		return "", err
	}
	if p1 != p2 {
		return "", errors.New("les deux saisies diffèrent")
	}
	return p1, nil
}

const phraseWarning = `ATTENTION : ces 24 mots sont la seule sauvegarde de la clé du nœud. Notez-les sur papier, dans
l'ordre, et gardez-les en lieu sûr. Ils ne sont PAS compatibles avec les portefeuilles Bitcoin ou
Ethereum : ne les saisissez jamais ailleurs que dans Étalon.`

func (a *app) initNode(cfg config.Config, cfgPath, home string, net node.Network, lang, passFile string) error {
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	if _, err := os.Stat(cfgPath); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(cfgPath, []byte(defaultConfig(home)), 0o600); err != nil {
			return err
		}
		fmt.Fprintln(a.out, "Configuration créée :", cfgPath)
	}
	l := wallet.French
	if lang == "en" {
		l = wallet.English
	} else if lang != "fr" {
		return errors.New("langue : fr ou en")
	}
	var pass string
	var err error
	if passFile != "" {
		pass, err = a.passphrase(passFile, "")
	} else {
		pass, err = a.newPassphrase()
	}
	if err != nil {
		return err
	}
	w, words, err := wallet.Create(cfg.Node.Wallet, pass, l, net.Params.AddressHRP)
	if err != nil {
		return err
	}
	defer w.Close()
	fmt.Fprintf(a.out, "\nClé du nœud créée : %s\nAdresse : %s\n\n%s\n\n", cfg.Node.Wallet, w.Address, phraseWarning)
	for i, word := range words {
		fmt.Fprintf(a.out, "%2d. %-12s", i+1, word)
		if (i+1)%4 == 0 {
			fmt.Fprintln(a.out)
		}
	}
	fmt.Fprintln(a.out)
	return nil
}

func (a *app) restore(cfg config.Config, net node.Network, passFile string) error {
	phrase, err := a.secret("Phrase de récupération (24 mots) : ")
	if err != nil {
		return err
	}
	var pass string
	if passFile != "" {
		pass, err = a.passphrase(passFile, "")
	} else {
		pass, err = a.newPassphrase()
	}
	if err != nil {
		return err
	}
	w, err := wallet.Restore(cfg.Node.Wallet, phrase, pass, net.Params.AddressHRP)
	if err != nil {
		return err
	}
	defer w.Close()
	fmt.Fprintf(a.out, "Clé du nœud restaurée : %s\nAdresse : %s\n", cfg.Node.Wallet, w.Address)
	return nil
}

func (a *app) runNode(cfg config.Config, net node.Network, passFile string) error {
	genesis, err := node.LoadGenesis(cfg.Node.Genesis, net)
	if err != nil {
		return err
	}
	var key *crypto.PrivateKey
	if cfg.Node.Wallet != "" {
		if _, err := os.Stat(cfg.Node.Wallet); err != nil {
			return fmt.Errorf("clé du nœud introuvable (%s) : « etl init », « etl restore », ou wallet = \"\" pour un nœud passif", cfg.Node.Wallet)
		}
		pass, err := a.passphrase(passFile, "Phrase de passe de la clé du nœud : ")
		if err != nil {
			return err
		}
		w, err := wallet.Open(cfg.Node.Wallet, pass, net.Params.AddressHRP)
		if err != nil {
			return err
		}
		defer w.Close()
		key = w.Key()
	}
	log := slog.New(slog.NewTextHandler(a.errw, &slog.HandlerOptions{Level: slog.LevelInfo}))
	n, err := node.Start(a.ctx, node.Options{Config: cfg, Network: net, Genesis: genesis, Key: key, Static: a.static,
		Version: Version, Log: log})
	if err != nil {
		return err
	}
	<-a.ctx.Done()
	n.Wait()
	return nil
}

// genesis : etl genesis -network NOM -date AAAA-MM-JJ -founders FICHIER -ipc-value N -ipc-period AAAA-Qn -ipc-sha256 HEX -out FICHIER
func (a *app) genesis(args []string) error {
	fl := flag.NewFlagSet("genesis", flag.ContinueOnError)
	fl.SetOutput(a.errw)
	netName := fl.String("network", "ETL-FR", "réseau")
	date := fl.String("date", "", "jour de lancement (AAAA-MM-JJ), bloc à 23:00 UTC")
	foundersPath := fl.String("founders", "", "fichier : une adresse de fondateur par ligne")
	ipcValue := fl.String("ipc-value", "", "IPC de référence × 1000 (103.07 → 103070)")
	ipcPeriod := fl.String("ipc-period", "", "période de l'IPC de référence (AAAA-Qn)")
	ipcSHA := fl.String("ipc-sha256", "", "empreinte de l'élément <Obs> de la source")
	out := fl.String("out", "genesis.json", "fichier produit")
	if err := fl.Parse(args); err != nil {
		return err
	}
	net, err := node.Lookup(*netName)
	if err != nil {
		return err
	}
	day, err := time.Parse("2006-01-02", *date)
	if err != nil {
		return fmt.Errorf("date : %w", err)
	}
	data, err := os.ReadFile(*foundersPath)
	if err != nil {
		return err
	}
	var founders []string
	for _, l := range strings.Split(string(data), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			founders = append(founders, l)
		}
	}
	value, err := strconv.ParseUint(*ipcValue, 10, 64)
	if err != nil {
		return fmt.Errorf("ipc-value : %w", err)
	}
	var sum [32]byte
	if raw, err := parseHex32(*ipcSHA); err != nil {
		return err
	} else {
		sum = raw
	}
	g, hash, err := node.NewGenesisFile(net, day, blockchain.IPCRecord{Value: value, Period: *ipcPeriod, SourceSHA256: sum}, founders)
	if err != nil {
		return err
	}
	if err := writeJSON(*out, g); err != nil {
		return err
	}
	fmt.Fprintf(a.out, "Genesis écrit : %s\nEmpreinte à graver dans le logiciel (internal/node/networks.go) : %s\n", *out, hash)
	return nil
}
