package api

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

var contentTypes = map[string]string{
	".html":  "text/html; charset=utf-8",
	".js":    "text/javascript; charset=utf-8",
	".css":   "text/css; charset=utf-8",
	".json":  "application/json; charset=utf-8",
	".svg":   "image/svg+xml",
	".png":   "image/png",
	".txt":   "text/plain; charset=utf-8",
	".woff2": "font/woff2",
}

// static sert l'interface : « / » → dashboard/index.html. Tests, données de test, sources Go et
// fichiers cachés ne sont jamais servis.
func (s *Server) static(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		fail(w, http.StatusMethodNotAllowed, "méthode non autorisée")
		return
	}
	p := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if p == "" {
		p = "dashboard/index.html"
	}
	ct, known := contentTypes[path.Ext(p)]
	if !known || strings.HasSuffix(p, ".test.js") || strings.Contains("/"+p, "/testdata/") || strings.Contains("/"+p, "/.") {
		fail(w, http.StatusNotFound, "fichier inconnu")
		return
	}
	data, err := fs.ReadFile(s.cfg.Static, p)
	if err != nil {
		fail(w, http.StatusNotFound, "fichier inconnu")
		return
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(data)
}
