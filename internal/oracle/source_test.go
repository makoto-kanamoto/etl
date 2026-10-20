package oracle

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

const fixture = "testdata/insee-011814630.xml" // réponse INSEE réelle (20/10/2026), réduite à 14 observations

func readFixture(t testing.TB) []byte {
	t.Helper()
	data, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// sdmx construit une réponse au format INSEE avec les éléments <Obs> donnés.
func sdmx(series string, obs ...string) []byte {
	return []byte(`<?xml version='1.0' encoding='UTF-8'?><message:StructureSpecificData xmlns:message="http://www.sdmx.org/resources/sdmxml/schemas/v2_1/message"><message:DataSet>` +
		series + strings.Join(obs, "") + `</Series></message:DataSet></message:StructureSpecificData>`)
}

const series = `<Series IDBANK="011814630" FREQ="M" DECIMALS="2">`

func obs(month, value, qual string) string {
	return `<Obs TIME_PERIOD="` + month + `" OBS_VALUE="` + value + `" OBS_STATUS="A" OBS_QUAL="` + qual + `" OBS_TYPE="A"/>`
}

// Vecteur de référence : septembre 2026 → 2026-Q3, 103,07, empreinte de l'élément <Obs> exact.
func TestParseRealINSEEResponse(t *testing.T) {
	data := readFixture(t)
	got, err := Parse(data, "011814630")
	if err != nil || len(got) != 14 {
		t.Fatalf("%d observations, err = %v", len(got), err)
	}
	first := got[0]
	want := `<Obs TIME_PERIOD="2026-09" OBS_VALUE="103.07" OBS_STATUS="A" OBS_REV="1" OBS_QUAL="DEF" OBS_TYPE="A" DATE_JO="2026-10-16"/>`
	if first.Month != "2026-09" || first.Value != 103070 || !first.Definitive || string(first.Raw) != want {
		t.Fatalf("première observation : %+v / %s", first, first.Raw)
	}
	for _, o := range got {
		if !bytes.Contains(data, o.Raw) || !bytes.HasPrefix(o.Raw, []byte("<Obs ")) || !bytes.HasSuffix(o.Raw, []byte("/>")) {
			t.Fatalf("%s : octets bruts inexacts : %s", o.Month, o.Raw)
		}
	}
	rec, ok := Latest(got, "2026-Q2")
	if !ok || rec.Period != "2026-Q3" || rec.Value != 103070 ||
		hex.EncodeToString(rec.SourceSHA256[:]) != "eda2e33cc65ca9b0218d281c0faf63bb96db4e0010fc5cb4cf28627454176c73" {
		t.Fatalf("triplet : %+v (%v)", rec, ok)
	}
	if _, ok := Latest(got, "2026-Q3"); ok {
		t.Fatal("trimestre déjà inscrit : aucun vote attendu")
	}
}

// Élément non auto-fermant : l'empreinte couvre aussi la balise de fin.
func TestParseRawIncludesEndTag(t *testing.T) {
	el := `<Obs TIME_PERIOD="2026-09" OBS_VALUE="103.07" OBS_QUAL="DEF"></Obs>`
	got, err := Parse(sdmx(series, el), "011814630")
	if err != nil || len(got) != 1 || string(got[0].Raw) != el {
		t.Fatalf("%v : %+v", err, got)
	}
}

func TestParseValue(t *testing.T) {
	for in, want := range map[string]uint64{"103.07": 103070, "103.070": 103070, "1": 1000, "0.001": 1, "99.5": 99500} {
		if got, err := parseValue(in); err != nil || got != want {
			t.Errorf("%q → %d, %v (attendu %d)", in, got, err, want)
		}
	}
	for _, in := range []string{"", "0", "0.000", "-1", "103.0701", "1e3", " 103", "103 ", "1.", ".5", "103,07", "1234567890123456", "NaN"} {
		if _, err := parseValue(in); err != ErrValue {
			t.Errorf("%q accepté (err = %v)", in, err)
		}
	}
}

func TestParseRejects(t *testing.T) {
	ok := obs("2026-09", "103.07", "DEF")
	cases := map[string]struct {
		data []byte
		want error
	}{
		"autre série":            {sdmx(`<Series IDBANK="001763852" FREQ="M" DECIMALS="2">`, ok), ErrSeries},
		"série trimestrielle":    {sdmx(`<Series IDBANK="011814630" FREQ="Q" DECIMALS="2">`, ok), ErrSeries},
		"fréquence absente":      {sdmx(`<Series IDBANK="011814630" DECIMALS="2">`, ok), ErrSeries},
		"4 décimales":            {sdmx(`<Series IDBANK="011814630" FREQ="M" DECIMALS="4">`, ok), ErrSeries},
		"décimales illisibles":   {sdmx(`<Series IDBANK="011814630" FREQ="M" DECIMALS="deux">`, ok), ErrSeries},
		"mois en double":         {sdmx(series, ok, ok), ErrFormat},
		"mois invalide":          {sdmx(series, obs("2026-13", "103.07", "DEF")), ErrFormat},
		"valeur absente":         {sdmx(series, `<Obs TIME_PERIOD="2026-09" OBS_QUAL="DEF"/>`), ErrValue},
		"valeur négative":        {sdmx(series, obs("2026-09", "-103.07", "DEF")), ErrValue},
		"deux séries":            {sdmx(series, ok, `</Series>`, series, obs("2026-06", "102.11", "DEF")), ErrFormat},
		"aucune série":           {[]byte(`<message:DataSet xmlns:message="m"></message:DataSet>`), ErrFormat},
		"observation hors série": {[]byte(`<DataSet>` + obs("2026-12", "104", "DEF") + series + ok + `</Series></DataSet>`), ErrFormat},
		"observation imbriquée":  {sdmx(series, `<Obs TIME_PERIOD="2026-12" OBS_VALUE="1" OBS_QUAL="DEF">`+ok+`</Obs>`), ErrFormat},
		"XML tronqué":            {sdmx(series, ok)[:200], ErrFormat},
		"pas du XML":             {[]byte(`{"value": 103.07}`), ErrFormat},
		"vide":                   {nil, ErrFormat},
	}
	for name, c := range cases {
		if _, err := Parse(c.data, "011814630"); err != c.want {
			t.Errorf("%s : err = %v, attendu %v", name, err, c.want)
		}
	}
	// Sans IDBANK (autre institut SDMX) : identifiant non contrôlé, le reste oui.
	if got, err := Parse(sdmx(`<Series FREQ="M">`, ok), "x"); err != nil || len(got) != 1 {
		t.Fatalf("série SDMX sans IDBANK : %v", err)
	}
}

// Seules les valeurs définitives du dernier mois d'un trimestre comptent ; le plus récent l'emporte.
func TestLatest(t *testing.T) {
	parse := func(o ...string) []Observation {
		got, err := Parse(sdmx(series, o...), "011814630")
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	cases := []struct {
		name, after, want string
		obs               []Observation
	}{
		{"septembre provisoire : on garde juin", "2026-Q1", "2026-Q2",
			parse(obs("2026-09", "103.1", "P"), obs("2026-08", "103.3", "DEF"), obs("2026-06", "102.11", "DEF"))},
		{"août seul ne fait pas un trimestre", "2026-Q2", "",
			parse(obs("2026-08", "103.35", "DEF"), obs("2026-07", "102.68", "DEF"), obs("2026-06", "102.11", "DEF"))},
		{"trimestre manqué : on passe au plus récent", "2025-Q4", "2026-Q3",
			parse(obs("2026-03", "101", "DEF"), obs("2026-09", "103.07", "DEF"), obs("2026-06", "102.11", "DEF"))},
		{"décembre → T4", "2026-Q3", "2026-Q4", parse(obs("2026-12", "104", "DEF"))},
		{"rien de plus récent", "2026-Q4", "", parse(obs("2026-12", "104", "DEF"))},
		{"chaîne sans IPC", "", "2026-Q1", parse(obs("2026-03", "101", "DEF"))},
		{"aucune observation", "", "", nil},
	}
	for _, c := range cases {
		rec, ok := Latest(c.obs, c.after)
		if (c.want == "") == ok || rec.Period != c.want {
			t.Errorf("%s : %q (%v), attendu %q", c.name, rec.Period, ok, c.want)
		}
	}
}

func TestCheckFrequency(t *testing.T) {
	if CheckFrequency("quarterly") != nil {
		t.Fatal("quarterly refusé")
	}
	for _, f := range []string{"monthly", "Quarterly", "", "annual"} {
		if CheckFrequency(f) != ErrFrequency {
			t.Errorf("%q accepté", f)
		}
	}
}

func TestNewSourceRejectsURL(t *testing.T) {
	for _, u := range []string{
		"http://api.insee.fr/series/BDM/V1/data/SERIES_BDM/011814630",
		"ftp://api.insee.fr/x",
		"https://",
		"https://user:pass@api.insee.fr/x",
		"https://api.insee.fr/x#frag",
		"api.insee.fr/x",
		"://",
	} {
		if _, err := NewSource(u); err != ErrURL {
			t.Errorf("%q : err = %v", u, err)
		}
	}
}

// tlsSource pointe une Source vers un serveur de test TLS (son certificat est la seule autorité).
func tlsSource(t *testing.T, srv *httptest.Server, path string) *Source {
	t.Helper()
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	s, err := newSource(srv.URL+path, &tls.Config{RootCAs: pool})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestFetch(t *testing.T) {
	data := readFixture(t)
	var requests []*http.Request
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r)
		http.SetCookie(w, &http.Cookie{Name: "INSEE", Value: "suivi"})
		w.Header().Set("Content-Type", "application/xml;charset=UTF-8")
		_, _ = w.Write(data)
	}))
	defer srv.Close()
	s := tlsSource(t, srv, "/series/BDM/V1/data/SERIES_BDM/011814630")
	for i := 0; i < 2; i++ {
		got, err := s.Fetch(context.Background())
		if err != nil || len(got) != 14 {
			t.Fatalf("lecture %d : %v", i, err)
		}
	}
	r := requests[1]
	if r.URL.Query().Get("lastNObservations") != "12" || r.Header.Get("User-Agent") != "etl-node" {
		t.Fatalf("requête : %s, User-Agent %q", r.URL, r.Header.Get("User-Agent"))
	}
	if len(r.Cookies()) != 0 {
		t.Fatal("cookie renvoyé : aucun suivi ne doit être conservé")
	}
	if len(r.Header) != 3 || r.Header.Get("Accept") != "application/xml" || r.Header.Get("Accept-Encoding") != "gzip" {
		t.Fatalf("en-têtes superflus : %v", r.Header)
	}
}

