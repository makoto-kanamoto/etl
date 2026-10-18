package i18n

import "testing"

func TestParse(t *testing.T) {
	cases := []struct {
		locale string
		want   Lang
	}{
		{"fr_FR.UTF-8", FR},
		{"fr_CA", FR},
		{"fr", FR},
		{"en_US.UTF-8", EN},
		{"ja_JP.eucJP", JA},
		{"ja-JP", JA},
		{"zh_CN.UTF-8", ZH},
		{"zh_TW", ZH},
		{"zh_CN@pinyin", ZH},
		{"FR_fr.utf8", FR},
		{"de_DE.UTF-8", EN}, // non supportée → repli
		{"C", EN},
		{"C.UTF-8", EN},
		{"POSIX", EN},
		{"", EN},
	}
	for _, c := range cases {
		if got := Parse(c.locale); got != c.want {
			t.Errorf("Parse(%q) = %q, attendu %q", c.locale, got, c.want)
		}
	}
}

func TestDetectFromPriority(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want Lang
	}{
		{"aucune variable", map[string]string{}, EN},
		{"LANG seul", map[string]string{"LANG": "ja_JP.UTF-8"}, JA},
		{"LC_MESSAGES l'emporte sur LANG", map[string]string{"LANG": "en_US", "LC_MESSAGES": "zh_CN"}, ZH},
		{"LC_ALL l'emporte sur tout", map[string]string{"LANG": "en_US", "LC_MESSAGES": "zh_CN", "LC_ALL": "fr_FR"}, FR},
		{"LC_ALL vide est ignoré", map[string]string{"LC_ALL": "", "LANG": "fr_FR.UTF-8"}, FR},
		{"première variable non supportée → repli, pas de cascade", map[string]string{"LC_ALL": "de_DE", "LANG": "fr_FR"}, EN},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			getenv := func(k string) string { return c.env[k] }
			if got := DetectFrom(getenv); got != c.want {
				t.Errorf("DetectFrom = %q, attendu %q", got, c.want)
			}
		})
	}
}
