// Package i18n détecte la langue de l'opérateur du nœud à partir de la locale système.
//
// Langues supportées : fr, en, ja, zh. Repli : en.
package i18n

import (
	"os"
	"strings"
)

// Lang est un code de langue ISO 639-1 supporté par ETL.
type Lang string

const (
	FR Lang = "fr"
	EN Lang = "en"
	JA Lang = "ja"
	ZH Lang = "zh"
)

// Fallback est la langue utilisée quand la locale est absente ou non supportée.
const Fallback = EN

// Supported liste les langues disponibles, dans l'ordre d'affichage.
var Supported = []Lang{FR, EN, JA, ZH}

// envVars suit la priorité POSIX : LC_ALL > LC_MESSAGES > LANG.
var envVars = []string{"LC_ALL", "LC_MESSAGES", "LANG"}

// Detect renvoie la langue de la locale système, ou Fallback.
func Detect() Lang {
	return DetectFrom(os.Getenv)
}

// DetectFrom renvoie la langue indiquée par la première variable de locale non vide.
// getenv est injecté pour les tests.
func DetectFrom(getenv func(string) string) Lang {
	for _, name := range envVars {
		if v := getenv(name); v != "" {
			return Parse(v)
		}
	}
	return Fallback
}

// Parse extrait la langue d'une locale POSIX (« fr_FR.UTF-8 », « zh_CN@pinyin »)
// ou d'une étiquette BCP 47 (« ja-JP »). Renvoie Fallback si la langue n'est pas supportée.
func Parse(locale string) Lang {
	base := strings.ToLower(locale)
	if i := strings.IndexAny(base, "_-.@"); i >= 0 {
		base = base[:i]
	}
	for _, l := range Supported {
		if Lang(base) == l {
			return l
		}
	}
	return Fallback
}
