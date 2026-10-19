package crypto

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// N, l'ordre de la courbe secp256k1.
var curveOrder = mustHex("fffffffffffffffffffffffffffffffebaaedce6af48a03bbfd25e8cd0364141")

func mustHex(s string) []byte {
	b, err := hexDecode(s)
	if err != nil {
		panic(err)
	}
	return b
}

func TestGenerateKeyRoundTrip(t *testing.T) {
	k, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	b := k.Bytes()
	if len(b) != PrivateKeySize {
		t.Fatalf("taille = %d", len(b))
	}
	k2, err := PrivateKeyFromBytes(b)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(k.PublicKey().Bytes(), k2.PublicKey().Bytes()) {
		t.Error("la clé rechargée donne une autre clé publique")
	}
}

func TestGenerateKeyIsRandom(t *testing.T) {
	a, _ := GenerateKey()
	b, _ := GenerateKey()
	if bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Fatal("deux clés générées identiques")
	}
}

func TestPrivateKeyFromBytesRejects(t *testing.T) {
	nPlus1 := append([]byte(nil), curveOrder...)
	nPlus1[31]++
	cases := map[string][]byte{
		"vide":        nil,
		"31 octets":   make([]byte, 31),
		"33 octets":   make([]byte, 33),
		"zéro":        make([]byte, 32),
		"égale à N":   curveOrder,
		"N + 1":       nPlus1,
		"tout à 0xff": bytes.Repeat([]byte{0xff}, 32),
	}
	for name, b := range cases {
		if _, err := PrivateKeyFromBytes(b); err != ErrInvalidPrivateKey {
			t.Errorf("%s : err = %v, attendu ErrInvalidPrivateKey", name, err)
		}
	}
}

func TestPrivateKeyNeverPrinted(t *testing.T) {
	k, _ := PrivateKeyFromBytes(mustHex("0000000000000000000000000000000000000000000000000000000000000001"))
	secret := fmt.Sprintf("%x", k.Bytes())
	wrapper := struct{ Key *PrivateKey }{k}
	for _, s := range []string{
		fmt.Sprint(k), fmt.Sprintf("%v", k), fmt.Sprintf("%+v", k), fmt.Sprintf("%#v", k),
		fmt.Sprintf("%v", wrapper), fmt.Sprintf("%+v", wrapper),
	} {
		if strings.Contains(s, secret) || !strings.Contains(s, "***") {
			t.Errorf("affichage non masqué : %q", s)
		}
	}
	if _, err := json.Marshal(wrapper); err == nil {
		t.Error("json.Marshal d'une clé privée doit échouer")
	}
}

func TestPublicKeyParse(t *testing.T) {
	k, _ := GenerateKey()
	b := k.PublicKey().Bytes()
	if len(b) != PublicKeySize || (b[0] != 0x02 && b[0] != 0x03) {
		t.Fatalf("clé publique non compressée : %x", b)
	}
	p, err := ParsePublicKey(b)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(p.Bytes(), b) {
		t.Error("aller-retour différent")
	}
}

func TestPublicKeyParseRejects(t *testing.T) {
	k, _ := GenerateKey()
	uncompressed := k.PublicKey().k.SerializeUncompressed()
	notOnCurve := append([]byte{0x02}, bytes.Repeat([]byte{0xff}, 32)...)
	for name, b := range map[string][]byte{
		"non compressée":  uncompressed,
		"hors courbe":     notOnCurve,
		"vide":            nil,
		"mauvais préfixe": append([]byte{0x05}, k.PublicKey().Bytes()[1:]...),
	} {
		if _, err := ParsePublicKey(b); err != ErrInvalidPublicKey {
			t.Errorf("%s : err = %v", name, err)
		}
	}
}

func TestZero(t *testing.T) {
	k, _ := GenerateKey()
	k.Zero()
	if !bytes.Equal(k.Bytes(), make([]byte, 32)) {
		t.Error("Zero n'a pas effacé la clé")
	}
}
