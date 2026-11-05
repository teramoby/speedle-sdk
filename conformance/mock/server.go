// Copyright (c) 2026 The Speedle Authors
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl.

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
)

const isAllowedPath = "/authz-check/v1/is-allowed"

// vector holds the parts of a conformance vector the mock needs. The rest of
// the vector (config, calls, expectations, events) is the runner's business.
type vector struct {
	ID        string     `json:"id"`
	Exchanges []exchange `json:"exchanges"`
}

type exchange struct {
	Request  expectedRequest  `json:"request"`
	Response scriptedResponse `json:"response"`
}

type expectedRequest struct {
	Method          string                   `json:"method"`
	Path            string                   `json:"path"`
	Headers         map[string]headerMatcher `json:"headers"`
	Body            json.RawMessage          `json:"body"`
	UnorderedArrays []string                 `json:"unorderedArrays"`

	body any // parsed Body
}

type scriptedResponse struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    json.RawMessage   `json:"body"`
	RawBody *string           `json:"rawBody"`
	Fault   string            `json:"fault"`
}

const (
	faultConnectionReset = "connection_reset"
	faultNoResponse      = "no_response"
)

func parseVector(b []byte) (*vector, error) {
	var v vector
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, fmt.Errorf("parse vector: %w", err)
	}
	if v.Exchanges == nil {
		return nil, errors.New("parse vector: exchanges is required")
	}
	for i := range v.Exchanges {
		ex := &v.Exchanges[i]
		if ex.Request.Method == "" || ex.Request.Path == "" || ex.Request.Body == nil {
			return nil, fmt.Errorf("exchange %d: request needs method, path and body", i)
		}
		body, err := decodeJSON(ex.Request.Body)
		if err != nil {
			return nil, fmt.Errorf("exchange %d: request body: %w", i, err)
		}
		ex.Request.body = body
		r := ex.Response
		switch r.Fault {
		case "":
			if r.Status < 100 || r.Status > 599 {
				return nil, fmt.Errorf("exchange %d: response status %d out of range", i, r.Status)
			}
			if r.Body != nil && r.RawBody != nil {
				return nil, fmt.Errorf("exchange %d: response has both body and rawBody", i)
			}
		case faultConnectionReset, faultNoResponse:
			if r.Status != 0 || r.Body != nil || r.RawBody != nil || r.Headers != nil {
				return nil, fmt.Errorf("exchange %d: a fault response has no other fields", i)
			}
		default:
			return nil, fmt.Errorf("exchange %d: unknown fault %q", i, r.Fault)
		}
	}
	return &v, nil
}

// verifyReport is what GET /verify returns.
type verifyReport struct {
	OK       bool     `json:"ok"`
	Vector   string   `json:"vector"`
	Expected int      `json:"expected"`
	Received int      `json:"received"`
	Errors   []string `json:"errors"`
}

// mock is the scripted server state. One vector is loaded at a time; loading
// another resets everything and releases requests held by no_response.
type mock struct {
	mu      sync.Mutex
	vec     *vector
	next    int
	errors  []string
	release chan struct{} // closed on reset to free no_response handlers
}

func newMock() *mock {
	return &mock{vec: &vector{Exchanges: []exchange{}}, release: make(chan struct{})}
}

func (m *mock) load(v *vector) {
	m.mu.Lock()
	close(m.release)
	m.release = make(chan struct{})
	m.vec, m.next, m.errors = v, 0, nil
	m.mu.Unlock()
}

func (m *mock) report() verifyReport {
	m.mu.Lock()
	defer m.mu.Unlock()
	errs := append([]string{}, m.errors...)
	if m.next < len(m.vec.Exchanges) {
		errs = append(errs, fmt.Sprintf("%d of %d expected requests were not received", len(m.vec.Exchanges)-m.next, len(m.vec.Exchanges)))
	}
	received := m.next
	if received > len(m.vec.Exchanges) {
		received = len(m.vec.Exchanges)
	}
	return verifyReport{
		OK:       len(errs) == 0,
		Vector:   m.vec.ID,
		Expected: len(m.vec.Exchanges),
		Received: received,
		Errors:   errs,
	}
}

