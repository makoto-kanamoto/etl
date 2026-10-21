// Commande etl : le nœud Étalon.
package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"golang.org/x/term"

	configfile "github.com/makoto-kanamoto/etl/config"
	"github.com/makoto-kanamoto/etl/web"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a := &app{out: os.Stdout, errw: os.Stderr, static: web.FS, ctx: ctx, secret: readSecret}
	if err := a.run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "erreur :", err)
		os.Exit(1)
	}
}

// readSecret lit une saisie sans l'afficher (terminal). Hors terminal (tube), refuse : utiliser
// -passphrase-file, pour qu'aucun secret ne transite par une variable ou un argument.
func readSecret(prompt string) (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", errors.New("pas de terminal pour la saisie : utilisez -passphrase-file")
	}
	fmt.Fprint(os.Stderr, prompt)
	b, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	return strings.TrimRight(string(b), "\r\n"), err
}

func parseHex32(s string) ([32]byte, error) {
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 32 {
		return [32]byte{}, errors.New("empreinte : 64 caractères hexadécimaux attendus")
	}
	return [32]byte(b), nil
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// defaultConfig : le fichier de configuration écrit par « etl init », chemins sous home.
func defaultConfig(home string) string {
	return strings.ReplaceAll(configfile.Default, "~/.etl", home)
}