func TestFetchRejects(t *testing.T) {
	data := readFixture(t)
	other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write(data)
	}))
	defer other.Close()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/erreur":
			http.Error(w, "indisponible", http.StatusServiceUnavailable)
		case "/erreur-xml": // statut d'erreur avec un corps SDMX valide : refusé sur le statut seul
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write(data)
		case "/json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(data)
		case "/enorme":
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write(bytes.Repeat([]byte(" "), maxBody+1))
		case "/ailleurs":
			http.Redirect(w, r, other.URL+"/011814630", http.StatusFound)
		case "/boucle":
			http.Redirect(w, r, "/boucle", http.StatusFound)
		case "/ici":
			http.Redirect(w, r, "/011814630", http.StatusFound)
		case "/lent":
			time.Sleep(300 * time.Millisecond)
		default:
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write(data)
		}
	}))
	srv.Config.ErrorLog = log.New(io.Discard, "", 0) // le refus de certificat ci-dessous est attendu
	defer srv.Close()

	// Redirection sur le même hôte : suivie (le dernier segment reste l'identifiant attendu).
	if _, err := tlsSource(t, srv, "/ici").Fetch(context.Background()); err != ErrSeries {
		t.Fatalf("redirection interne : err = %v (la série attendue reste celle de l'URL configurée)", err)
	}
	for path, want := range map[string]error{
		"/erreur":     ErrHTTP,
		"/erreur-xml": ErrHTTP,
		"/json":       ErrHTTP,
		"/enorme":     ErrTooLarge,
		"/ailleurs":   ErrHTTP,
		"/boucle":     ErrHTTP,
		"/999999999":  ErrSeries,
	} {
		if _, err := tlsSource(t, srv, path).Fetch(context.Background()); !errors.Is(err, want) {
			t.Errorf("%s : err = %v, attendu %v", path, err, want)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := tlsSource(t, srv, "/lent").Fetch(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("délai dépassé : err = %v", err)
	}
	// Certificat inconnu : refusé (pas d'autorité de test).
	s, _ := NewSource(srv.URL + "/011814630")
	if _, err := s.Fetch(context.Background()); err == nil {
		t.Error("certificat non reconnu accepté")
	}
}

// Fuzzing de l'analyseur : jamais de panique ; toute observation acceptée a des octets bruts
// présents tels quels dans l'entrée, un mois valide et une valeur non nulle.
func FuzzParse(f *testing.F) {
	f.Add(readFixture(f))
	f.Add(sdmx(series, obs("2026-09", "103.07", "DEF")))
	f.Add(sdmx(series, `<Obs TIME_PERIOD="2026-09" OBS_VALUE="1"></Obs>`))
	f.Add([]byte(`<Series FREQ="M"><Obs TIME_PERIOD="2026-12" OBS_VALUE="0.001"/></Series>`))
	f.Fuzz(func(t *testing.T, data []byte) {
		got, err := Parse(data, "011814630")
		if err != nil {
			return
		}
		for _, o := range got {
			if !bytes.Contains(data, o.Raw) || !monthRe.MatchString(o.Month) || o.Value == 0 {
				t.Fatalf("observation incohérente : %+v", o)
			}
			_ = sha256.Sum256(o.Raw)
		}
	})
}
