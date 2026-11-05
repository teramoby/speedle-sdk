// Copyright (c) 2026 The Speedle Authors
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl.

package speedle

// The conformance runner (design §11): it runs every vector in
// ../conformance/vectors against the shared mock in ../conformance/mock.
// `go test -short` skips it; CI runs it with `go test -run TestConformance`.
// SPEEDLE_MOCK_DIR overrides the mock's source directory.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/teramoby/speedle-sdk/go/internal/clock"
)

type mockInfo struct {
	Endpoint string `json:"endpoint"`
	Control  string `json:"control"`
	CAPEM    string `json:"caPem"`
}

func startMock(t *testing.T) mockInfo {
	t.Helper()
	dir := os.Getenv("SPEEDLE_MOCK_DIR")
	if dir == "" {
		dir = filepath.Join("..", "conformance", "mock")
	}
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		t.Fatalf("conformance mock not found in %s: %v", dir, err)
	}
	bin := filepath.Join(t.TempDir(), "speedle-mock")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = dir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build mock: %v\n%s", err, out)
	}
	cmd := exec.Command(bin, "-exit-on-stdin-close")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Wait()
	})
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatalf("read mock start-up line: %v", err)
	}
	var info mockInfo
	if err := json.Unmarshal([]byte(line), &info); err != nil {
		t.Fatalf("mock start-up line %q: %v", line, err)
	}
	return info
}

type vectorFile struct {
	ID             string          `json:"id"`
	Backend        Backend         `json:"backend"`
	Config         vectorConfig    `json:"config"`
	JitterFraction *float64        `json:"jitterFraction"`
	Call           *vectorCall     `json:"call"`
	Expect         json.RawMessage `json:"expect"`
	Steps          []struct {
		AdvanceMs int64           `json:"advanceMs"`
		Call      vectorCall      `json:"call"`
		Expect    json.RawMessage `json:"expect"`
	} `json:"steps"`
	Events []map[string]any `json:"events"`
}

type vectorConfig struct {
	Auth struct {
		Type  string `json:"type"`
		Token string `json:"token"`
	} `json:"auth"`
	ServiceName string `json:"serviceName"`
	Retry       struct {
		MaxAttempts      *int   `json:"maxAttempts"`
		BaseDelayMs      *int64 `json:"baseDelayMs"`
		MaxDelayMs       *int64 `json:"maxDelayMs"`
		AttemptTimeoutMs *int64 `json:"attemptTimeoutMs"`
		DeadlineMs       *int64 `json:"deadlineMs"`
	} `json:"retry"`
	FailureMode    string `json:"failureMode"`
	CircuitBreaker *struct {
		FailureThreshold int   `json:"failureThreshold"`
		CoolDownMs       int64 `json:"coolDownMs"`
	} `json:"circuitBreaker"`
}

type vectorCall struct {
	Method      string          `json:"method"`
	Subject     json.RawMessage `json:"subject"`
	Action      string          `json:"action"`
	Resource    string          `json:"resource"`
	ServiceName *string         `json:"serviceName"`
	Attributes  []struct {
		Name  string `json:"name"`
		Type  string `json:"type"`
		Value any    `json:"value"`
	} `json:"attributes"`
	InferredAttributes map[string]any `json:"inferredAttributes"`
}

func TestConformance(t *testing.T) {
	if testing.Short() {
		t.Skip("conformance runner; run without -short")
	}
	mock := startMock(t)
	files, err := filepath.Glob(filepath.Join("..", "conformance", "vectors", "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no vectors found: %v", err)
	}
	for _, file := range files {
		t.Run(strings.TrimSuffix(filepath.Base(file), ".json"), func(t *testing.T) {
			raw, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			runVector(t, mock, raw)
		})
	}
}

func ms(v int64) time.Duration {
	if v == 0 {
		// Zero means "default" in RetryPolicy; the smallest non-zero value
		// rounds to the same 0 ms the vector asks for.
		return time.Nanosecond
	}
	return time.Duration(v) * time.Millisecond
}

