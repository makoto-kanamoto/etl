package crypto

import (
	"strings"
	"testing"
)

// Vecteurs officiels BIP-350 : https://github.com/bitcoin/bips/blob/master/bip-0350.mediawiki

func TestBech32mValidVectors(t *testing.T) {
	for _, s := range []string{
		"A1LQFN3A",
		"a1lqfn3a",
		"an83characterlonghumanreadablepartthatcontainsthetheexcludedcharactersbioandnumber11sg7hg6",
		"abcdef1l7aum6echk45nj3s0wdvt2fg8x9yrzpqzd3ryx",
		"11llllllllllllllllllllllllllllllllllllllllllllllllllllllllllllllllllllllllllllllllllludsr8",
		"split1checkupstagehandshakeupstreamerranterredcaperredlc445v",
		"?1v759aa",
	} {
		hrp, data, err := bech32mDecode(s)
		if err != nil {
			t.Errorf("%s : rejeté à tort", s)
			continue
		}
		back, err := bech32mEncode(hrp, data)
		if err != nil || back != strings.ToLower(s) {
			t.Errorf("%s : ré-encodé en %q (err=%v)", s, back, err)
		}
	}
}

func TestBech32mInvalidVectors(t *testing.T) {
	for _, s := range []string{
		"\x201xj0phk", // caractère HRP hors plage
		"\x7f1g6xzxy", // caractère HRP hors plage
		"\x801vctc34", // caractère HRP hors plage
		"an84characterslonghumanreadablepartthatcontainsthetheexcludedcharactersbioandnumber11d6pts4", // trop long
		"qyrz8wqd2c9m",  // pas de séparateur
		"1qyrz8wqd2c9m", // HRP vide
		"y1b0jsk6g",     // caractère de données invalide
		"lt1igcx5c0",    // caractère de données invalide
		"in1muywd",      // checksum trop court
		"mm1crxm3i",     // caractère invalide dans le checksum
		"au1s5cgom",     // caractère invalide dans le checksum
		"M1VUXWEZ",      // checksum calculé avec le HRP en majuscules
		"16plkw9",       // HRP vide
		"1p2gdwpf",      // HRP vide
		"A12UEL5L",      // bech32 valide, mais pas bech32m
		"bc1p0xlxvlhemja6c4dqv22uapctqupfhlxm9h8z3k2e72q4k9hcz7vqh2y7hd", // bech32 au lieu de bech32m
		"tb1p0xlxvlhemja6c4dqv22uapctqupfhlxm9h8z3k2e72q4k9hcz7vq47Zagq", // casse mixte
	} {
		if _, _, err := bech32mDecode(s); err == nil {
			t.Errorf("%q : accepté à tort", s)
		}
	}
}

// BIP-350 : rembourrage invalide lors de la conversion 5 → 8 bits.
func TestConvertBitsPaddingVectors(t *testing.T) {
	for _, s := range []string{
		"bc1p0xlxvlhemja6c4dqv22uapctqupfhlxm9h8z3k2e72q4k9hcz7v07qwwzcrf", // plus de 4 bits de rembourrage
		"tb1p0xlxvlhemja6c4dqv22uapctqupfhlxm9h8z3k2e72q4k9hcz7vpggkg4j",   // rembourrage non nul
	} {
		_, data, err := bech32mDecode(s)
		if err != nil {
			t.Fatalf("%s : checksum devrait être valide : %v", s, err)
		}
		if _, err := convertBits(data[1:], 5, 8, false); err == nil {
			t.Errorf("%s : rembourrage accepté à tort", s)
		}
	}
}

func TestBech32mEncodeRejectsUppercaseHRP(t *testing.T) {
	if _, err := bech32mEncode("ETL", nil); err == nil {
		t.Error("HRP en majuscules accepté à l'encodage")
	}
}
