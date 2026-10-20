package wallet

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	mrand "math/rand"
	"sort"
	"strings"
	"testing"
)

// Les listes embarquées sont exactement les fichiers officiels (github.com/bitcoin/bips, bip-0039).
func TestWordlistsAreOfficial(t *testing.T) {
	for name, c := range map[string]struct{ raw, want string }{
		"française": {frenchList, frenchListSHA256},
		"anglaise":  {englishList, englishListSHA256},
	} {
		sum := sha256.Sum256([]byte(c.raw))
		if hex.EncodeToString(sum[:]) != c.want {
			t.Errorf("liste %s modifiée", name)
		}
	}
	for lang, wl := range lists {
		if len(wl.words) != 2048 || len(wl.index) != 2048 {
			t.Errorf("%s : %d mots, %d formes normalisées distinctes (2048 attendus)", lang, len(wl.words), len(wl.index))
		}
	}
}

// Vecteurs officiels BIP-39 (anglais, 32 octets) : github.com/trezor/python-mnemonic, vectors.json.
func TestOfficialVectors(t *testing.T) {
	vectors := []struct{ entropy, phrase string }{
		{"0000000000000000000000000000000000000000000000000000000000000000", "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon art"},
		{"7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f", "legal winner thank year wave sausage worth useful legal winner thank year wave sausage worth useful legal winner thank year wave sausage worth title"},
		{"8080808080808080808080808080808080808080808080808080808080808080", "letter advice cage absurd amount doctor acoustic avoid letter advice cage absurd amount doctor acoustic avoid letter advice cage absurd amount doctor acoustic bless"},
		{"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", "zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo zoo vote"},
		{"68a79eaca2324873eacc50cb9c6eca8cc68ea5d936f98787c60c7ebc74e6ce7c", "hamster diagram private dutch cause delay private meat slide toddler razor book happy fancy gospel tennis maple dilemma loan word shrug inflict delay length"},
		{"9f6a2878b2520799a44ef18bc7df394e7061a224d2c33cd015b157d746869863", "panda eyebrow bullet gorilla call smoke muffin taste mesh discover soft ostrich alcohol speed nation flash devote level hobby quick inner drive ghost inside"},
		{"066dca1a2bb7e8a1db2832148ce9933eea0f3ac9548d793112d9a95c9407efad", "all hour make first leader extend hole alien behind guard gospel lava path output census museum junior mass reopen famous sing advance salt reform"},
		{"f585c11aec520db57dd353c69554b21a89b20fb0650966fa0a9d6f74fd989d8f", "void come effort suffer camp survey warrior heavy shoot primary clutch crush open amazing screen patrol group space point ten exist slush involve unfold"},
	}
	for _, v := range vectors {
		entropy, _ := hex.DecodeString(v.entropy)
		words, err := EncodePhrase(entropy, English)
		if err != nil || strings.Join(words, " ") != v.phrase {
			t.Errorf("encodage %s…", v.entropy[:8])
		}
		key, lang, err := DecodePhrase(v.phrase)
		if err != nil || lang != English || !bytes.Equal(key, entropy) {
			t.Errorf("décodage %s… : %v (%s)", v.entropy[:8], err, lang)
		}
	}
}

func TestRoundTripBothLanguages(t *testing.T) {
	for _, lang := range []Language{French, English} {
		for i := 0; i < 200; i++ {
			key := make([]byte, 32)
			_, _ = rand.Read(key)
			words, err := EncodePhrase(key, lang)
			if err != nil {
				t.Fatal(err)
			}
			got, gotLang, err := DecodePhrase(strings.Join(words, " "))
			if err != nil || gotLang != lang || !bytes.Equal(got, key) {
				t.Fatalf("%s : aller-retour : %v", lang, err)
			}
		}
	}
}

// Saisie tolérante : majuscules, espaces multiples, sans accents, accents composés ou décomposés.
func TestFrenchInputTolerance(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i * 37)
	}
	words, _ := EncodePhrase(key, French)
	joined := strings.Join(words, " ")
	if !strings.ContainsAny(joined, "éè") {
		t.Fatal("prémisse : phrase avec accents")
	}
	strip := strings.NewReplacer("é", "e", "è", "e").Replace
	decomposed := strings.NewReplacer("é", "é", "è", "è").Replace
	for name, in := range map[string]string{
		"telle quelle":       joined,
		"sans accents":       strip(joined),
		"majuscules":         strings.ToUpper(joined),
		"accents décomposés": decomposed(joined),
		"espaces multiples":  "  " + strings.Join(words, "   ") + "\n",
	} {
		got, lang, err := DecodePhrase(in)
		if err != nil || lang != French || !bytes.Equal(got, key) {
			t.Errorf("%s : %v", name, err)
		}
	}
	// L'affichage utilise les caractères accentués usuels, jamais la forme décomposée de la liste officielle.
	if strings.ContainsRune(joined, '́') || strings.ContainsRune(joined, '̀') {
		t.Error("accents décomposés dans la phrase affichée")
	}
}