func runVector(t *testing.T, mock mockInfo, raw []byte) {
	var v vectorFile
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(mock.Control+"/load", "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("mock load: %d %s", resp.StatusCode, body)
	}

	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM([]byte(mock.CAPEM))
	cfg := Config{
		Backend:     v.Backend,
		Endpoint:    mock.Endpoint,
		ServiceName: v.Config.ServiceName,
		RootCAs:     pool,
	}
	switch v.Config.Auth.Type {
	case "none":
		cfg.Auth = NoAuth()
	case "bearer":
		cfg.Auth = BearerToken(v.Config.Auth.Token)
	default:
		t.Fatalf("auth type %q", v.Config.Auth.Type)
	}
	r := v.Config.Retry
	if r.MaxAttempts != nil {
		cfg.Retry.MaxAttempts = *r.MaxAttempts
	}
	if r.BaseDelayMs != nil {
		cfg.Retry.BaseDelay = ms(*r.BaseDelayMs)
	}
	if r.MaxDelayMs != nil {
		cfg.Retry.MaxDelay = ms(*r.MaxDelayMs)
	}
	if r.AttemptTimeoutMs != nil {
		cfg.Retry.AttemptTimeout = ms(*r.AttemptTimeoutMs)
	}
	if r.DeadlineMs != nil {
		cfg.Retry.Deadline = ms(*r.DeadlineMs)
	}
	if v.Config.FailureMode == "closed" {
		cfg.FailureMode = FailureModeClosed
	}
	if cb := v.Config.CircuitBreaker; cb != nil {
		cfg.CircuitBreaker = &CircuitBreaker{FailureThreshold: cb.FailureThreshold, CoolDown: time.Duration(cb.CoolDownMs) * time.Millisecond}
	}
	var (
		mu     sync.Mutex
		events []map[string]any
	)
	add := func(e map[string]any) { mu.Lock(); events = append(events, e); mu.Unlock() }
	cfg.Hooks = Hooks{
		OnAttempt: func(e AttemptEvent) {
			add(map[string]any{"type": "attempt", "attempt": e.Attempt, "status": nullInt(e.Status), "errorType": nullStr(e.ErrorType)})
		},
		OnRetry: func(e RetryEvent) {
			add(map[string]any{"type": "retry", "attempt": e.Attempt, "delayMs": durMs(e.Delay), "errorType": e.ErrorType})
		},
		OnGiveUp: func(e GiveUpEvent) {
			add(map[string]any{"type": "giveUp", "attempts": e.Attempts, "reason": string(e.Reason), "errorType": string(e.Err.Kind)})
		},
		OnBreakerStateChange: func(e BreakerStateChangeEvent) {
			add(map[string]any{"type": "breaker", "from": string(e.From), "to": string(e.To)})
		},
	}
	client, err := NewClient(cfg)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()
	vclock := &clock.Virtual{}
	client.clock = vclock
	jitter := 1.0
	if v.JitterFraction != nil {
		jitter = *v.JitterFraction
	}
	client.random = func() float64 { return jitter }

	type step struct {
		advance int64
		call    vectorCall
		expect  json.RawMessage
	}
	var steps []step
	if v.Call != nil {
		steps = append(steps, step{call: *v.Call, expect: v.Expect})
	}
	for _, s := range v.Steps {
		steps = append(steps, step{advance: s.AdvanceMs, call: s.Call, expect: s.Expect})
	}
	for i, s := range steps {
		vclock.Advance(time.Duration(s.advance) * time.Millisecond)
		vclock.TakeSleeps()
		result, decision, callErr := runCall(t, client, s.call)
		var sleeps []any
		for _, d := range vclock.TakeSleeps() {
			sleeps = append(sleeps, durMs(d))
		}
		if p := compareOutcome(s.expect, result, decision, callErr, sleeps); len(p) > 0 {
			t.Errorf("step %d: %s", i+1, strings.Join(p, "; "))
		}
	}

	if v.Events != nil {
		mu.Lock()
		got := events
		mu.Unlock()
		if len(got) != len(v.Events) {
			t.Errorf("events: got %d, want %d\n got: %v\nwant: %v", len(got), len(v.Events), got, v.Events)
		} else {
			for i, want := range v.Events {
				if p := matchFields(want, got[i]); len(p) > 0 {
					t.Errorf("event %d: %s (got %v)", i+1, strings.Join(p, "; "), got[i])
				}
			}
		}
	}

	resp, err = http.Get(mock.Control + "/verify")
	if err != nil {
		t.Fatal(err)
	}
	var rep struct {
		OK     bool     `json:"ok"`
		Errors []string `json:"errors"`
	}
	err = json.NewDecoder(resp.Body).Decode(&rep)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !rep.OK {
		t.Errorf("mock verify:\n  %s", strings.Join(rep.Errors, "\n  "))
	}
}

func runCall(t *testing.T, c *Client, call vectorCall) (*bool, *Decision, error) {
	var subject Subject
	var name string
	if err := json.Unmarshal(call.Subject, &name); err == nil {
		subject = User(name)
	} else {
		var ps []struct{ Type, Name, IDD string }
		if err := json.Unmarshal(call.Subject, &ps); err != nil {
			t.Fatalf("subject: %v", err)
		}
		for _, p := range ps {
			subject = append(subject, Principal{Type: p.Type, Name: p.Name, IDD: p.IDD})
		}
	}
	var attrs Attributes
	for _, a := range call.Attributes {
		attrs = append(attrs, typedAttribute(t, a.Name, a.Type, a.Value))
	}
	if call.InferredAttributes != nil {
		inferred, err := Infer(call.InferredAttributes)
		if err != nil {
			return nil, nil, err
		}
		attrs = inferred
	}
	var opts []CallOption
	if call.ServiceName != nil {
		opts = append(opts, WithServiceName(*call.ServiceName))
	}
	ctx := context.Background()
	switch call.Method {
	case "isAllowed":
		ok, err := c.IsAllowed(ctx, subject, call.Action, call.Resource, attrs, opts...)
		if err != nil {
			return nil, nil, err
		}
		return &ok, nil, nil
	case "check":
		d, err := c.Check(ctx, subject, call.Action, call.Resource, attrs, opts...)
		return nil, d, err
	}
	t.Fatalf("method %q", call.Method)
	return nil, nil, nil
}

