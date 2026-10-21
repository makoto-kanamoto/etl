// Package api sert le dashboard de l'opérateur (localhost) : l'interface web et une API JSON en
// lecture, en soumission (opérations signées dans le navigateur) et pour les actions de l'opérateur
// (signées par la clé du nœud, protégées par un jeton).
//
// Protections (le navigateur de l'opérateur visite aussi des sites hostiles) :
//   - écoute sur 127.0.0.1 seulement ;
//   - en-tête Host limité à localhost:port et 127.0.0.1:port (parade au rebond DNS) ;
//   - toute requête qui modifie : Origin, s'il est présent, égal à celui du dashboard, et corps JSON
//     (Content-Type application/json : un formulaire d'un autre site ne passe pas) ;
//   - actions de l'opérateur : jeton aléatoire du fichier ~/.etl/dashboard.token en plus ;
//   - aucun en-tête CORS ; politique de sécurité du contenu stricte, rien d'externe.
//
// Format JSON : montants en chaînes décimales d'unités (1 ETL = 10^8), adresses bech32m,
// identifiants en hexadécimal.
package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/makoto-kanamoto/etl/internal/blockchain"
	"github.com/makoto-kanamoto/etl/internal/consensus"
	"github.com/makoto-kanamoto/etl/internal/crypto"
	"github.com/makoto-kanamoto/etl/internal/oracle"
)

// Chain : lecture de la chaîne (storage.Store).
type Chain interface {
	Tip() blockchain.Header
	HeaderAt(height uint64) (blockchain.Header, error)
	BlockAt(height uint64) (blockchain.Block, error)
	ReadState(func(*blockchain.State))
}

// Engine : soumissions et état du consensus (consensus.Engine).
type Engine interface {
	SubmitTransaction(blockchain.Transaction) error
	SubmitEscrowOp(blockchain.EscrowOp) error
	SubmitParamVote(blockchain.ParamID, uint64) error
	SubmitExclusionVote(crypto.Address) error
	IPCTallies() []consensus.IPCTally
	ParamTallies() []consensus.ParamTally
	Address() crypto.Address
	NextNonce(crypto.Address) uint64
}

// Network : état du réseau (network.Host).
type Network interface {
	PeerCount() (in, out int)
	Halted() bool
}

// Oracle : lecture de l'IPC (oracle.Oracle).
type Oracle interface {
	Status() oracle.Status
	ReadNow(ctx context.Context, now time.Time) (*blockchain.IPCRecord, error)
}

// Config d'un serveur. Engine, Network et Oracle peuvent être nil (nœud passif, lecture seule).
type Config struct {
	Params    blockchain.Params
	Port      int    // [dashboard] port
	TokenPath string // fichier du jeton opérateur (créé à chaque démarrage, 0600)
	ReadOnly  bool   // lecture seule : aucune soumission ni action (exposition publique future)
	Static    fs.FS  // interface web (web.FS) ; nil : API seule
	Version   string // version du logiciel, affichée
	Now       func() time.Time
	Poll      time.Duration // fréquence de surveillance des nouveaux blocs (SSE) ; défaut 1 s
	MaxEvents int           // clients SSE simultanés au plus ; défaut 16

	Chain   Chain
	Engine  Engine
	Network Network
	Oracle  Oracle
}

// Server est le serveur du dashboard.
type Server struct {
	cfg    Config
	token  string
	mux    *http.ServeMux
	events chan struct{} // jetons de clients SSE
	csp    string
}

const cspBase = "default-src 'none'; script-src 'self'%s; style-src 'self'; img-src 'self' data:; " +
	"connect-src 'self'; font-src 'self'; manifest-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// importMapHash renvoie l'empreinte CSP (sha256) de l'import map de dashboard/index.html : seul
// script en ligne autorisé, il porte l'empreinte SRI de chaque module. Vide sans import map.
func importMapHash(static fs.FS) (string, error) {
	if static == nil {
		return "", nil
	}
	page, err := fs.ReadFile(static, "dashboard/index.html")
	if err != nil {
		return "", nil // API seule, ou interface absente
	}
	const open, end = `<script type="importmap">`, `</script>`
	i := bytes.Index(page, []byte(open))
	if i < 0 {
		return "", nil
	}
	rest := page[i+len(open):]
	j := bytes.Index(rest, []byte(end))
	if j < 0 || bytes.Contains(rest[j+len(end):], []byte(open)) {
		return "", errors.New("api: import map mal formée ou en double dans dashboard/index.html")
	}
	sum := sha256.Sum256(rest[:j])
	return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'", nil
}

