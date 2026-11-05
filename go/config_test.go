// Copyright (c) 2026 The Speedle Authors
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl.

package speedle

import (
	"crypto/tls"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
)

func TestNewClientRejectsBadConfig(t *testing.T) {
	base := func() Config {
		return Config{Backend: BackendSpeedleCloud, Endpoint: "https://cloud.speedle.io", Auth: BearerToken("t"), ServiceName: "books"}
	}
	cases := map[string]func(*Config){
		"no backend":         func(c *Config) { c.Backend = "" },
		"unknown backend":    func(c *Config) { c.Backend = "other" },
		"no auth":            func(c *Config) { c.Auth = nil },
		"empty token":        func(c *Config) { c.Auth = BearerToken("") },
		"token with space":   func(c *Config) { c.Auth = BearerToken("a b") },
		"token with newline": func(c *Config) { c.Auth = BearerToken("a\nb") },
		"empty mtls":         func(c *Config) { c.Auth = MutualTLS(tls.Certificate{}) },
		"no endpoint":        func(c *Config) { c.Endpoint = "" },
		"no host":            func(c *Config) { c.Endpoint = "https://" },
		"query":              func(c *Config) { c.Endpoint = "https://h/?a=1" },
		"empty query":        func(c *Config) { c.Endpoint = "https://h/?" },
		"fragment":           func(c *Config) { c.Endpoint = "https://h/#x" },
		"user info":          func(c *Config) { c.Endpoint = "https://u:p@h/" },
		"ftp":                func(c *Config) { c.Endpoint = "ftp://h/" },
		"http cloud":         func(c *Config) { c.Endpoint = "http://127.0.0.1:1" },
		"http bearer plus":   func(c *Config) { c.Backend = BackendSpeedlePlus; c.Endpoint = "http://127.0.0.1:1" },
		"http remote plus": func(c *Config) {
			c.Backend, c.Auth, c.Endpoint = BackendSpeedlePlus, NoAuth(), "http://ads.internal:6734"
		},
		"no service":           func(c *Config) { c.ServiceName = "" },
		"negative attempts":    func(c *Config) { c.Retry.MaxAttempts = -1 },
		"negative deadline":    func(c *Config) { c.Retry.Deadline = -1 },
		"bad failure mode":     func(c *Config) { c.FailureMode = 7 },
		"negative breaker":     func(c *Config) { c.CircuitBreaker = &CircuitBreaker{FailureThreshold: -1} },
		"control in UA suffix": func(c *Config) { c.UserAgentSuffix = "a\r\nX-Evil: 1" },
		"bearer over http even with AllowInsecureHTTP": func(c *Config) { c.Backend, c.Endpoint, c.AllowInsecureHTTP = BackendSpeedlePlus, "http://h", true },
	}
	for name, mutate := range cases {
		cfg := base()
		mutate(&cfg)
		if _, err := NewClient(cfg); !errors.Is(err, ErrConfiguration) {
			t.Errorf("%s: got %v, want ErrConfiguration", name, err)
		}
	}
}

func TestNewClientAcceptsEndpoints(t *testing.T) {
	cases := []struct {
		backend  Backend
		auth     Auth
		endpoint string
		insecure bool
		url      string
	}{
		{BackendSpeedleCloud, BearerToken("t"), "https://cloud.speedle.io", false, "https://cloud.speedle.io/authz-check/v1/is-allowed"},
		{BackendSpeedleCloud, BearerToken("t"), "https://gw.example/prefix/", false, "https://gw.example/prefix/authz-check/v1/is-allowed"},
		{BackendSpeedlePlus, NoAuth(), "http://127.0.0.1:6734", false, "http://127.0.0.1:6734/authz-check/v1/is-allowed"},
		{BackendSpeedlePlus, NoAuth(), "http://localhost:6734", false, "http://localhost:6734/authz-check/v1/is-allowed"},
		{BackendSpeedlePlus, NoAuth(), "http://[::1]:6734", false, "http://[::1]:6734/authz-check/v1/is-allowed"},
		{BackendSpeedlePlus, NoAuth(), "http://ads.internal:6734", true, "http://ads.internal:6734/authz-check/v1/is-allowed"},
		{BackendSpeedlePlus, BearerToken("t"), "https://gw.internal", false, "https://gw.internal/authz-check/v1/is-allowed"},
	}
	for _, c := range cases {
		cl, err := NewClient(Config{Backend: c.backend, Endpoint: c.endpoint, Auth: c.auth, ServiceName: "s", AllowInsecureHTTP: c.insecure})
		if err != nil {
			t.Errorf("%s: %v", c.endpoint, err)
			continue
		}
		if cl.url != c.url {
			t.Errorf("%s: url %s, want %s", c.endpoint, cl.url, c.url)
		}
	}
}

