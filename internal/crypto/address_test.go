package crypto

import (
	"strings"
	"testing"
)

func TestAddressRoundTrip(t *testing.T) {
	k, _ := GenerateKey()
	a := k.PublicKey().Address("etl")
	s := a.String()
	if !strings.HasPrefix(s, "etl1q") {
		t.Fatalf("préfixe inattendu : %s", s)
	}
	if len(s) != 43 { // "etl" + "1" + 1 version + 32 données + 6 checksum
		t.Errorf("longueur = %d : %s", len(s), s)
	}
	got, err := ParseAddress(s, "etl")
	if err != nil || got != a {
		t.Fatalf("aller-retour : %v, %v", got, err)
	}
	upper, err := ParseAddress(strings.ToUpper(s), "etl")
	if err != nil || upper != a {
		t.Errorf("majuscules refusées : %v", err)
	}
}

func TestAddressPerFork(t *testing.T) {
	k, _ := GenerateKey()
	fr := k.PublicKey().Address("etl").String()
	de := k.PublicKey().Address("etlde").String()
	if _, err := ParseAddress(de, "etl"); err != ErrInvalidAddress {
		t.Error("adresse ETL-DE acceptée sur ETL-FR")
	}
	if _, err := ParseAddress(fr, "etlde"); err != ErrInvalidAddress {
		t.Error("adresse ETL-FR acceptée sur ETL-DE")
	}
}

func TestParseAddressRejects(t *testing.T) {
	k, _ := GenerateKey()
	s := k.PublicKey().Address("etl").String()

	typo := []byte(s)
	if typo[10] == 'q' {
		typo[10] = 'p'
	} else {
		typo[10] = 'q'
	}
	mixed := s[:5] + strings.ToUpper(s[5:10]) + s[10:]

	var a Address
	a.HRP = "etl"
	data, _ := convertBits(a.Hash[:], 8, 5, true)
	v1, _ := bech32mEncode("etl", append([]byte{1}, data...))
	short, _ := bech32mEncode("etl", append([]byte{0}, data[:30]...))

	for name, in := range map[string]string{
		"faute de frappe":  string(typo),
		"casse mixte":      mixed,
		"version 1":        v1,
		"corps trop court": short,
		"vide":             "",
		"ancien exemple":   "ETL1xK9f3mP2qR8vL7nT4uW6sA5dF1gH3jK",
	} {
		if _, err := ParseAddress(in, "etl"); err != ErrInvalidAddress {
			t.Errorf("%s (%q) : accepté à tort", name, in)
		}
	}
}

// Vecteur de référence ETL, figé : sert aussi au futur wallet JavaScript,
// qui doit produire exactement la même adresse et la même signature.
func TestReferenceVector(t *testing.T) {
	k, err := PrivateKeyFromBytes(mustHex("0000000000000000000000000000000000000000000000000000000000000001"))
	if err != nil {
		t.Fatal(err)
	}
	pub := k.PublicKey()
	if got := hexString(pub.Bytes()); got != "0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798" {
		t.Fatalf("clé publique = %s", got)
	}
	addr := pub.Address("etl")
	sig, _ := k.Sign("ETL-FR", PurposeTx, []byte("ETL"))
	t.Logf("adresse   = %s", addr)
	t.Logf("signature = %s", hexString(sig[:]))
	if want := refAddress; addr.String() != want {
		t.Errorf("adresse = %s, attendu %s", addr, want)
	}
	if want := refSignature; hexString(sig[:]) != want {
		t.Errorf("signature = %s, attendu %s", hexString(sig[:]), want)
	}
}

const (
	refAddress   = "etl1qnwxsgqx9c4phyx8vjt6650fl5zgxzcaav8qcw0"
	refSignature = "1fca5e70ded84793cfe8f662696d2ca6301f826d8a929194e01f8f94bec355eda145eb5c29fe8c7478a52896ae154a7460140b18a9d2c9c6925ca5ff1ed9f07add"
)

func TestAddressHRP(t *testing.T) {
	k, _ := GenerateKey()
	for _, hrp := range []string{"etl", "etlt", "etlde"} {
		a := k.PublicKey().Address(hrp).String()
		if got, ok := AddressHRP(a); !ok || got != hrp {
			t.Errorf("%s : %q %v", a, got, ok)
		}
		if got, ok := AddressHRP(strings.ToUpper(a)); !ok || got != hrp {
			t.Errorf("majuscules %s : %q %v", a, got, ok)
		}
		last := byte('q')
		if a[len(a)-1] == 'q' {
			last = 'p'
		}
		bad := a[:len(a)-1] + string(last)
		if _, ok := AddressHRP(bad); ok {
			t.Errorf("somme de contrôle fausse acceptée : %s", bad)
		}
	}
	// bech32m valide, mais pas une adresse : mauvaise version, mauvaise longueur.
	notVersion, _ := bech32mEncode("etlt", append([]byte{AddressVersion + 1}, make([]byte, 32)...))
	tooShort, _ := bech32mEncode("etlt", []byte{AddressVersion, 1, 2, 3})
	for _, s := range []string{"", "etl1", "bonjour", "3f7a91c2e8", notVersion, tooShort} {
		if _, ok := AddressHRP(s); ok {
			t.Errorf("%q accepté", s)
		}
	}
}
