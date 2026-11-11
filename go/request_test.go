// Copyright (c) 2026 The Speedle Authors
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl.

package speedle

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestRequestFormat(t *testing.T) {
	s := newScript(t, reply{status: 200, body: okBody}, reply{status: 200, body: okBody})
	c, _ := testClient(t, s, nil)
	subject := Subject{{Name: "bob"}, {Type: PrincipalGroup, Name: "readers", IDD: "corp"}}
	attrs := Attributes{String("b", "x"), Datetime("a", time.Date(2026, 11, 5, 3, 0, 0, 0, time.UTC))}
	if ok, err := c.IsAllowed(bg, subject, "get", "/books/<1>&", attrs); err != nil || !ok {
		t.Fatalf("IsAllowed: %v %v", ok, err)
	}
	want := `{"subject":{"principals":[{"type":"user","name":"bob"},{"type":"group","name":"readers","idd":"corp"}]},` +
		`"serviceName":"books","resource":"/books/<1>&","action":"get",` +
		`"attributes":[{"name":"b","type":"string","value":"x"},{"name":"a","type":"datetime","value":"2026-11-05T03:00:00Z"}]}`
	if s.bodies[0] != want {
		t.Errorf("body\n got %s\nwant %s", s.bodies[0], want)
	}
	r := s.reqs[0]
	if r.Method != http.MethodPost || r.URL.Path != IsAllowedPath || r.URL.RawQuery != "" {
		t.Errorf("request line %s %s", r.Method, r.URL)
	}
	if r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Authorization") != "Bearer spdl_test" ||
		!strings.HasPrefix(r.Header.Get("User-Agent"), "speedle-sdk-go/") {
		t.Errorf("headers %v", r.Header)
	}

	// Per-call serviceName override; no attributes means no attributes field.
	if _, err := c.IsAllowed(bg, User("alice"), "get", "/r", nil, WithServiceName("orders")); err != nil {
		t.Fatal(err)
	}
	if want := `{"subject":{"principals":[{"type":"user","name":"alice"}]},"serviceName":"orders","resource":"/r","action":"get"}`; s.bodies[1] != want {
		t.Errorf("override body %s", s.bodies[1])
	}
}

