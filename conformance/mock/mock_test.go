// Copyright (c) 2026 The Speedle Authors
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl.

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func startTest(t *testing.T) *instance {
	t.Helper()
	inst, err := start("127.0.0.1", filepath.Join(t.TempDir(), "ca.pem"), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(inst.stop)
	return inst
}

func sdkClient(t *testing.T, inst *instance, timeout time.Duration) *http.Client {
	t.Helper()
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(inst.info.CAPEM)) {
		t.Fatal("CA PEM does not parse")
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func loadVector(t *testing.T, inst *instance, raw []byte) {
	t.Helper()
	resp, err := http.Post(inst.info.Control+"/load", "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("load: status %d: %s", resp.StatusCode, b)
	}
}

func verify(t *testing.T, inst *instance) verifyReport {
	t.Helper()
	resp, err := http.Get(inst.info.Control + "/verify")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var rep verifyReport
	if err := json.NewDecoder(resp.Body).Decode(&rep); err != nil {
		t.Fatal(err)
	}
	return rep
}

// sampleHeader gives a value that satisfies a pattern matcher in the vectors.
var sampleHeader = map[string]string{
	"user-agent":   "speedle-sdk-test/0.1.0 (go/1.26.0; linux)",
	"content-type": "application/json",
}

// TestReplayEveryVector plays each vector's expected requests against the
// mock, as a conforming SDK would send them, and checks the scripted answers.
func TestReplayEveryVector(t *testing.T) {
	inst := startTest(t)
	files, err := filepath.Glob("../vectors/*.json")
	if err != nil || len(files) == 0 {
		t.Fatalf("no vectors: %v", err)
	}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			raw, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			v, err := parseVector(raw)
			if err != nil {
				t.Fatal(err)
			}
			if want := strings.TrimSuffix(filepath.Base(file), ".json"); v.ID != want {
				t.Errorf("id %q, want %q", v.ID, want)
			}
			loadVector(t, inst, raw)
			for i, ex := range v.Exchanges {
				timeout := 5 * time.Second
				if ex.Response.Fault == faultNoResponse {
					timeout = 200 * time.Millisecond
				}
				client := sdkClient(t, inst, timeout)
				req, err := http.NewRequest(ex.Request.Method, inst.info.Endpoint+ex.Request.Path, bytes.NewReader(ex.Request.Body))
				if err != nil {
					t.Fatal(err)
				}
				for name, m := range ex.Request.Headers {
					switch {
					case m.exact != nil:
						req.Header.Set(name, *m.exact)
					case m.pattern != nil:
						s, ok := sampleHeader[strings.ToLower(name)]
						if !ok {
							t.Fatalf("no sample value for pattern header %s", name)
						}
						req.Header.Set(name, s)
					}
				}
				if _, set := req.Header["Content-Type"]; !set {
					req.Header.Set("Content-Type", "application/json")
				}
				resp, err := client.Do(req)
				switch ex.Response.Fault {
				case faultConnectionReset:
					if err == nil {
						resp.Body.Close()
						t.Fatalf("exchange %d: want a network error, got status %d", i, resp.StatusCode)
					}
					if !errors.Is(err, syscall.ECONNRESET) && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
						t.Logf("exchange %d: reset surfaced as %v", i, err)
					}
				case faultNoResponse:
					var ne net.Error
					if err == nil || !errors.As(err, &ne) || !ne.Timeout() {
						t.Fatalf("exchange %d: want a client timeout, got %v", i, err)
					}
				default:
					if err != nil {
						t.Fatalf("exchange %d: %v", i, err)
					}
					body, _ := io.ReadAll(resp.Body)
					resp.Body.Close()
					if resp.StatusCode != ex.Response.Status {
						t.Errorf("exchange %d: status %d, want %d", i, resp.StatusCode, ex.Response.Status)
					}
					for name, value := range ex.Response.Headers {
						if got := resp.Header.Get(name); got != value {
							t.Errorf("exchange %d: header %s = %q, want %q", i, name, got, value)
						}
					}
					if ex.Response.Body != nil {
						if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
							t.Errorf("exchange %d: Content-Type %q", i, ct)
						}
						want, _ := decodeJSON(ex.Response.Body)
						got, err := decodeJSON(body)
						if err != nil {
							t.Fatalf("exchange %d: body: %v", i, err)
						}
						if p := compareJSON(want, got, "", nil); len(p) > 0 {
							t.Errorf("exchange %d: body: %v", i, p)
						}
					}
					if ex.Response.RawBody != nil && string(body) != *ex.Response.RawBody {
						t.Errorf("exchange %d: raw body %q, want %q", i, body, *ex.Response.RawBody)
					}
				}
			}
			if rep := verify(t, inst); !rep.OK || rep.Received != len(v.Exchanges) {
				t.Fatalf("verify: %+v", rep)
			}
		})
	}
}

