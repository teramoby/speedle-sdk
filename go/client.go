// Copyright (c) 2026 The Speedle Authors
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl.

package speedle

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"

	"github.com/teramoby/speedle-sdk/go/internal/clock"
)

// IsAllowedPath is appended to the endpoint's base URL.
const IsAllowedPath = "/authz-check/v1/is-allowed"

// Config configures a [Client]. Backend, Endpoint, Auth and ServiceName are
// required.
type Config struct {
	Backend Backend
	// Endpoint is the base URL: scheme, host, optional port and optional
	// path prefix, for example "https://cloud.speedle.io". Query strings,
	// fragments and user info are rejected. It must be https, except that
	// http is accepted for BackendSpeedle with NoAuth on a loopback host,
	// or on any host with AllowInsecureHTTP.
	Endpoint string
	Auth     Auth
	// ServiceName is sent on every call; WithServiceName overrides it per
	// call. On Speedle Cloud it must be the token's service.
	ServiceName    string
	Retry          RetryPolicy
	FailureMode    FailureMode
	CircuitBreaker *CircuitBreaker // nil means no breaker
	Hooks          Hooks
	// RootCAs is the pool of trusted server roots; nil means the system
	// roots. As in crypto/tls it replaces the system pool, so to trust an
	// extra CA in addition to the system roots, start from
	// x509.SystemCertPool() and add the CA to it. Certificate verification
	// cannot be disabled.
	RootCAs           *x509.CertPool
	AllowInsecureHTTP bool
	// UserAgentSuffix is appended to the User-Agent after a space.
	UserAgentSuffix string
}

// Client calls is-allowed on one endpoint. It is safe for concurrent use;
// create it once and share it.
type Client struct {
	backend       Backend
	url           string
	serviceName   string
	authorization string
	userAgent     string
	retry         RetryPolicy
	failureMode   FailureMode
	breaker       *breaker
	hooks         Hooks
	transport     *http.Transport
	http          *http.Client

	// Test seam (design §11), set only by this package's conformance runner.
	clock  clock.Clock
	random func() float64 // uniform in [0, 1]
}

// CallOption changes one call.
type CallOption func(*callOptions)

type callOptions struct {
	serviceName *string
}

// WithServiceName overrides Config.ServiceName for this call. An empty name
// fails the call with ErrInvalidRequest.
func WithServiceName(name string) CallOption {
	return func(o *callOptions) { o.serviceName = &name }
}

// NewClient validates cfg and creates a client. Configuration errors wrap
// [ErrConfiguration].
func NewClient(cfg Config) (*Client, error) {
	switch cfg.Backend {
	case BackendSpeedleCloud, BackendSpeedle:
	case "":
		return nil, configError("Backend is required")
	default:
		return nil, configError("unknown Backend %q", string(cfg.Backend))
	}
	if cfg.Auth == nil {
		return nil, configError("Auth is required; use NoAuth() for none")
	}
	var tc transportConfig
	if err := cfg.Auth.apply(&tc); err != nil {
		return nil, err
	}
	endpoint, err := checkEndpoint(cfg, tc.kind)
	if err != nil {
		return nil, err
	}
	if cfg.ServiceName == "" {
		return nil, configError("ServiceName is required")
	}
	retry, err := retryDefaults(cfg.Retry)
	if err != nil {
		return nil, err
	}
	if cfg.FailureMode != FailureModeError && cfg.FailureMode != FailureModeClosed {
		return nil, configError("unknown FailureMode %d", int(cfg.FailureMode))
	}
	var br *breaker
	if cfg.CircuitBreaker != nil {
		if cfg.CircuitBreaker.FailureThreshold < 0 || cfg.CircuitBreaker.CoolDown < 0 {
			return nil, configError("CircuitBreaker values must not be negative")
		}
		br = newBreaker(*cfg.CircuitBreaker)
	}
	for _, r := range cfg.UserAgentSuffix {
		if r < 0x20 || r == 0x7f {
			return nil, configError("UserAgentSuffix contains control characters")
		}
	}
	ua := fmt.Sprintf("speedle-sdk-go/%s (go/%s; %s)", Version, strings.TrimPrefix(runtime.Version(), "go"), runtime.GOOS)
	if cfg.UserAgentSuffix != "" {
		ua += " " + cfg.UserAgentSuffix
	}

	transport := &http.Transport{
		Proxy:       http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{KeepAlive: 30 * time.Second}).DialContext,
		TLSClientConfig: &tls.Config{
			MinVersion:   tls.VersionTLS12,
			RootCAs:      cfg.RootCAs,
			Certificates: tc.certificates,
		},
		ForceAttemptHTTP2:   true,
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 100,
		IdleConnTimeout:     90 * time.Second,
	}
	return &Client{
		backend:       cfg.Backend,
		url:           endpoint,
		serviceName:   cfg.ServiceName,
		authorization: tc.authorization,
		userAgent:     ua,
		retry:         retry,
		failureMode:   cfg.FailureMode,
		breaker:       br,
		hooks:         cfg.Hooks,
		transport:     transport,
		http: &http.Client{
			Transport: transport,
			// Never follow redirects, so Authorization cannot leak.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		clock:  clock.System{},
		random: rand.Float64,
	}, nil
}

