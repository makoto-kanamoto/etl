package web

import (
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"io/fs"
	"regexp"
	"testing"
)

// Chaque module de l'import map est embarqué dans le binaire, avec le contenu exact de son empreinte.
func TestEmbeddedModulesMatchImportMap(t *testing.T) {
	page, err := fs.ReadFile(FS, "dashboard/index.html")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`<script type="importmap">([^<]*)</script>`).FindSubmatch(page)
	if m == nil {
		t.Fatal("import map absente")
	}
	var im struct {
		Integrity map[string]string `json:"integrity"`
	}
	if err := json.Unmarshal(m[1], &im); err != nil || len(im.Integrity) < 20 {
		t.Fatalf("import map : %v (%d modules)", err, len(im.Integrity))
	}
	for path, want := range im.Integrity {
		data, err := fs.ReadFile(FS, path[1:])
		if err != nil {
			t.Errorf("%s absent du binaire", path)
			continue
		}
		sum := sha512.Sum384(data)
		if got := "sha384-" + base64.StdEncoding.EncodeToString(sum[:]); got != want {
			t.Errorf("%s : empreinte différente", path)
		}
	}
}