func TestDefaults(t *testing.T) {
	c, err := NewClient(Config{Backend: BackendSpeedleCloud, Endpoint: "https://h", Auth: BearerToken("t"), ServiceName: "s"})
	if err != nil {
		t.Fatal(err)
	}
	want := RetryPolicy{MaxAttempts: 3, BaseDelay: 1e8, MaxDelay: 2e9, AttemptTimeout: 2e9, Deadline: 5e9}
	if c.retry != want || c.failureMode != FailureModeError || c.breaker != nil {
		t.Errorf("defaults: %+v %v %v", c.retry, c.failureMode, c.breaker)
	}
	b := newBreaker(CircuitBreaker{})
	if b.threshold != 5 || b.coolDown != 3e10 {
		t.Errorf("breaker defaults: %d %v", b.threshold, b.coolDown)
	}
}

func TestUserAgent(t *testing.T) {
	c, err := NewClient(Config{Backend: BackendSpeedleCloud, Endpoint: "https://h", Auth: BearerToken("t"), ServiceName: "s", UserAgentSuffix: "my-app/2"})
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`^speedle-sdk-go/0\.1\.0 \(go/[0-9][^;]*; [a-z0-9]+\) my-app/2$`)
	if !re.MatchString(c.userAgent) {
		t.Errorf("User-Agent %q", c.userAgent)
	}
}

func TestTokenIsRedacted(t *testing.T) {
	a := BearerToken("spdl_secret")
	cfg := Config{Auth: a}
	for _, s := range []string{fmt.Sprint(a), fmt.Sprintf("%v %+v %#v", a, cfg, a)} {
		if strings.Contains(s, "spdl_secret") {
			t.Errorf("token printed: %s", s)
		}
	}
	_, err := NewClient(Config{Backend: BackendSpeedleCloud, Endpoint: "http://h", Auth: BearerToken("spdl_secret"), ServiceName: "s"})
	if err == nil || strings.Contains(err.Error(), "spdl_secret") {
		t.Errorf("error %v", err)
	}
}

func TestErrorKinds(t *testing.T) {
	cases := []struct {
		kind ErrorKind
		is   []error
		not  []error
	}{
		{KindRateLimited, []error{ErrRateLimited, ErrTooManyRequests}, []error{ErrQuotaExceeded}},
		{KindQuotaExceeded, []error{ErrQuotaExceeded, ErrTooManyRequests}, []error{ErrRateLimited}},
		{KindDeadlineExceeded, []error{ErrDeadlineExceeded, ErrTimeout, ErrNetwork}, []error{ErrServer}},
		{KindTimeout, []error{ErrTimeout, ErrNetwork}, []error{ErrDeadlineExceeded}},
		{KindServer, []error{ErrServer}, []error{ErrServiceUnavailable}},
	}
	for _, c := range cases {
		var err error = fmt.Errorf("wrapped: %w", &Error{Kind: c.kind})
		for _, s := range c.is {
			if !errors.Is(err, s) {
				t.Errorf("%s should be %v", c.kind, s)
			}
		}
		for _, s := range c.not {
			if errors.Is(err, s) {
				t.Errorf("%s should not be %v", c.kind, s)
			}
		}
	}
	for kind := range sentinels {
		if !errors.Is(&Error{Kind: kind}, sentinels[kind]) {
			t.Errorf("%s does not match its sentinel", kind)
		}
	}
	e := &Error{Kind: KindForbidden, Status: 403, Code: "service_mismatch"}
	if e.Error() != "speedle: ForbiddenError (HTTP 403) service_mismatch" {
		t.Errorf("Error() = %q", e.Error())
	}
}

func TestReasonString(t *testing.T) {
	if DiscoverMode.String() != "DISCOVER_MODE" || Reason(42).String() != "Reason(42)" {
		t.Errorf("%s %s", DiscoverMode, Reason(42))
	}
}