func checkEndpoint(cfg Config, kind authKind) (string, error) {
	if cfg.Endpoint == "" {
		return "", configError("Endpoint is required")
	}
	u, err := url.Parse(cfg.Endpoint)
	if err != nil {
		return "", configError("Endpoint is not a valid URL")
	}
	if u.Host == "" || u.Hostname() == "" {
		return "", configError("Endpoint must have a host")
	}
	if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(cfg.Endpoint, "?#") {
		return "", configError("Endpoint must not have a query string or fragment")
	}
	if u.User != nil {
		return "", configError("Endpoint must not contain user info")
	}
	switch u.Scheme {
	case "https":
	case "http":
		switch {
		case cfg.Backend != BackendSpeedle:
			return "", configError("Endpoint must use https for %s", cfg.Backend)
		case kind != authNone:
			return "", configError("Endpoint must use https with BearerToken or MutualTLS")
		case !isLoopback(u.Hostname()) && !cfg.AllowInsecureHTTP:
			return "", configError("http is accepted only for loopback hosts unless AllowInsecureHTTP is set")
		}
	default:
		return "", configError("Endpoint scheme must be https")
	}
	return u.Scheme + "://" + u.Host + strings.TrimRight(u.EscapedPath(), "/") + IsAllowedPath, nil
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func retryDefaults(p RetryPolicy) (RetryPolicy, error) {
	if p.MaxAttempts < 0 || p.BaseDelay < 0 || p.MaxDelay < 0 || p.AttemptTimeout < 0 || p.Deadline < 0 {
		return p, configError("RetryPolicy values must not be negative")
	}
	if p.MaxAttempts == 0 {
		p.MaxAttempts = 3
	}
	if p.BaseDelay == 0 {
		p.BaseDelay = 100 * time.Millisecond
	}
	if p.MaxDelay == 0 {
		p.MaxDelay = 2 * time.Second
	}
	if p.AttemptTimeout == 0 {
		p.AttemptTimeout = 2 * time.Second
	}
	if p.Deadline == 0 {
		p.Deadline = 5 * time.Second
	}
	return p, nil
}

// Close releases the connection pool. Calls made after Close still work but
// open new connections.
func (c *Client) Close() { c.transport.CloseIdleConnections() }

// IsAllowed returns the allowed value of a 200 response. Every other outcome
// is an error, or false with [FailureModeClosed]. When ctx is cancelled or
// its deadline passes, the call stops, including during a retry delay, and
// returns an error for which errors.Is(err, ctx.Err()) holds, in every
// failure mode.
func (c *Client) IsAllowed(ctx context.Context, subject Subject, action, resource string, attrs Attributes, opts ...CallOption) (bool, error) {
	d, err := c.call(ctx, subject, action, resource, attrs, opts)
	if err != nil {
		if _, ok := err.(*Error); ok && c.failureMode == FailureModeClosed {
			return false, nil
		}
		return false, err
	}
	return d.Allowed, nil
}

// Check returns the full decision. It always returns the error when the call
// gives up, whatever the failure mode.
func (c *Client) Check(ctx context.Context, subject Subject, action, resource string, attrs Attributes, opts ...CallOption) (*Decision, error) {
	d, err := c.call(ctx, subject, action, resource, attrs, opts)
	if err != nil {
		return nil, err
	}
	return d, nil
}

type wirePrincipal struct {
	Type string `json:"type"`
	Name string `json:"name"`
	IDD  string `json:"idd,omitempty"`
}

type wireSubject struct {
	Principals []wirePrincipal `json:"principals"`
}

// wireRequest is the Speedle JsonContext, fields in the design's order.
type wireRequest struct {
	Subject     wireSubject     `json:"subject"`
	ServiceName string          `json:"serviceName"`
	Resource    string          `json:"resource"`
	Action      string          `json:"action"`
	Attributes  []wireAttribute `json:"attributes,omitempty"`
}

func (c *Client) buildBody(subject Subject, action, resource string, attrs Attributes, opts []CallOption) ([]byte, *Error) {
	var o callOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	service := c.serviceName
	if o.serviceName != nil {
		if *o.serviceName == "" {
			return nil, invalidRequest("the serviceName override is empty")
		}
		service = *o.serviceName
	}
	if len(subject) == 0 {
		return nil, invalidRequest("subject has no principals")
	}
	req := wireRequest{
		Subject:     wireSubject{Principals: make([]wirePrincipal, len(subject))},
		ServiceName: service,
		Resource:    resource,
		Action:      action,
	}
	for i, p := range subject {
		if p.Name == "" {
			return nil, invalidRequest("principal %d has an empty name", i)
		}
		typ := p.Type
		if typ == "" {
			typ = PrincipalUser
		}
		req.Subject.Principals[i] = wirePrincipal{Type: typ, Name: p.Name, IDD: p.IDD}
	}
	wa, err := attrs.toWire()
	if err != nil {
		return nil, err
	}
	req.Attributes = wa
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(req); err != nil {
		return nil, invalidRequest("request cannot be encoded as JSON")
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// cancelled wraps the caller's context error.
func cancelled(err error) error { return fmt.Errorf("speedle: call cancelled: %w", err) }

// call runs one call: validation, breaker, retry loop, hooks.
func (c *Client) call(ctx context.Context, subject Subject, action, resource string, attrs Attributes, opts []CallOption) (*Decision, error) {
	if err := ctx.Err(); err != nil {
		return nil, cancelled(err)
	}
	body, verr := c.buildBody(subject, action, resource, attrs, opts)
	if verr != nil {
		c.onGiveUp(GiveUpEvent{Attempts: 0, Reason: GiveUpNonRetryable, Err: verr})
		return nil, verr
	}
	trial := false
	if c.breaker != nil {
		ok, tr, changes := c.breaker.allow(c.clock.Now())
		c.onBreaker(changes)
		if !ok {
			e := newError(KindCircuitOpen, "the circuit breaker is open")
			c.onGiveUp(GiveUpEvent{Attempts: 0, Reason: GiveUpCircuitOpen, Err: e})
			return nil, e
		}
		trial = tr
	}
	r := c.retryLoop(ctx, body)
	switch {
	case r.cancel != nil:
		c.record(trial, outcomeNeutral)
		return nil, cancelled(r.cancel)
	case r.decision != nil:
		c.record(trial, outcomeSuccess)
		return r.decision, nil
	}
	c.onGiveUp(GiveUpEvent{Attempts: r.attempts, Reason: r.reason, Err: r.err})
	c.record(trial, classify(r.err))
	return nil, r.err
}

func (c *Client) record(trial bool, o outcome) {
	if c.breaker != nil {
		c.onBreaker(c.breaker.record(trial, o, c.clock.Now()))
	}
}

// classify maps a final error to its breaker outcome (design §6.6).
func classify(e *Error) outcome {
	switch e.Kind {
	case KindNetwork, KindTimeout, KindDeadlineExceeded:
		return outcomeFailure
	}
	switch {
	case e.Status >= 500:
		return outcomeFailure
	case e.Status >= 200 && e.Status < 300, e.Status >= 400 && e.Status < 500:
		return outcomeSuccess
	}
	return outcomeNeutral
}

type loopResult struct {
	decision *Decision
	err      *Error
	reason   GiveUpReason
	attempts int
	cancel   error
}

// retryLoop is the algorithm of design §6.3.
func (c *Client) retryLoop(ctx context.Context, body []byte) loopResult {
	p := c.retry
	start := c.clock.Now()
	deadlineAt := start.Add(p.Deadline)
	if dl, ok := ctx.Deadline(); ok {
		if callerAt := start.Add(time.Until(dl)); callerAt.Before(deadlineAt) {
			deadlineAt = callerAt
		}
	}
	for attempt := 1; ; attempt++ {
		remaining := deadlineAt.Sub(c.clock.Now())
		if remaining <= 0 {
			return loopResult{err: newError(KindDeadlineExceeded, "the call deadline left no time for another attempt"), reason: GiveUpDeadline, attempts: attempt - 1}
		}
		timeout, cutByDeadline := p.AttemptTimeout, false
		if remaining <= timeout {
			timeout, cutByDeadline = remaining, true
		}
		began := c.clock.Now()
		res := c.send(ctx, body, timeout)
		if err := ctx.Err(); err != nil {
			return loopResult{cancel: err}
		}
		if res.err != nil && res.err.Kind == KindTimeout && cutByDeadline {
			res.err.Kind = KindDeadlineExceeded
			res.err.Message = "the call deadline ended the attempt"
			res.retryable = false
		}
		ev := AttemptEvent{Attempt: attempt, Duration: c.clock.Now().Sub(began), Status: res.status}
		if res.err != nil {
			ev.ErrorType = string(res.err.Kind)
		}
		c.onAttempt(ev)

		if res.decision != nil {
			return loopResult{decision: res.decision}
		}
		switch {
		case res.err.Kind == KindDeadlineExceeded:
			return loopResult{err: res.err, reason: GiveUpDeadline, attempts: attempt}
		case !res.retryable:
			return loopResult{err: res.err, reason: GiveUpNonRetryable, attempts: attempt}
		case attempt >= p.MaxAttempts:
			return loopResult{err: res.err, reason: GiveUpAttemptsExhausted, attempts: attempt}
		}
		delay := c.backoff(attempt)
		if res.retryAfter > 0 && res.retryAfter > delay {
			delay = res.retryAfter
		}
		if delay >= deadlineAt.Sub(c.clock.Now()) {
			reason := GiveUpDeadline
			if res.retryAfter > 0 {
				reason = GiveUpRetryAfterExceedsDeadline
			}
			return loopResult{err: res.err, reason: reason, attempts: attempt}
		}
		c.onRetry(RetryEvent{Attempt: attempt + 1, Delay: delay, ErrorType: string(res.err.Kind)})
		if err := c.clock.Sleep(ctx, delay); err != nil {
			return loopResult{cancel: err}
		}
	}
}

// backoff is full jitter: random(0, min(maxDelay, baseDelay × 2^(n-1))).
func (c *Client) backoff(attempt int) time.Duration {
	ceiling := c.retry.MaxDelay
	if shift := attempt - 1; shift < 62 {
		if d := c.retry.BaseDelay << shift; d > 0 && d>>shift == c.retry.BaseDelay && d < ceiling {
			ceiling = d
		}
	}
	return time.Duration(float64(ceiling) * c.random())
}

func (c *Client) onAttempt(ev AttemptEvent) {
	if f := c.hooks.OnAttempt; f != nil {
		safely(func() { f(ev) })
	}
}

func (c *Client) onRetry(ev RetryEvent) {
	if f := c.hooks.OnRetry; f != nil {
		safely(func() { f(ev) })
	}
}

func (c *Client) onGiveUp(ev GiveUpEvent) {
	if f := c.hooks.OnGiveUp; f != nil {
		safely(func() { f(ev) })
	}
}

func (c *Client) onBreaker(changes []BreakerStateChangeEvent) {
	if f := c.hooks.OnBreakerStateChange; f != nil {
		for _, ev := range changes {
			safely(func() { f(ev) })
		}
	}
}

// safely runs a hook and ignores its panic: a hook never changes the result.
func safely(f func()) {
	defer func() { _ = recover() }()
	f()
}
