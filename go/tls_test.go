// Copyright (c) 2026 The Speedle Authors
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl.

package speedle

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestUntrustedServerIsNotRetried(t *testing.T) {
	s := newScript(t)
	rec := &recorder{}
	c, _ := testClient(t, s, func(c *Config) { c.RootCAs = x509.NewCertPool(); c.Hooks = rec.hooks() })
	_, err := c.IsAllowed(bg, User("a"), "get", "/r", nil)
	if !errors.Is(err, ErrNetwork) || errors.Is(err, ErrTimeout) || len(rec.attempts) != 1 || rec.giveUps[0].Reason != GiveUpNonRetryable {
		t.Fatalf("%v, attempts %+v, give-ups %+v", err, rec.attempts, rec.giveUps)
	}
}

func TestConnectionRefusedIsRetried(t *testing.T) {
	s := newScript(t)
	url := s.srv.URL
	s.srv.Close()
	rec := &recorder{}
	c, _ := testClient(t, s, func(c *Config) { c.Endpoint = url; c.Hooks = rec.hooks() })
	_, err := c.IsAllowed(bg, User("a"), "get", "/r", nil)
	if !errors.Is(err, ErrNetwork) || len(rec.attempts) != 3 || rec.giveUps[0].Reason != GiveUpAttemptsExhausted {
		t.Fatalf("%v, attempts %+v", err, rec.attempts)
	}
}

func newCert(t *testing.T, cn string, ca *x509.Certificate, caKey *ecdsa.PrivateKey, client bool) (*x509.Certificate, tls.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	parent, signer := tmpl, key
	if ca == nil {
		tmpl.IsCA, tmpl.BasicConstraintsValid = true, true
	} else {
		parent, signer = ca, caKey
	}
	if !client {
		tmpl.ExtKeyUsage = nil
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, &key.PublicKey, signer)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	return cert, tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func TestMutualTLS(t *testing.T) {
	caCert, caPair := newCert(t, "client CA", nil, nil, false)
	_, clientPair := newCert(t, "client", caCert, caPair.PrivateKey.(*ecdsa.PrivateKey), true)
	clientCAs := x509.NewCertPool()
	clientCAs.AddCert(caCert)

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		if len(r.TLS.PeerCertificates) == 0 || r.TLS.PeerCertificates[0].Subject.CommonName != "client" {
			w.WriteHeader(500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, okBody)
	}))
	srv.TLS = &tls.Config{ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: clientCAs}
	srv.StartTLS()
	defer srv.Close()
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())

	c, err := NewClient(Config{Backend: BackendSpeedle, Endpoint: srv.URL, Auth: MutualTLS(clientPair), ServiceName: "s", RootCAs: roots})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if ok, err := c.IsAllowed(bg, User("a"), "get", "/r", nil); !ok || err != nil {
		t.Fatalf("with a client certificate: %v %v", ok, err)
	}

	rec := &recorder{}
	plain, err := NewClient(Config{Backend: BackendSpeedle, Endpoint: srv.URL, Auth: NoAuth(), ServiceName: "s", RootCAs: roots, Hooks: rec.hooks()})
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
	_, err = plain.IsAllowed(bg, User("a"), "get", "/r", nil)
	if !errors.Is(err, ErrNetwork) || len(rec.attempts) != 1 {
		t.Fatalf("without a client certificate: %v after %d attempts", err, len(rec.attempts))
	}
}
