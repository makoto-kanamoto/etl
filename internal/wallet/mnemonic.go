// Package wallet garde la clé d'un nœud ou d'un utilisateur : coffre chiffré par phrase de passe
// (Argon2id + AES-256-GCM), phrase de récupération de 24 mots (BIP-39) et construction de transactions.
package wallet

import (
	"crypto/sha256"
	_ "embed"
	"errors"
	"strings"
	"unicode"
)

// Phrase de récupération : les 32 octets de la clé privée + 8 bits de somme de contrôle (SHA-256),
// soit 264 bits = 24 mots de 11 bits, selon l'encodage BIP-39 et ses listes officielles.
//
// ATTENTION : la phrase encode DIRECTEMENT la clé (une clé par phrase). Elle n'est PAS compatible
// avec les wallets Bitcoin ou Ethereum (Ledger, MetaMask…), qui dérivent leurs clés autrement à partir
// des mêmes mots : importer une phrase ETL dans un tel wallet donnerait une autre clé, un autre compte.

// Language désigne une liste de mots officielle BIP-39.
type Language string

const (
	French  Language = "fr"
	English Language = "en"
)

// Empreintes SHA-256 des fichiers officiels (github.com/bitcoin/bips, bip-0039).
const (
	frenchListSHA256  = "ebc3959ab7801a1df6bac4fa7d970652f1df76b683cd2f4003c941c63d517e59"
	englishListSHA256 = "2f5eed53a4727b4bf8880d8f3f199efc90e58503646d9ff8eff3a2ed3b24dbda"
)

//go:embed wordlists/french.txt
var frenchList string

//go:embed wordlists/english.txt
var englishList string

var (
	ErrPhraseLength   = errors.New("wallet: la phrase de récupération doit compter 24 mots")
	ErrPhraseWord     = errors.New("wallet: mot inconnu dans la phrase de récupération")
	ErrPhraseChecksum = errors.New("wallet: phrase de récupération incorrecte (somme de contrôle)")
	ErrPhraseLanguage = errors.New("wallet: mots de plusieurs langues mélangés")
)

type wordlist struct {
	words []string       // forme d'affichage (accents composés : « é », « è »)
	index map[string]int // forme normalisée (minuscules, sans accents) → indice
}

var lists = map[Language]*wordlist{
	French:  loadList(frenchList),
	English: loadList(englishList),
}

func loadList(raw string) *wordlist {
	wl := &wordlist{index: map[string]int{}}
	for i, w := range strings.Fields(raw) {
		wl.words = append(wl.words, compose(w))
		wl.index[normalize(w)] = i
	}
	return wl
}

// compose remplace les accents décomposés de la liste officielle française (lettre + accent
// combinant U+0301 ou U+0300) par les caractères usuels : seuls « é » et « è » y figurent.
func compose(w string) string {
	return strings.NewReplacer("e\u0301", "\u00e9", "e\u0300", "\u00e8").Replace(w)
}

// normalize met un mot en minuscules et retire ses accents, composés ou décomposés :
// « Académie », « academie » et la forme officielle décomposée deviennent identiques.
func normalize(w string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(w)) {
		switch {
		case unicode.Is(unicode.Mn, r): // accent combinant
			continue
		case strings.ContainsRune("éèêë", r):
			b.WriteRune('e')
		case strings.ContainsRune("àâä", r):
			b.WriteRune('a')
		case strings.ContainsRune("îï", r):
			b.WriteRune('i')
		case strings.ContainsRune("ôö", r):
			b.WriteRune('o')
		case strings.ContainsRune("ùûü", r):
			b.WriteRune('u')
		case r == 'ç':
			b.WriteRune('c')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// EncodePhrase renvoie les 24 mots correspondant à une clé privée de 32 octets.
func EncodePhrase(key []byte, lang Language) ([]string, error) {
	wl, ok := lists[lang]
	if !ok || len(key) != 32 {
		return nil, ErrPhraseLength
	}
	sum := sha256.Sum256(key)
	bits := append(append([]byte(nil), key...), sum[0]) // 264 bits
	words := make([]string, 24)
	for i := range words {
		idx := 0
		for j := 0; j < 11; j++ {
			bit := i*11 + j
			idx = idx<<1 | int(bits[bit/8]>>(7-bit%8)&1)
		}
		words[i] = wl.words[idx]
	}
	return words, nil
}

// DecodePhrase retrouve la clé privée (32 octets) et la langue d'une phrase de 24 mots.
// Tolère majuscules, espaces multiples et absence d'accents ; détecte une faute de frappe sur un
// mot 255 fois sur 256 (somme de contrôle de 8 bits). Certains mots existent dans les deux listes
// (« abandon »…) : la phrase est décodée dans chaque langue et la somme de contrôle tranche.
func DecodePhrase(phrase string) ([]byte, Language, error) {
	fields := strings.Fields(phrase)
	if len(fields) != 24 {
		return nil, "", ErrPhraseLength
	}
	var found []Language
	var key []byte
	complete := 0
	for _, lang := range []Language{French, English} {
		k, ok, err := decodeIn(fields, lists[lang])
		if ok {
			complete++
		}
		if err == nil {
			found, key = append(found, lang), k
		}
	}
	switch {
	case len(found) == 1:
		return key, found[0], nil
	case len(found) > 1: // les deux langues valides : impossible en pratique, refusé par prudence
		return nil, "", ErrPhraseLanguage
	case complete > 0:
		return nil, "", ErrPhraseChecksum
	}
	for _, f := range fields { // aucun mot inconnu des deux listes : langues mélangées
		_, fr := lists[French].index[normalize(f)]
		_, en := lists[English].index[normalize(f)]
		if !fr && !en {
			return nil, "", ErrPhraseWord
		}
	}
	return nil, "", ErrPhraseLanguage
}

// decodeIn décode la phrase dans une liste : ok si tous les mots y figurent, err si la somme de
// contrôle est fausse.
func decodeIn(fields []string, wl *wordlist) (key []byte, ok bool, err error) {
	bits := make([]byte, 33)
	for i, f := range fields {
		idx, found := wl.index[normalize(f)]
		if !found {
			return nil, false, ErrPhraseWord
		}
		for j := 0; j < 11; j++ {
			if idx>>(10-j)&1 == 1 {
				bit := i*11 + j
				bits[bit/8] |= 1 << (7 - bit%8)
			}
		}
	}
	key = bits[:32]
	if sum := sha256.Sum256(key); sum[0] != bits[32] {
		return nil, true, ErrPhraseChecksum
	}
	return key, true, nil
}