const oneExchange = `{
  "id": "t",
  "exchanges": [{
    "request": {
      "method": "POST", "path": "/authz-check/v1/is-allowed",
      "headers": {
        "Authorization": {"absent": true},
        "User-Agent": {"pattern": "^speedle-sdk-[a-z]+/.*$"},
        "X-Exact": "yes"
      },
      "body": {"subject": {"principals": [{"name": "a"}]}, "attributes": [{"name": "x"}, {"name": "y"}]},
      "unorderedArrays": ["/attributes"]
    },
    "response": {"status": 200, "body": {"allowed": true, "reason": 0}}
  }]
}`

func post(t *testing.T, inst *instance, path string, headers map[string]string, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, inst.info.Endpoint+path, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := sdkClient(t, inst, 5*time.Second).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp
}

func TestMatchingRequestPasses(t *testing.T) {
	inst := startTest(t)
	loadVector(t, inst, []byte(oneExchange))
	resp := post(t, inst, isAllowedPath, map[string]string{"User-Agent": "speedle-sdk-go/0.1.0", "x-exact": "yes"},
		`{"attributes": [{"name": "y"}, {"name": "x"}], "subject": {"principals": [{"name": "a"}]}}`)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if rep := verify(t, inst); !rep.OK {
		t.Fatalf("verify: %+v", rep)
	}
}

func TestMismatchesAreReported(t *testing.T) {
	inst := startTest(t)
	loadVector(t, inst, []byte(oneExchange))
	post(t, inst, "/wrong?q=1", map[string]string{
		"Authorization": "Bearer secret-token",
		"User-Agent":    "curl/8",
		"X-Exact":       "no",
	}, `{"subject": {"principals": [{"name": "b"}]}, "attributes": [{"name": "x"}], "extra": 1}`)
	resp := post(t, inst, isAllowedPath, nil, `{}`)
	if resp.StatusCode != http.StatusTeapot {
		t.Errorf("extra request: status %d, want 418", resp.StatusCode)
	}
	rep := verify(t, inst)
	if rep.OK {
		t.Fatal("verify passed")
	}
	all := strings.Join(rep.Errors, "\n")
	for _, want := range []string{
		"path: want /authz-check/v1/is-allowed, got /wrong",
		`query: want none`,
		"header Authorization: want absent",
		"header User-Agent:",
		`header X-Exact: want "yes", got "no"`,
		"/subject/principals/0/name",
		"/attributes: want 2 items, got 1",
		"/extra: unexpected member",
		"request 2: unexpected request",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in:\n%s", want, all)
		}
	}
	if strings.Contains(all, "secret-token") {
		t.Errorf("token leaked into the report:\n%s", all)
	}
}

func TestMissingRequestIsReported(t *testing.T) {
	inst := startTest(t)
	loadVector(t, inst, []byte(oneExchange))
	rep := verify(t, inst)
	if rep.OK || len(rep.Errors) != 1 || !strings.Contains(rep.Errors[0], "1 of 1 expected requests were not received") {
		t.Fatalf("verify: %+v", rep)
	}
}

func TestEmptyExchangesRejectsAnyRequest(t *testing.T) {
	inst := startTest(t)
	loadVector(t, inst, []byte(`{"id": "none", "exchanges": []}`))
	if rep := verify(t, inst); !rep.OK {
		t.Fatalf("verify before any request: %+v", rep)
	}
	post(t, inst, isAllowedPath, nil, `{}`)
	if rep := verify(t, inst); rep.OK {
		t.Fatal("verify passed after an unexpected request")
	}
}

