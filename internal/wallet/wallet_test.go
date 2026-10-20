package wallet

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/text/unicode/norm"

	"github.com/makoto-kanamoto/etl/internal/blockchain"
	"github.com/makoto-kanamoto/etl/internal/blockchain/chaintest"
	"github.com/makoto-kanamoto/etl/internal/crypto"
)

const pass = "correct horse battery staple"

func path(t *testing.T) string { return filepath.Join(t.TempDir(), "etl", "wallet.key") }

func TestCreateOpenRestore(t *testing.T) {
	p := path(t)
	w, phrase, err := Create(p, pass, French, "etl")
	if err != nil {
		t.Fatal(err)
	}
	if len(phrase) != 24 || !strings.HasPrefix(w.Address.String(), "etl1q") {
		t.Fatalf("phrase de %d mots, adresse %s", len(phrase), w.Address)
	}
	o, err := Open(p, pass, "etl")
	if err != nil || o.Address != w.Address {
		t.Fatalf("réouverture : %v", err)
	}
	if a, err := ReadAddress(p, "etl"); err != nil || a != w.Address {
		t.Fatalf("adresse sans phrase de passe : %v", err)
	}
	r, err := Restore(path(t), strings.Join(phrase, " "), "une autre phrase de passe", "etl")
	if err != nil || r.Address != w.Address || string(r.Key().Bytes()) != string(w.Key().Bytes()) {
		t.Fatalf("restauration depuis la phrase : %v", err)
	}
}

// Vecteur de référence ETL (clé privée = 1) : le wallet retrouve l'adresse de crypto.
func TestReferenceVector(t *testing.T) {
	one := make([]byte, 32)
	one[31] = 1
	words, _ := EncodePhrase(one, English)
	w, err := Restore(path(t), strings.Join(words, " "), pass, "etl")
	if err != nil {
		t.Fatal(err)
	}
	if w.Address.String() != "etl1qnwxsgqx9c4phyx8vjt6650fl5zgxzcaav8qcw0" {
		t.Fatalf("adresse %s", w.Address)
	}
}

func TestRejects(t *testing.T) {
	p := path(t)
	if _, _, err := Create(p, "trop court", English, "etl"); err != ErrPassphraseShort {
		t.Fatalf("phrase de passe courte : err = %v", err)
	}
	w, phrase, _ := Create(p, pass, English, "etl")
	if _, err := Open(p, pass+"!", "etl"); err != ErrWrongPassphrase {
		t.Fatalf("mauvaise phrase de passe : err = %v", err)
	}
	if _, _, err := Create(p, pass, English, "etl"); err != ErrExists {
		t.Fatalf("écrasement par Create : err = %v", err)
	}
	if _, err := Restore(p, strings.Join(phrase, " "), pass, "etl"); err != ErrExists {
		t.Fatalf("écrasement par Restore : err = %v", err)
	}
	if o, _ := Open(p, pass, "etl"); o == nil || o.Address != w.Address {
		t.Fatal("le coffre d'origine doit être intact")
	}
	if _, err := Open(p, pass, "etlde"); err != ErrFormat {
		t.Fatalf("coffre d'un autre réseau : err = %v", err)
	}
}

