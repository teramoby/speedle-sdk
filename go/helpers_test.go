// Copyright (c) 2026 The Speedle Authors
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl.

package speedle

import (
	"context"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/teramoby/speedle-sdk/go/internal/clock"
)

// reply is one scripted answer of the test server.
type reply struct {
	status  int
	headers map[string]string
	body    string
	hang    bool // wait until the client gives up
}

// script is a TLS test server that answers requests in order and records them.
type script struct {
	srv     *httptest.Server
	mu      sync.Mutex
	replies []reply
	reqs    []*http.Request
	bodies  []string
}

func newScript(t *testing.T, replies ...reply) *script {
	t.Helper()
	s := &script{replies: replies}
	s.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.reqs = append(s.reqs, r)
		s.bodies = append(s.bodies, string(b))
		var rep reply
		if len(s.replies) > 0 {
			rep, s.replies = s.replies[0], s.replies[1:]
		} else {
			rep = reply{status: 599, body: `{"error":"unscripted"}`}
		}
		s.mu.Unlock()
		if rep.hang {
			<-r.Context().Done()
			return
		}
		for k, v := range rep.headers {
			w.Header().Set(k, v)
		}
		if rep.body != "" && w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", "application/json")
		}
		w.WriteHeader(rep.status)
		_, _ = io.WriteString(w, rep.body)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *script) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.reqs)
}

func (s *script) pool() *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(s.srv.Certificate())
	return p
}

// recorder captures hook events.
type recorder struct {
	mu       sync.Mutex
	attempts []AttemptEvent
	retries  []RetryEvent
	giveUps  []GiveUpEvent
	breaker  []BreakerStateChangeEvent
}

func (r *recorder) hooks() Hooks {
	return Hooks{
		OnAttempt: func(e AttemptEvent) { r.mu.Lock(); r.attempts = append(r.attempts, e); r.mu.Unlock() },
		OnRetry:   func(e RetryEvent) { r.mu.Lock(); r.retries = append(r.retries, e); r.mu.Unlock() },
		OnGiveUp:  func(e GiveUpEvent) { r.mu.Lock(); r.giveUps = append(r.giveUps, e); r.mu.Unlock() },
		OnBreakerStateChange: func(e BreakerStateChangeEvent) {
			r.mu.Lock()
			r.breaker = append(r.breaker, e)
			r.mu.Unlock()
		},
	}
}

const okBody = `{"allowed":true,"reason":0}`

// testClient builds a client for s with a virtual clock and jitter 1.
func testClient(t *testing.T, s *script, mutate func(*Config)) (*Client, *clock.Virtual) {
	t.Helper()
	cfg := Config{
		Backend:     BackendSpeedleCloud,
		Endpoint:    s.srv.URL,
		Auth:        BearerToken("spdl_test"),
		ServiceName: "books",
		RootCAs:     s.pool(),
	}
	if mutate != nil {
		mutate(&cfg)
	}
	c, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(c.Close)
	v := &clock.Virtual{}
	c.clock = v
	c.random = func() float64 { return 1 }
	return c, v
}

var bg = context.Background()