func TestLoadResetsStateAndReleasesHeldRequests(t *testing.T) {
	inst := startTest(t)
	loadVector(t, inst, []byte(`{"id": "hold", "exchanges": [{"request": {"method": "POST", "path": "/authz-check/v1/is-allowed", "body": {}}, "response": {"fault": "no_response"}}]}`))
	errc := make(chan error, 1)
	go func() {
		resp, err := sdkClient(t, inst, 10*time.Second).Post(inst.info.Endpoint+isAllowedPath, "application/json", strings.NewReader(`{}`))
		if err == nil {
			resp.Body.Close()
		}
		errc <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for verify(t, inst).Received == 0 {
		if time.Now().After(deadline) {
			t.Fatal("request never arrived")
		}
		time.Sleep(10 * time.Millisecond)
	}
	loadVector(t, inst, []byte(`{"id": "next", "exchanges": []}`))
	select {
	case err := <-errc:
		if err == nil {
			t.Fatal("held request got a response")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("held request was not released by load")
	}
	if rep := verify(t, inst); !rep.OK || rep.Vector != "next" {
		t.Fatalf("verify after reload: %+v", rep)
	}
}

func TestLoadRejectsInvalidVectors(t *testing.T) {
	inst := startTest(t)
	for _, body := range []string{
		`not json`,
		`{"id": "x"}`,
		`{"exchanges": [{"request": {"method": "POST", "path": "/p"}, "response": {"status": 200}}]}`,
		`{"exchanges": [{"request": {"method": "POST", "path": "/p", "body": {}}, "response": {"status": 99}}]}`,
		`{"exchanges": [{"request": {"method": "POST", "path": "/p", "body": {}}, "response": {"fault": "explode"}}]}`,
		`{"exchanges": [{"request": {"method": "POST", "path": "/p", "body": {}}, "response": {"fault": "no_response", "status": 200}}]}`,
		`{"exchanges": [{"request": {"method": "POST", "path": "/p", "body": {}, "headers": {"A": {"pattern": "("}}}, "response": {"status": 200}}]}`,
		`{"exchanges": [{"request": {"method": "POST", "path": "/p", "body": {}, "headers": {"A": {"absent": false}}}, "response": {"status": 200}}]}`,
	} {
		resp, err := http.Post(inst.info.Control+"/load", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", body, resp.StatusCode)
		}
	}
}

func TestRawBodyAndHeaders(t *testing.T) {
	inst := startTest(t)
	loadVector(t, inst, []byte(`{"id": "raw", "exchanges": [{"request": {"method": "POST", "path": "/authz-check/v1/is-allowed", "body": {}},
	  "response": {"status": 502, "headers": {"Retry-After": "1"}, "rawBody": "<html>bad gateway</html>"}}]}`))
	resp, err := sdkClient(t, inst, 5*time.Second).Post(inst.info.Endpoint+isAllowedPath, "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 502 || string(b) != "<html>bad gateway</html>" || resp.Header.Get("Retry-After") != "1" {
		t.Fatalf("got %d %q %v", resp.StatusCode, b, resp.Header)
	}
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		t.Errorf("raw body sent as JSON")
	}
}

func TestCompareJSON(t *testing.T) {
	parse := func(s string) any {
		v, err := decodeJSON([]byte(s))
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	cases := []struct {
		want, got string
		unordered []string
		ok        bool
	}{
		{`{"a": 1, "b": [1, 2]}`, `{"b": [1, 2], "a": 1.0}`, nil, true},
		{`[1, 2]`, `[2, 1]`, nil, false},
		{`{"x": [1, 2, 2]}`, `{"x": [2, 1, 2]}`, []string{"/x"}, true},
		{`{"x": [1, 2, 2]}`, `{"x": [2, 1, 1]}`, []string{"/x"}, false},
		{`{"a~b/c": [1, 2]}`, `{"a~b/c": [2, 1]}`, []string{"/a~0b~1c"}, true},
		{`{"a": null}`, `{}`, nil, false},
		{`{"a": null}`, `{"a": null}`, nil, true},
		{`"1"`, `1`, nil, false},
		{`true`, `false`, nil, false},
		{`{"a": []}`, `{"a": {}}`, nil, false},
	}
	for _, c := range cases {
		unordered := map[string]bool{}
		for _, p := range c.unordered {
			unordered[p] = true
		}
		p := compareJSON(parse(c.want), parse(c.got), "", unordered)
		if (len(p) == 0) != c.ok {
			t.Errorf("compare %s with %s: problems %v, want ok=%v", c.want, c.got, p, c.ok)
		}
	}
}

// TestBinaryPrintsStartupLine builds the command and checks the contract the
// runners rely on: one JSON line on stdout, a working control port, and exit
// on stdin EOF.
func TestBinaryPrintsStartupLine(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	bin := filepath.Join(t.TempDir(), "mock")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-exit-on-stdin-close")
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	var info startupInfo
	if err := json.Unmarshal([]byte(line), &info); err != nil {
		t.Fatalf("startup line %q: %v", line, err)
	}
	if !strings.HasPrefix(info.Endpoint, "https://127.0.0.1:") || !strings.HasPrefix(info.Control, "http://127.0.0.1:") {
		t.Errorf("startup line %+v", info)
	}
	pem, err := os.ReadFile(info.CAFile)
	if err != nil || string(pem) != info.CAPEM || !strings.Contains(info.CAPEM, "BEGIN CERTIFICATE") {
		t.Errorf("CA file %s: %v", info.CAFile, err)
	}
	resp, err := http.Get(info.Control + "/healthz")
	if err != nil || resp.StatusCode != http.StatusNoContent {
		t.Fatalf("healthz: %v %v", resp, err)
	}
	resp.Body.Close()
	_ = stdin.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("exit: %v", err)
	}
	if _, err := os.Stat(info.CAFile); !os.IsNotExist(err) {
		t.Errorf("temporary CA file not removed: %v", err)
	}
}