func TestNoAuthSendsNoAuthorization(t *testing.T) {
	s := newScript(t, reply{status: 200, body: okBody})
	c, _ := testClient(t, s, func(c *Config) { c.Backend, c.Auth = BackendSpeedle, NoAuth() })
	if _, err := c.IsAllowed(bg, User("a"), "get", "/r", nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.reqs[0].Header["Authorization"]; ok {
		t.Error("NoAuth sent Authorization")
	}
}

func TestValidationSendsNothing(t *testing.T) {
	s := newScript(t)
	rec := &recorder{}
	c, _ := testClient(t, s, func(c *Config) { c.Hooks = rec.hooks() })
	calls := map[string]func() error{
		"empty subject":  func() error { _, err := c.IsAllowed(bg, nil, "get", "/r", nil); return err },
		"empty name":     func() error { _, err := c.IsAllowed(bg, Subject{{Type: "user"}}, "get", "/r", nil); return err },
		"empty override": func() error { _, err := c.Check(bg, User("a"), "get", "/r", nil, WithServiceName("")); return err },
		"duplicate": func() error {
			_, err := c.Check(bg, User("a"), "get", "/r", Attributes{Bool("x", true), Bool("x", false)})
			return err
		},
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if s.count() != 0 {
		t.Errorf("%d requests sent", s.count())
	}
	if len(rec.giveUps) != len(calls) || rec.giveUps[0].Attempts != 0 || rec.giveUps[0].Reason != GiveUpNonRetryable {
		t.Errorf("give-up events %+v", rec.giveUps)
	}
}

func TestDecisionParsing(t *testing.T) {
	s := newScript(t,
		reply{status: 200, headers: map[string]string{"X-Quota-Daily-Remaining": "41"}, body: `{"allowed":false,"reason":4,"errorMessage":"boom","extra":[1]}`},
		reply{status: 200, headers: map[string]string{"X-Quota-Daily-Remaining": "-1"}, body: `{"allowed":true,"reason":99}`},
	)
	c, _ := testClient(t, s, nil)
	d, err := c.Check(bg, User("a"), "get", "/r", nil)
	if err != nil {
		t.Fatal(err)
	}
	if d.Allowed || d.Reason != ErrorInEvaluation || d.ErrorMessage != "boom" || d.QuotaDailyRemaining == nil || *d.QuotaDailyRemaining != 41 {
		t.Errorf("decision %+v", d)
	}
	d, err = c.Check(bg, User("a"), "get", "/r", nil)
	if err != nil || !d.Allowed || d.Reason != Reason(99) || d.QuotaDailyRemaining != nil {
		t.Errorf("decision %+v %v", d, err)
	}
}

func TestUnexpectedBodies(t *testing.T) {
	for _, body := range []string{
		`not json`, `[]`, `null`, `{"allowed":"true","reason":0}`, `{"allowed":1,"reason":0}`,
		`{"reason":0}`, `{"allowed":true}`, `{"allowed":true,"reason":"0"}`, `{"allowed":true,"reason":1.5}`,
		`{"allowed":true,"reason":0,"errorMessage":3}`,
	} {
		s := newScript(t, reply{status: 200, body: body})
		c, _ := testClient(t, s, nil)
		ok, err := c.IsAllowed(bg, User("a"), "get", "/r", nil)
		if ok || !errors.Is(err, ErrUnexpectedResponse) || s.count() != 1 {
			t.Errorf("%s: %v %v (%d requests)", body, ok, err, s.count())
		}
	}
}

func TestErrorMapping(t *testing.T) {
	cases := []struct {
		backend Backend
		status  int
		headers map[string]string
		body    string
		kind    ErrorKind
		code    string
		message string
		retryAf time.Duration
		quota   int64 // -1: absent
		tries   int
	}{
		{BackendSpeedleCloud, 400, map[string]string{"X-Quota-Daily-Remaining": "7"}, `{"error":"invalid_request"}`, KindInvalidRequest, "invalid_request", "", 0, 7, 1},
		{BackendSpeedleCloud, 400, nil, `{"error":"SPDL-0004 bad attribute"}`, KindInvalidRequest, "", "SPDL-0004 bad attribute", 0, -1, 1},
		{BackendSpeedle, 400, nil, `{"error":"invalid_request"}`, KindInvalidRequest, "", "invalid_request", 0, -1, 1},
		{BackendSpeedleCloud, 401, nil, `{"error":"unauthorized"}`, KindUnauthorized, "unauthorized", "", 0, -1, 1},
		{BackendSpeedleCloud, 403, nil, `{"error":"service_mismatch"}`, KindForbidden, "service_mismatch", "", 0, -1, 1},
		{BackendSpeedleCloud, 404, nil, `{"error":"service_not_found"}`, KindHTTP, "service_not_found", "", 0, -1, 1},
		{BackendSpeedle, 404, nil, `{"error":"service_not_found"}`, KindHTTP, "", "service_not_found", 0, -1, 1},
		{BackendSpeedleCloud, 404, nil, `404 page not found`, KindHTTP, "", "", 0, -1, 1},
		{BackendSpeedleCloud, 413, nil, `{"error":"request_too_large"}`, KindRequestTooLarge, "request_too_large", "", 0, -1, 1},
		{BackendSpeedleCloud, 415, nil, ``, KindHTTP, "", "", 0, -1, 1},
		{BackendSpeedleCloud, 429, map[string]string{"Retry-After": "60"}, `{"error":"rate_limited"}`, KindRateLimited, "rate_limited", "", time.Minute, -1, 1},
		{BackendSpeedleCloud, 429, map[string]string{"Retry-After": "3600"}, `{"error":"daily_quota_exceeded"}`, KindQuotaExceeded, "daily_quota_exceeded", "", time.Hour, -1, 1},
		{BackendSpeedleCloud, 429, map[string]string{"Retry-After": "1"}, `{"error":"slow_down"}`, KindTooManyRequests, "", "slow_down", time.Second, -1, 1},
		{BackendSpeedleCloud, 429, nil, `<html>`, KindTooManyRequests, "", "", 0, -1, 1},
		{BackendSpeedle, 429, nil, `{"error":"gateway says slow down"}`, KindRateLimited, "", "gateway says slow down", 0, -1, 1},
		{BackendSpeedle, 429, map[string]string{"Retry-After": "soon"}, ``, KindRateLimited, "", "", 0, -1, 1},
		{BackendSpeedleCloud, 500, nil, `{"error":"internal_error"}`, KindServer, "internal_error", "", 0, -1, 1},
		{BackendSpeedleCloud, 501, nil, ``, KindServer, "", "", 0, -1, 1},
		{BackendSpeedleCloud, 502, nil, `<html>`, KindServer, "", "", 0, -1, 3},
		{BackendSpeedleCloud, 503, nil, `{"error":"quota_unavailable"}`, KindServiceUnavailable, "quota_unavailable", "", 0, -1, 3},
		{BackendSpeedleCloud, 504, nil, ``, KindServer, "", "", 0, -1, 3},
		{BackendSpeedleCloud, 302, map[string]string{"Location": "https://evil.example/"}, ``, KindHTTP, "", "", 0, -1, 1},
		{BackendSpeedleCloud, 204, nil, ``, KindHTTP, "", "", 0, -1, 1},
	}
	for _, tc := range cases {
		replies := []reply{}
		for range 3 {
			replies = append(replies, reply{status: tc.status, headers: tc.headers, body: tc.body})
		}
		s := newScript(t, replies...)
		c, _ := testClient(t, s, func(c *Config) { c.Backend = tc.backend })
		_, err := c.Check(bg, User("a"), "get", "/r", nil)
		var e *Error
		if !errors.As(err, &e) {
			t.Errorf("%s %d: %v", tc.backend, tc.status, err)
			continue
		}
		quotaOK := (tc.quota < 0 && e.QuotaDailyRemaining == nil) || (e.QuotaDailyRemaining != nil && *e.QuotaDailyRemaining == tc.quota)
		if e.Kind != tc.kind || e.Status != tc.status || e.Code != tc.code || e.Message != tc.message || e.RetryAfter != tc.retryAf || !quotaOK || s.count() != tc.tries {
			t.Errorf("%s %d %s: got %+v after %d requests", tc.backend, tc.status, tc.body, e, s.count())
		}
	}
}