func typedAttribute(t *testing.T, name, typ string, value any) Attribute {
	items, isArray := value.([]any)
	parseTime := func(v any) time.Time {
		tm, err := time.Parse(time.RFC3339, v.(string))
		if err != nil {
			t.Fatalf("attribute %s: %v", name, err)
		}
		return tm
	}
	switch typ {
	case "string":
		if !isArray {
			return String(name, value.(string))
		}
		s := make([]string, len(items))
		for i, x := range items {
			s[i] = x.(string)
		}
		return Strings(name, s...)
	case "numeric":
		if !isArray {
			return Numeric(name, value.(float64))
		}
		s := make([]float64, len(items))
		for i, x := range items {
			s[i] = x.(float64)
		}
		return Numerics(name, s...)
	case "bool":
		if !isArray {
			return Bool(name, value.(bool))
		}
		s := make([]bool, len(items))
		for i, x := range items {
			s[i] = x.(bool)
		}
		return Bools(name, s...)
	case "datetime":
		if !isArray {
			return Datetime(name, parseTime(value))
		}
		s := make([]time.Time, len(items))
		for i, x := range items {
			s[i] = parseTime(x)
		}
		return Datetimes(name, s...)
	}
	t.Fatalf("attribute type %q", typ)
	return Attribute{}
}

func compareOutcome(rawExpect json.RawMessage, result *bool, decision *Decision, callErr error, sleeps []any) []string {
	var expect map[string]any
	if err := json.Unmarshal(rawExpect, &expect); err != nil {
		return []string{"bad expect: " + err.Error()}
	}
	var problems []string
	if want, ok := expect["delaysMs"]; ok {
		if sleeps == nil {
			sleeps = []any{}
		}
		if !jsonEqual(want, sleeps) {
			problems = append(problems, fmt.Sprintf("delaysMs: got %v, want %v", sleeps, want))
		}
	}
	switch {
	case expect["result"] != nil:
		if callErr != nil {
			return append(problems, fmt.Sprintf("want result %v, got error %v", expect["result"], callErr))
		}
		if result == nil || *result != expect["result"].(bool) {
			problems = append(problems, fmt.Sprintf("result: got %v, want %v", deref(result), expect["result"]))
		}
	case expect["decision"] != nil:
		if callErr != nil {
			return append(problems, fmt.Sprintf("want a decision, got error %v", callErr))
		}
		got := map[string]any{
			"allowed":             decision.Allowed,
			"reason":              int(decision.Reason),
			"errorMessage":        nullStr(decision.ErrorMessage),
			"quotaDailyRemaining": nullInt64(decision.QuotaDailyRemaining),
		}
		problems = append(problems, matchFields(expect["decision"].(map[string]any), got)...)
	case expect["error"] != nil:
		var e *Error
		if !errors.As(callErr, &e) {
			return append(problems, fmt.Sprintf("want error %v, got %v (result %v)", expect["error"], callErr, deref(result)))
		}
		got := map[string]any{
			"type":                string(e.Kind),
			"status":              nullInt(e.Status),
			"code":                nullStr(e.Code),
			"retryAfterSeconds":   nullFloat(e.RetryAfter.Seconds()),
			"quotaDailyRemaining": nullInt64(e.QuotaDailyRemaining),
		}
		problems = append(problems, matchFields(expect["error"].(map[string]any), got)...)
		if !errors.Is(callErr, sentinels[e.Kind]) {
			problems = append(problems, "errors.Is does not match the kind's sentinel")
		}
	default:
		problems = append(problems, "expect has no result, decision or error")
	}
	return problems
}

// matchFields checks every field listed in want; unlisted fields are not
// checked and null means empty.
func matchFields(want, got map[string]any) []string {
	keys := make([]string, 0, len(want))
	for k := range want {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var problems []string
	for _, k := range keys {
		g, ok := got[k]
		if !ok {
			problems = append(problems, fmt.Sprintf("%s: not recorded", k))
			continue
		}
		if !jsonEqual(want[k], g) {
			problems = append(problems, fmt.Sprintf("%s: got %v, want %v", k, g, want[k]))
		}
	}
	return problems
}

func jsonEqual(a, b any) bool {
	na, _ := json.Marshal(a)
	nb, _ := json.Marshal(b)
	var x, y any
	_ = json.Unmarshal(na, &x)
	_ = json.Unmarshal(nb, &y)
	return reflect.DeepEqual(x, y)
}

func durMs(d time.Duration) int64 { return int64(math.Round(float64(d) / float64(time.Millisecond))) }

func nullInt(v int) any {
	if v == 0 {
		return nil
	}
	return v
}

func nullInt64(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}

func nullFloat(v float64) any {
	if v == 0 {
		return nil
	}
	return v
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func deref(b *bool) any {
	if b == nil {
		return nil
	}
	return *b
}
