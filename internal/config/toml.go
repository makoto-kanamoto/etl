package config

import (
	"fmt"
	"strconv"
	"strings"
)

// Lecteur TOML limité au sous-ensemble utilisé par la configuration du nœud : commentaires (#),
// sections [nom], et valeurs chaîne ("…" avec \" \\ \n \t), entier, booléen, liste de chaînes
// (éventuellement sur plusieurs lignes). Tout le reste est refusé, avec le numéro de ligne :
// une configuration ambiguë ne doit jamais être lue « à peu près ».

// value est une valeur lue : string, int64, bool ou []string.
type value struct {
	v    any
	line int
}

// document : section → clé → valeur (la section « » précède le premier en-tête).
type document map[string]map[string]value

// ParseError situe une erreur de syntaxe.
type ParseError struct {
	Line int
	Msg  string
}

func (e *ParseError) Error() string { return fmt.Sprintf("config : ligne %d : %s", e.Line, e.Msg) }

func isName(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_') {
			return false
		}
	}
	return true
}

// stripComment retire un commentaire en tenant compte des chaînes.
func stripComment(s string) string {
	in, esc := false, false
	for i, r := range s {
		switch {
		case esc:
			esc = false
		case in && r == '\\':
			esc = true
		case r == '"':
			in = !in
		case !in && r == '#':
			return s[:i]
		}
	}
	return s
}

func parse(src string) (document, error) {
	doc := document{"": {}}
	section := ""
	lines := strings.Split(src, "\n")
	for i := 0; i < len(lines); i++ {
		n := i + 1
		line := strings.TrimSpace(stripComment(strings.TrimSuffix(lines[i], "\r")))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") {
			if !strings.HasSuffix(line, "]") || !isName(line[1:len(line)-1]) {
				return nil, &ParseError{n, "en-tête de section invalide"}
			}
			section = line[1 : len(line)-1]
			if _, dup := doc[section]; dup {
				return nil, &ParseError{n, "section [" + section + "] en double"}
			}
			doc[section] = map[string]value{}
			continue
		}
		key, raw, ok := strings.Cut(line, "=")
		key, raw = strings.TrimSpace(key), strings.TrimSpace(raw)
		if !ok || !isName(key) || raw == "" {
			return nil, &ParseError{n, "attendu : nom = valeur"}
		}
		// Liste sur plusieurs lignes : on accumule jusqu'au crochet fermant.
		if strings.HasPrefix(raw, "[") {
			for !strings.HasSuffix(strings.TrimSpace(raw), "]") {
				i++
				if i >= len(lines) {
					return nil, &ParseError{n, "liste non fermée"}
				}
				raw += " " + strings.TrimSpace(stripComment(strings.TrimSuffix(lines[i], "\r")))
			}
		}
		v, err := parseValue(raw)
		if err != nil {
			return nil, &ParseError{n, err.Error()}
		}
		if _, dup := doc[section][key]; dup {
			return nil, &ParseError{n, "clé « " + key + " » en double"}
		}
		doc[section][key] = value{v, n}
	}
	return doc, nil
}

func parseValue(raw string) (any, error) {
	switch {
	case raw == "true":
		return true, nil
	case raw == "false":
		return false, nil
	case strings.HasPrefix(raw, `"`):
		s, rest, err := parseString(raw)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(rest) != "" {
			return nil, fmt.Errorf("texte après la chaîne")
		}
		return s, nil
	case strings.HasPrefix(raw, "["):
		return parseList(strings.TrimSpace(raw[1 : len(raw)-1]))
	}
	n, err := strconv.ParseInt(strings.ReplaceAll(raw, "_", ""), 10, 64)
	if err != nil || strings.HasPrefix(raw, "_") || strings.HasSuffix(raw, "_") || strings.Contains(raw, "__") {
		return nil, fmt.Errorf("valeur invalide : %s (chaîne entre guillemets, entier ou booléen)", raw)
	}
	return n, nil
}

// parseString lit une chaîne entre guillemets au début de s et renvoie le reste.
func parseString(s string) (string, string, error) {
	var b strings.Builder
	esc := false
	for i, r := range s[1:] {
		switch {
		case esc:
			switch r {
			case '"', '\\':
				b.WriteRune(r)
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			default:
				return "", "", fmt.Errorf("échappement inconnu \\%c", r)
			}
			esc = false
		case r == '\\':
			esc = true
		case r == '"':
			return b.String(), s[i+2:], nil
		case r < 0x20:
			return "", "", fmt.Errorf("caractère de contrôle dans une chaîne")
		default:
			b.WriteRune(r)
		}
	}
	return "", "", fmt.Errorf("chaîne non fermée")
}

func parseList(body string) ([]string, error) {
	out := []string{}
	for body != "" {
		if !strings.HasPrefix(body, `"`) {
			return nil, fmt.Errorf("liste : seules les chaînes sont acceptées")
		}
		s, rest, err := parseString(body)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
		rest = strings.TrimSpace(rest)
		if rest == "" {
			break
		}
		if !strings.HasPrefix(rest, ",") {
			return nil, fmt.Errorf("liste : virgule attendue")
		}
		body = strings.TrimSpace(rest[1:])
	}
	return out, nil
}
