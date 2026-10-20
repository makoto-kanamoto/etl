// Package oracle lit l'indice des prix à la source officielle configurée et fait voter le nœud pour
// le dernier trimestre publié. Le consensus inscrit l'IPC quand plus de 2/3 des nœuds ont voté pour
// le même triplet {valeur, période, empreinte} : l'oracle ne fait que lire, vérifier et voter.
package oracle

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Source SDMX-ML 2.1 « structure-specific » (INSEE et autres instituts) :
//
//	<Series IDBANK="011814630" FREQ="M" DECIMALS="2" …>
//	  <Obs TIME_PERIOD="2026-09" OBS_VALUE="103.07" OBS_QUAL="DEF" … />
//
// L'empreinte d'une observation est le SHA-256 des octets EXACTS de son élément <Obs …/>, tels que
// servis : jamais une ré-sérialisation, qui pourrait différer d'une implémentation à l'autre.

const (
	// Frequency est la seule fréquence acceptée aujourd'hui. Rendre la fréquence gouvernable (mensuel)
	// changerait le format des périodes dans le consensus : ce sera une étape dédiée, si elle est votée.
	Frequency = "quarterly"

	userAgent     = "etl-node" // neutre et constant : rien qui distingue un nœud d'un autre
	fetchTimeout  = 30 * time.Second
	maxBody       = 4 << 20
	maxRedirects  = 3
	lastNObs      = "12" // un an d'observations suffit à trouver le dernier trimestre
	maxDecimals   = 3    // valeur stockée × 1000
	valueScale    = 1000
	definitiveObs = "DEF"
)

var (
	ErrFrequency = errors.New("oracle: seule la fréquence « quarterly » est acceptée (mensuel : étape future, par vote)")
	ErrURL       = errors.New("oracle: URL de la source invalide (HTTPS obligatoire)")
	ErrHTTP      = errors.New("oracle: réponse HTTP inattendue")
	ErrTooLarge  = errors.New("oracle: réponse trop volumineuse")
	ErrFormat    = errors.New("oracle: réponse SDMX invalide")
	ErrSeries    = errors.New("oracle: série inattendue (identifiant, fréquence ou décimales)")
	ErrValue     = errors.New("oracle: valeur d'indice invalide")
)

// CheckFrequency valide ipc_frequency (config [oracle]).
func CheckFrequency(f string) error {
	if f != Frequency {
		return ErrFrequency
	}
	return nil
}

// Observation est une valeur mensuelle de l'indice.
type Observation struct {
	Month      string // "2026-09"
	Value      uint64 // indice × 1000 (103.07 → 103070)
	Definitive bool   // OBS_QUAL="DEF"
	Raw        []byte // octets exacts de l'élément <Obs …/>
}

// Source lit une série SDMX en HTTPS.
type Source struct {
	url    string // URL configurée, complétée de lastNObservations
	series string // dernier segment du chemin : identifiant attendu (IDBANK) s'il est fourni
	client *http.Client
}

// NewSource prépare la lecture de la série à l'URL configurée (ipc_url).
func NewSource(rawURL string) (*Source, error) {
	return newSource(rawURL, nil)
}

// newSource : tlsConfig nil = autorités de certification du système (les tests fournissent les leurs).
func newSource(rawURL string, tlsConfig *tls.Config) (*Source, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" {
		return nil, ErrURL
	}
	q := u.Query()
	q.Set("lastNObservations", lastNObs)
	u.RawQuery = q.Encode()
	if tlsConfig == nil {
		tlsConfig = &tls.Config{}
	}
	tlsConfig = tlsConfig.Clone()
	tlsConfig.MinVersion = tls.VersionTLS12
	transport := http.DefaultTransport.(*http.Transport).Clone() // HTTPS_PROXY respecté
	transport.TLSClientConfig = tlsConfig
	host := u.Host
	client := &http.Client{
		Timeout:   fetchTimeout,
		Transport: transport,
		Jar:       nil, // aucun cookie conservé
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects || req.URL.Scheme != "https" || req.URL.Host != host {
				return ErrHTTP
			}
			return nil
		},
	}
	return &Source{url: u.String(), series: path.Base(u.Path), client: client}, nil
}