// take consumes the next exchange for a received request and records any
// mismatch. It returns nil when the request was not expected at all.
func (m *mock) take(r *http.Request, body []byte) (*exchange, <-chan struct{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	index := m.next
	m.next++
	if index >= len(m.vec.Exchanges) {
		m.errors = append(m.errors, fmt.Sprintf("request %d: unexpected request (%d expected)", index+1, len(m.vec.Exchanges)))
		return nil, m.release
	}
	ex := &m.vec.Exchanges[index]
	var problems []string
	if r.Method != ex.Request.Method {
		problems = append(problems, fmt.Sprintf("method: want %s, got %s", ex.Request.Method, r.Method))
	}
	if r.URL.Path != ex.Request.Path {
		problems = append(problems, fmt.Sprintf("path: want %s, got %s", ex.Request.Path, r.URL.Path))
	}
	if r.URL.RawQuery != "" {
		problems = append(problems, fmt.Sprintf("query: want none, got %q", r.URL.RawQuery))
	}
	problems = append(problems, matchHeaders(ex.Request.Headers, r.Header)...)
	got, err := decodeJSON(body)
	if err != nil {
		problems = append(problems, fmt.Sprintf("body: not JSON: %v", err))
	} else {
		unordered := map[string]bool{}
		for _, p := range ex.Request.UnorderedArrays {
			unordered[p] = true
		}
		problems = append(problems, compareJSON(ex.Request.body, got, "", unordered)...)
	}
	for _, p := range problems {
		m.errors = append(m.errors, fmt.Sprintf("request %d: %s", index+1, p))
	}
	return ex, m.release
}

// serveSDK answers the SDK under test.
func (m *mock) serveSDK(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		m.mu.Lock()
		m.errors = append(m.errors, fmt.Sprintf("reading request body: %v", err))
		m.mu.Unlock()
		return
	}
	ex, release := m.take(r, body)
	if ex == nil {
		writeJSON(w, http.StatusTeapot, map[string]string{"error": "conformance mock: unexpected request"})
		return
	}
	resp := ex.Response
	switch resp.Fault {
	case faultConnectionReset:
		resetConnection(w)
		return
	case faultNoResponse:
		// Hold the request until the client gives up and closes the
		// connection, or the next vector is loaded.
		select {
		case <-r.Context().Done():
		case <-release:
		}
		resetConnection(w)
		return
	}
	for name, value := range resp.Headers {
		w.Header().Set(name, value)
	}
	switch {
	case resp.RawBody != nil:
		w.WriteHeader(resp.Status)
		_, _ = io.WriteString(w, *resp.RawBody)
	case resp.Body != nil:
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", "application/json")
		}
		w.WriteHeader(resp.Status)
		_, _ = w.Write(resp.Body)
	default:
		w.WriteHeader(resp.Status)
	}
}

// resetConnection closes the client connection with a TCP RST, so the client
// sees a connection reset before any response byte.
func resetConnection(w http.ResponseWriter) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		panic(http.ErrAbortHandler)
	}
	conn, _, err := hj.Hijack()
	if err != nil {
		panic(http.ErrAbortHandler)
	}
	raw := conn
	if nc, ok := conn.(interface{ NetConn() net.Conn }); ok {
		raw = nc.NetConn()
	}
	if tcp, ok := raw.(*net.TCPConn); ok {
		_ = tcp.SetLinger(0)
	}
	_ = raw.Close()
}

// controlHandler is the plain-HTTP control API used by runners.
func (m *mock) controlHandler(caPEM []byte, shutdown func()) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /load", func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		v, err := parseVector(b)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		m.load(v)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /reset", func(w http.ResponseWriter, r *http.Request) {
		m.load(&vector{Exchanges: []exchange{}})
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /verify", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, m.report())
	})
	mux.HandleFunc("GET /ca.pem", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-pem-file")
		_, _ = w.Write(caPEM)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /shutdown", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
		go shutdown()
	})
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	_ = enc.Encode(v)
}

// sdkHandler serves every path, so a wrong path is reported as a mismatch
// instead of a 404 the SDK might handle.
func (m *mock) sdkHandler() http.Handler {
	return http.HandlerFunc(m.serveSDK)
}