const maxBody = 64 << 10

// New prépare le serveur et écrit un nouveau jeton opérateur (sauf en lecture seule).
func New(cfg Config) (*Server, error) {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Poll == 0 {
		cfg.Poll = time.Second
	}
	if cfg.MaxEvents == 0 {
		cfg.MaxEvents = 16
	}
	s := &Server{cfg: cfg, mux: http.NewServeMux(), events: make(chan struct{}, cfg.MaxEvents)}
	h, err := importMapHash(cfg.Static)
	if err != nil {
		return nil, err
	}
	if h != "" {
		h = " " + h
	}
	s.csp = fmt.Sprintf(cspBase, h)
	if !cfg.ReadOnly {
		tok, err := writeToken(cfg.TokenPath)
		if err != nil {
			return nil, err
		}
		s.token = tok
	}
	s.routes()
	return s, nil
}

// writeToken crée un jeton aléatoire de 32 octets et l'écrit (0600, répertoire 0700).
func writeToken(path string) (string, error) {
	if path == "" {
		return "", errors.New("api: chemin du jeton opérateur manquant")
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(b)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(tok+"\n"), 0o600); err != nil {
		return "", err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		return "", err
	}
	return tok, os.Rename(tmp, path)
}

func (s *Server) routes() {
	m := s.mux
	m.HandleFunc("GET /api/status", s.status)
	m.HandleFunc("GET /api/blocks", s.blocks)
	m.HandleFunc("GET /api/blocks/{height}", s.block)
	m.HandleFunc("GET /api/accounts/{address}", s.account)
	m.HandleFunc("GET /api/accounts/{address}/history", s.history)
	m.HandleFunc("GET /api/escrows/{id}", s.escrow)
	m.HandleFunc("GET /api/ipc", s.ipc)
	m.HandleFunc("GET /api/params", s.params)
	m.HandleFunc("GET /api/events", s.eventsHandler)
	if !s.cfg.ReadOnly {
		m.HandleFunc("POST /api/tx", s.submitTx)
		m.HandleFunc("POST /api/escrow", s.submitEscrow)
		m.HandleFunc("POST /api/operator/param-vote", s.operator(s.paramVote))
		m.HandleFunc("POST /api/operator/exclusion-vote", s.operator(s.exclusionVote))
		m.HandleFunc("POST /api/operator/ipc-read", s.operator(s.ipcRead))
	}
	m.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { fail(w, http.StatusNotFound, "point d'API inconnu") })
	if s.cfg.Static != nil {
		m.HandleFunc("/", s.static)
	}
}

// ServeHTTP applique les protections communes puis route la requête.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Content-Security-Policy", s.csp)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	if !s.allowedHost(r.Host) {
		fail(w, http.StatusForbidden, "hôte non autorisé")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		if o := r.Header.Get("Origin"); o != "" && !s.allowedOrigin(o) {
			fail(w, http.StatusForbidden, "origine non autorisée")
			return
		}
		if mt := strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]); mt != "application/json" {
			fail(w, http.StatusUnsupportedMediaType, "corps JSON attendu")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	}
	s.mux.ServeHTTP(w, r)
}

func (s *Server) allowedHost(host string) bool {
	return host == fmt.Sprintf("localhost:%d", s.cfg.Port) || host == fmt.Sprintf("127.0.0.1:%d", s.cfg.Port)
}

func (s *Server) allowedOrigin(o string) bool {
	return o == fmt.Sprintf("http://localhost:%d", s.cfg.Port) || o == fmt.Sprintf("http://127.0.0.1:%d", s.cfg.Port)
}

// operator exige le jeton opérateur (en-tête Authorization: Bearer <jeton>).
func (s *Server) operator(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) != 1 {
			fail(w, http.StatusUnauthorized, "jeton opérateur absent ou invalide")
			return
		}
		h(w, r)
	}
}

// ListenAndServe écoute sur 127.0.0.1:port jusqu'à l'annulation du contexte.
func (s *Server) ListenAndServe(ctx context.Context) error {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", s.cfg.Port))
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: s, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second,
		IdleTimeout: 120 * time.Second, MaxHeaderBytes: 16 << 10}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func reply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