// Fetch lit la série et renvoie ses observations.
func (s *Source) Fetch(ctx context.Context) ([]Observation, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/xml")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Content-Type"), "xml") {
		return nil, fmt.Errorf("%w : %s", ErrHTTP, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxBody {
		return nil, ErrTooLarge
	}
	return Parse(data, s.series)
}

var monthRe = regexp.MustCompile(`^[0-9]{4}-(0[1-9]|1[0-2])$`)

// Parse lit une réponse SDMX-ML contenant exactement une série mensuelle. Si la série porte un
// identifiant IDBANK, il doit valoir series. Chaque mois apparaît au plus une fois.
func Parse(data []byte, series string) ([]Observation, error) {
	d := xml.NewDecoder(bytes.NewReader(data))
	d.Strict = true
	var (
		obs     []Observation
		nSeries int
		inObs   bool
		start   int64
		cur     Observation
		seen    = map[string]bool{}
	)
	for {
		before := d.InputOffset()
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, ErrFormat
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "Series":
				nSeries++ // plus d'une série : refusé à la fin ; série dans une observation : à l'observation suivante
				if err := checkSeries(t.Attr, series); err != nil {
					return nil, err
				}
			case "Obs":
				if nSeries != 1 || inObs {
					return nil, ErrFormat
				}
				o, err := parseObs(t.Attr)
				if err != nil {
					return nil, err
				}
				if seen[o.Month] {
					return nil, ErrFormat
				}
				seen[o.Month] = true
				inObs, start, cur = true, before, o
			}
		case xml.EndElement:
			if t.Name.Local == "Obs" && inObs {
				cur.Raw = bytes.Clone(data[start:d.InputOffset()])
				obs = append(obs, cur)
				inObs = false
			}
		}
	}
	if nSeries != 1 || inObs {
		return nil, ErrFormat
	}
	return obs, nil
}

func attr(attrs []xml.Attr, name string) (string, bool) {
	for _, a := range attrs {
		if a.Name.Space == "" && a.Name.Local == name {
			return a.Value, true
		}
	}
	return "", false
}

func checkSeries(attrs []xml.Attr, series string) error {
	if id, ok := attr(attrs, "IDBANK"); ok && id != series {
		return ErrSeries
	}
	if f, _ := attr(attrs, "FREQ"); f != "M" {
		return ErrSeries
	}
	if dec, ok := attr(attrs, "DECIMALS"); ok {
		n, err := strconv.Atoi(dec)
		if err != nil || n < 0 || n > maxDecimals {
			return ErrSeries
		}
	}
	return nil
}

func parseObs(attrs []xml.Attr) (Observation, error) {
	month, _ := attr(attrs, "TIME_PERIOD")
	if !monthRe.MatchString(month) {
		return Observation{}, ErrFormat
	}
	raw, _ := attr(attrs, "OBS_VALUE")
	v, err := parseValue(raw)
	if err != nil {
		return Observation{}, err
	}
	q, _ := attr(attrs, "OBS_QUAL")
	return Observation{Month: month, Value: v, Definitive: q == definitiveObs}, nil
}

var valueRe = regexp.MustCompile(`^[0-9]{1,15}(\.[0-9]{1,3})?$`)

// parseValue convertit « 103.07 » en 103070, sans nombre flottant. Au plus 3 décimales, valeur > 0.
func parseValue(s string) (uint64, error) {
	if !valueRe.MatchString(s) {
		return 0, ErrValue
	}
	whole, frac, _ := strings.Cut(s, ".")
	w, _ := strconv.ParseUint(whole, 10, 64)
	f := uint64(0)
	if frac != "" {
		f, _ = strconv.ParseUint(frac+strings.Repeat("0", maxDecimals-len(frac)), 10, 64)
	}
	v := w*valueScale + f // 15 chiffres entiers × 1000 : pas de débordement
	if v == 0 {
		return 0, ErrValue
	}
	return v, nil
}