// tamper modifie le coffre comme le ferait un attaquant ayant accès au disque.
func tamper(t *testing.T, p string, f func(*keyFile)) {
	t.Helper()
	data, _ := os.ReadFile(p)
	var kf keyFile
	if err := json.Unmarshal(data, &kf); err != nil {
		t.Fatal(err)
	}
	f(&kf)
	data, _ = json.Marshal(&kf)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestTamperedVault(t *testing.T) {
	other, _ := crypto.GenerateKey()
	cases := map[string]struct {
		edit func(*keyFile)
		want error
	}{
		"adresse affichée remplacée": {func(f *keyFile) { f.Address = other.PublicKey().Address("etl").String() }, ErrWrongPassphrase},
		"sel modifié":                {func(f *keyFile) { f.KDF.Salt[0] ^= 1 }, ErrWrongPassphrase},
		"texte chiffré modifié":      {func(f *keyFile) { f.Ciphertext[3] ^= 1 }, ErrWrongPassphrase},
		"nonce modifié":              {func(f *keyFile) { f.Cipher.Nonce[0] ^= 1 }, ErrWrongPassphrase},
		"paramètres affaiblis":       {func(f *keyFile) { f.KDF.MemoryKiB = 1024; f.KDF.Time = 1 }, ErrFormat},
		"mémoire démesurée":          {func(f *keyFile) { f.KDF.MemoryKiB = 1 << 30 }, ErrFormat},
		"version inconnue":           {func(f *keyFile) { f.Version = 2 }, ErrFormat},
		"autre algorithme":           {func(f *keyFile) { f.KDF.Name = "pbkdf2" }, ErrFormat},
	}
	for name, c := range cases {
		p := path(t)
		if _, _, err := Create(p, pass, English, "etl"); err != nil {
			t.Fatal(err)
		}
		tamper(t, p, c.edit)
		if _, err := Open(p, pass, "etl"); err != c.want {
			t.Errorf("%s : err = %v, attendu %v", name, err, c.want)
		}
	}
}

func TestFilePermissionsAndAtomicity(t *testing.T) {
	p := path(t)
	if _, _, err := Create(p, pass, English, "etl"); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(p)
	di, _ := os.Stat(filepath.Dir(p))
	if fi.Mode().Perm() != 0o600 || di.Mode().Perm() != 0o700 {
		t.Fatalf("permissions : fichier %v, répertoire %v", fi.Mode().Perm(), di.Mode().Perm())
	}
	if err := ChangePassphrase(p, pass, "nouvelle phrase de passe", "etl"); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Dir(p))
	if len(entries) != 1 {
		t.Fatalf("%d fichiers dans le répertoire : fichier temporaire oublié", len(entries))
	}
	if _, err := Open(p, pass, "etl"); err != ErrWrongPassphrase {
		t.Fatal("l'ancienne phrase de passe fonctionne encore")
	}
	if _, err := Open(p, "nouvelle phrase de passe", "etl"); err != nil {
		t.Fatalf("nouvelle phrase de passe : %v", err)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o600 {
		t.Fatal("permissions perdues au changement de phrase de passe")
	}
}

func TestCloseZeroesKey(t *testing.T) {
	w, _, _ := Create(path(t), pass, English, "etl")
	k := w.Key()
	w.Close()
	if w.Key() != nil || string(k.Bytes()) != string(make([]byte, 32)) {
		t.Fatal("clé non effacée")
	}
}

// Une transaction construite par le wallet, avec les frais en vigueur, est acceptée par le consensus.
func TestTransferAcceptedByConsensus(t *testing.T) {
	net := chaintest.New(t, chaintest.Params())
	net.Next(t, 1, nil) // chaque fondateur : 1 ETL
	founder := net.Founders[0]
	words, _ := EncodePhrase(founder.Key.Bytes(), French)
	w, err := Restore(path(t), strings.Join(words, " "), pass, net.P.AddressHRP)
	if err != nil || w.Address != founder.Addr {
		t.Fatalf("restauration du fondateur : %v", err)
	}
	tx, err := w.Transfer(net.Founders[1].Addr, blockchain.UnitsPerETL/2, 0, chaintest.Day(1), net.P)
	if err != nil {
		t.Fatal(err)
	}
	net.Next(t, 2, func(b *blockchain.Block) { b.Txs = []blockchain.Transaction{tx} }) // échoue si refusée
	if net.State.Account(founder.Addr).Nonce != 1 {
		t.Fatal("transaction non appliquée")
	}
	// Frais en vigueur après un vote (0,2 %) : le wallet suit les paramètres qu'on lui donne.
	voted := net.P.With(blockchain.ParamFeeBps, 20)
	tx2, err := w.Transfer(net.Founders[1].Addr, blockchain.UnitsPerETL/2, 1, chaintest.Day(2), voted)
	if err != nil || tx2.Fee != blockchain.UnitsPerETL/2*20/10000 {
		t.Fatalf("frais en vigueur : %d, %v", tx2.Fee, err)
	}
}

// La phrase de passe est normalisée en NFC : la forme composée (« é », un caractère) et la forme
// décomposée (e + accent combinant) ouvrent le même coffre, et la longueur se compte après NFC.
func TestPassphraseNormalizedNFC(t *testing.T) {
	nfc := norm.NFC.String("phrase de passe élève été")
	nfd := norm.NFD.String(nfc)
	if nfc == nfd {
		t.Fatal("prémisse : formes NFC et NFD différentes")
	}
	p := path(t)
	w, _, err := Create(p, nfc, French, "etl")
	if err != nil {
		t.Fatal(err)
	}
	if o, err := Open(p, nfd, "etl"); err != nil || o.Address != w.Address {
		t.Fatalf("coffre NFC ouvert avec la forme NFD : %v", err)
	}
	if err := ChangePassphrase(p, nfd, norm.NFD.String("nouvelle phrase élève"), "etl"); err != nil {
		t.Fatalf("changement depuis la forme NFD : %v", err)
	}
	if _, err := Open(p, norm.NFC.String("nouvelle phrase élève"), "etl"); err != nil {
		t.Fatalf("nouvelle phrase, forme NFC : %v", err)
	}
	// 11 caractères en NFC, 15 points de code en NFD : trop court dans les deux cas.
	short := norm.NFC.String("élèveélève!")
	if len([]rune(short)) != 11 || len([]rune(norm.NFD.String(short))) != 15 {
		t.Fatal("prémisse : 11 caractères NFC, 15 en NFD")
	}
	for _, pass := range []string{short, norm.NFD.String(short)} {
		if _, _, err := Create(path(t), pass, English, "etl"); err != ErrPassphraseShort {
			t.Fatalf("phrase de passe courte (%d points de code) : err = %v", len([]rune(pass)), err)
		}
	}
}