// Une faute de frappe sur un mot est détectée (somme de contrôle de 8 bits : 255 fois sur 256).
func TestTypoDetected(t *testing.T) {
	detected, trials := 0, 4000
	for i := 0; i < trials; i++ {
		key := make([]byte, 32)
		_, _ = rand.Read(key)
		words, _ := EncodePhrase(key, English)
		pos := i % 24
		words[pos] = lists[English].words[(lists[English].index[words[pos]]+1+i%2000)%2048]
		got, _, err := DecodePhrase(strings.Join(words, " "))
		if err != nil || !bytes.Equal(got, key) {
			detected++
		}
	}
	if rate := float64(detected) / float64(trials); rate < 0.98 {
		t.Fatalf("taux de détection %.3f, attendu ~0,996", rate)
	}
}

func TestDecodeRejects(t *testing.T) {
	fr, _ := EncodePhrase(make([]byte, 32), French)
	en, _ := EncodePhrase(make([]byte, 32), English)
	mixed := append(append([]string(nil), fr[:12]...), en[12:]...)
	for name, c := range map[string]struct {
		in   string
		want error
	}{
		"23 mots":           {strings.Join(en[:23], " "), ErrPhraseLength},
		"mot inconnu":       {strings.Join(append(append([]string(nil), en[:23]...), "bitcoin"), " "), ErrPhraseWord},
		"langues mélangées": {strings.Join(mixed, " "), ErrPhraseLanguage},
		"somme fausse":      {strings.Join(append(append([]string(nil), en[:23]...), "zoo"), " "), ErrPhraseChecksum},
	} {
		if _, _, err := DecodePhrase(c.in); err != c.want {
			t.Errorf("%s : err = %v, attendu %v", name, err, c.want)
		}
	}
}

// Une phrase formée de mots communs aux deux listes (« abandon »…) peut être valide dans les deux
// langues (environ 1 fois sur 65 536) : elle désigne alors deux clés différentes et doit être refusée.
func TestAmbiguousPhraseRefused(t *testing.T) {
	var common []string
	for w := range lists[French].index {
		if _, ok := lists[English].index[w]; ok {
			common = append(common, w)
		}
	}
	sort.Strings(common)
	rng := mrand.New(mrand.NewSource(1))
	fields := make([]string, 24)
	for try := 0; try < 1<<22; try++ {
		for i := range fields {
			fields[i] = common[rng.Intn(len(common))]
		}
		_, frOK, frErr := decodeIn(fields, lists[French])
		_, enOK, enErr := decodeIn(fields, lists[English])
		if !frOK || !enOK || frErr != nil || enErr != nil {
			continue
		}
		if _, _, err := DecodePhrase(strings.Join(fields, " ")); err != ErrPhraseLanguage {
			t.Fatalf("phrase valide dans les deux langues : err = %v, attendu %v", err, ErrPhraseLanguage)
		}
		return
	}
	t.Fatal("prémisse : aucune phrase ambiguë trouvée")
}

// DecodePhrase reçoit une saisie arbitraire : jamais de panique, et toute phrase acceptée
// désigne une clé de 32 octets qui se ré-encode en une phrase équivalente.
func FuzzDecodePhrase(f *testing.F) {
	fr, _ := EncodePhrase(make([]byte, 32), French)
	en, _ := EncodePhrase(bytes.Repeat([]byte{0xff}, 32), English)
	f.Add(strings.Join(fr, " "))
	f.Add(strings.ToUpper(strings.Join(en, "  ")))
	f.Add("abandon abandon")
	f.Add("élève élève")
	f.Add("")
	f.Fuzz(func(t *testing.T, phrase string) {
		key, lang, err := DecodePhrase(phrase)
		if err != nil {
			return
		}
		if len(key) != 32 {
			t.Fatalf("clé de %d octets", len(key))
		}
		words, err := EncodePhrase(key, lang)
		if err != nil {
			t.Fatal(err)
		}
		again, lang2, err := DecodePhrase(strings.Join(words, " "))
		if err != nil || lang2 != lang || !bytes.Equal(again, key) {
			t.Fatalf("aller-retour : %v", err)
		}
		for i, w := range strings.Fields(phrase) {
			if normalize(w) != normalize(words[i]) {
				t.Fatalf("mot %d : %q ≠ %q", i, w, words[i])
			}
		}
	})
}
