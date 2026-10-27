package api

import (
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Public règle le site public (etalon-etl.com) : le serveur est joint à travers un proxy HTTPS local
// (Caddy). Différences avec le dashboard de l'opérateur :
//   - Host limité au nom du site, Origin à https://<nom> ; HSTS ;
//   - aucune route de l'opérateur, aucun jeton écrit ; pas de flux SSE (les pages relisent l'API) ;
//   - soumissions (transferts, échanges signés dans le navigateur) acceptées ;
//   - limitation de débit par adresse IP, en mémoire seulement : aucune adresse IP n'est journalisée
//     ni écrite sur disque ;
//   - sans chaîne (Config.Chain nil, réseau pas encore lancé) : pages de présentation et statut
//     minimal seulement.
type Public struct {
	Host           string // nom d'hôte du site, sans schéma ni port
	ReadPerMinute  int    // lectures de l'API par IP et par minute
	WritePerMinute int    // soumissions par IP et par minute
}

// HSTS : deux ans ; les sous-domaines (nœuds d'amorçage, P2P en TCP) ne sont pas concernés.
const hsts = "max-age=63072000"

// clientIP renvoie la clé de limitation du client : l'adresse transmise par le proxy local
// (dernière valeur de X-Forwarded-For, celle qu'il a lui-même ajoutée) si la connexion vient de la
// boucle locale, sinon l'adresse de la connexion. IPv6 : préfixe /64 (une machine en a des milliers).
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip != nil && ip.IsLoopback() {
		if xff := r.Header.Values("X-Forwarded-For"); len(xff) > 0 {
			parts := strings.Split(xff[len(xff)-1], ",")
			if fwd := net.ParseIP(strings.TrimSpace(parts[len(parts)-1])); fwd != nil {
				ip = fwd
			}
		}
	}
	if ip == nil {
		return "?"
	}
	if v4 := ip.To4(); v4 != nil {
		return v4.String()
	}
	return ip.Mask(net.CIDRMask(64, 128)).String()
}

// limiter : seau à jetons par client, rempli de perMinute jetons par minute, contenant au plus
// perMinute jetons. Les seaux pleins (clients inactifs depuis une minute) sont oubliés.
type limiter struct {
	mu        sync.Mutex
	perMinute float64
	now       func() time.Time
	buckets   map[string]*bucket
	swept     time.Time
}

type bucket struct {
	tokens float64
	at     time.Time
}

// maxClients : au-delà, les nouveaux clients partagent un seau commun (mémoire bornée).
const maxClients = 100_000

func newLimiter(perMinute int, now func() time.Time) *limiter {
	return &limiter{perMinute: float64(perMinute), now: now, buckets: map[string]*bucket{}}
}

// allow consomme un jeton du client key ; sinon, renvoie l'attente avant le prochain jeton.
func (l *limiter) allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if now.Sub(l.swept) >= time.Minute {
		for k, b := range l.buckets {
			if l.level(b, now) >= l.perMinute {
				delete(l.buckets, k)
			}
		}
		l.swept = now
	}
	b, ok := l.buckets[key]
	if !ok {
		if len(l.buckets) >= maxClients {
			key = "*"
			b, ok = l.buckets[key]
		}
		if !ok {
			b = &bucket{tokens: l.perMinute, at: now}
			l.buckets[key] = b
		}
	}
	b.tokens, b.at = l.level(b, now), now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	return false, time.Duration((1 - b.tokens) * float64(time.Minute) / l.perMinute)
}

func (l *limiter) level(b *bucket, now time.Time) float64 {
	return math.Min(l.perMinute, b.tokens+now.Sub(b.at).Minutes()*l.perMinute)
}

// limit applique la limitation de débit aux requêtes de l'API (les fichiers de l'interface, servis
// depuis la mémoire, n'y sont pas soumis). Renvoie false si la requête a été refusée.
func (s *Server) limit(w http.ResponseWriter, r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, "/api/") {
		return true
	}
	l := s.reads
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		l = s.writes
	}
	ok, wait := l.allow(clientIP(r))
	if !ok {
		secs := int(math.Ceil(wait.Seconds()))
		w.Header().Set("Retry-After", strconv.Itoa(secs))
		fail(w, http.StatusTooManyRequests, "trop de requêtes : réessayez dans "+strconv.Itoa(secs)+" s")
	}
	return ok
}

// prelaunchStatus : réseau pas encore lancé, aucune chaîne.
func (s *Server) prelaunchStatus(w http.ResponseWriter, _ *http.Request) {
	reply(w, map[string]any{"version": s.cfg.Version, "chain_id": s.cfg.Params.ChainID, "hrp": s.cfg.Params.AddressHRP,
		"public": true, "launched": false})
}
