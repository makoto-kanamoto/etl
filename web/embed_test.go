package web

import (
	"bytes"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"io/fs"
	"path"
	"regexp"
	"testing"
)

// Chaque module de l'import map est embarqué dans le binaire, avec le contenu exact de son empreinte ;
// le point d'entrée porte la même empreinte dans son attribut integrity. Un échec ici veut dire que le
// navigateur bloquerait l'interface : ne pas déployer ce binaire.
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
	entry := regexp.MustCompile(`<script type="module" src="/dashboard/app.js" integrity="([^"]+)"></script>`).FindSubmatch(page)
	if entry == nil || string(entry[1]) != im.Integrity["/dashboard/app.js"] {
		t.Fatal("point d'entrée : attribut integrity absent ou différent de l'import map")
	}
}

// Aucun fichier texte embarqué n'a de fin de ligne Windows (CRLF) : elles changent les empreintes et
// bloquent l'interface. Cause habituelle : copie du dépôt faite sous Windows avant .gitattributes.
func TestEmbeddedTextHasNoCRLF(t *testing.T) {
	text := map[string]bool{".js": true, ".html": true, ".css": true, ".json": true, ".svg": true, ".txt": true, ".md": true}
	n := 0
	err := fs.WalkDir(FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !text[path.Ext(p)] {
			return err
		}
		data, err := fs.ReadFile(FS, p)
		if err != nil {
			return err
		}
		n++
		if bytes.Contains(data, []byte("\r\n")) {
			t.Errorf("%s : fins de ligne Windows (CRLF) ; compiler depuis une copie du dépôt en LF (.gitattributes)", p)
		}
		return nil
	})
	if err != nil || n < 50 {
		t.Fatalf("parcours des fichiers embarqués : %v (%d fichiers texte)", err, n)
	}
}

// Polices, logo et icône sont embarqués, et les polices ont l'empreinte de fonts/integrity.json.
func TestEmbeddedFontsAndIcons(t *testing.T) {
	data, err := fs.ReadFile(FS, "dashboard/fonts/integrity.json")
	if err != nil {
		t.Fatal(err)
	}
	var fonts map[string]struct {
		SHA384 string `json:"sha384"`
	}
	if err := json.Unmarshal(data, &fonts); err != nil || len(fonts) != 4 {
		t.Fatalf("fonts/integrity.json : %v (%d polices)", err, len(fonts))
	}
	for name, f := range fonts {
		data, err := fs.ReadFile(FS, "dashboard/fonts/"+name)
		if err != nil {
			t.Errorf("%s absente du binaire", name)
			continue
		}
		sum := sha512.Sum384(data)
		if "sha384-"+base64.StdEncoding.EncodeToString(sum[:]) != f.SHA384 {
			t.Errorf("%s : empreinte différente", name)
		}
	}
	for _, p := range []string{"dashboard/logo.svg", "dashboard/icon-180.png", "dashboard/style.css"} {
		if _, err := fs.Stat(FS, p); err != nil {
			t.Errorf("%s absent du binaire", p)
		}
	}
}
