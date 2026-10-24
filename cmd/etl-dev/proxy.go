//go:build dev

package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"
)

// devHost : nom du site public de développement (Chrome le résout vers 127.0.0.1).
const devHost = "etalon.test"

// startProxy imite Caddy devant le site public : HTTPS (certificat autosigné jetable), Host du site,
// X-Forwarded-For remplacé par l'adresse du visiteur. Le port du proxy n'existe pas en production :
// l'Origin https://etalon.test:PORT envoyé par le navigateur est ramené à https://etalon.test.
func startProxy(tlsPort, apiPort int) (string, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", err
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: devHost}, DNSNames: []string{devHost},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return "", err
	}
	target, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", apiPort))
	origin := fmt.Sprintf("https://%s:%d", devHost, tlsPort)
	proxy := &httputil.ReverseProxy{Rewrite: func(r *httputil.ProxyRequest) {
		r.SetURL(target)
		r.SetXForwarded()
		r.Out.Host = devHost
		if r.In.Header.Get("Origin") == origin {
			r.Out.Header.Set("Origin", "https://"+devHost)
		}
	}}
	ln, err := tls.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", tlsPort), &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}})
	if err != nil {
		return "", err
	}
	go func() { _ = (&http.Server{Handler: proxy, ReadHeaderTimeout: 10 * time.Second}).Serve(ln) }()
	return origin + "/", nil
}
