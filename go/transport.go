// Copyright (c) 2026 The Speedle Authors
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl.

package speedle

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// maxResponseBody bounds how much of a response the SDK reads.
const maxResponseBody = 1 << 20

// cloudCodes are the error codes of the Speedle Cloud contract (design §3.4).
var cloudCodes = map[string]bool{
	"invalid_request":             true,
	"unsupported_token_assertion": true,
	"unauthorized":                true,
	"service_mismatch":            true,
	"service_not_found":           true,
	"request_too_large":           true,
	"rate_limited":                true,
	"daily_quota_exceeded":        true,
	"internal_error":              true,
	"quota_unavailable":           true,
	"snapshot_unavailable":        true,
}

// attemptResult is the outcome of one HTTP attempt.
type attemptResult struct {
	decision   *Decision
	err        *Error
	status     int           // 0 when no response
	retryable  bool          // the error may be retried (design §6.3)
	retryAfter time.Duration // a usable Retry-After, or 0
}

func (c *Client) send(ctx context.Context, body []byte, timeout time.Duration) attemptResult {
	actx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(actx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return attemptResult{err: newError(KindNetwork, "cannot build the request")}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	if c.authorization != "" {
		req.Header.Set("Authorization", c.authorization)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return transportFailure(actx, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody+1))
	if err != nil {
		r := transportFailure(actx, err)
		r.status = 0
		return r
	}
	return c.interpret(resp, data)
}

// transportFailure classifies an error with no complete response.
func transportFailure(actx context.Context, err error) attemptResult {
	if errors.Is(actx.Err(), context.DeadlineExceeded) {
		return attemptResult{err: &Error{Kind: KindTimeout, Message: "the attempt timed out", cause: err}, retryable: true}
	}
	if isTLSFailure(err) {
		return attemptResult{err: &Error{Kind: KindNetwork, Message: "TLS handshake or certificate verification failed: " + causeText(err), cause: err}}
	}
	return attemptResult{err: &Error{Kind: KindNetwork, Message: causeText(err), cause: err}, retryable: true}
}

// causeText describes a transport error without the request URL that
// *url.Error adds.
func causeText(err error) string {
	var ue interface{ Unwrap() error }
	if errors.As(err, &ue) {
		if inner := ue.Unwrap(); inner != nil {
			return inner.Error()
		}
	}
	return err.Error()
}

func isTLSFailure(err error) bool {
	var (
		unknownAuthority x509.UnknownAuthorityError
		hostname         x509.HostnameError
		invalid          x509.CertificateInvalidError
		verification     *tls.CertificateVerificationError
		alert            tls.AlertError
		recordHeader     tls.RecordHeaderError
	)
	if errors.As(err, &unknownAuthority) || errors.As(err, &hostname) || errors.As(err, &invalid) ||
		errors.As(err, &verification) || errors.As(err, &alert) || errors.As(err, &recordHeader) {
		return true
	}
	// A TLS alert from the server, such as "certificate required", comes
	// as a *net.OpError with Op "remote error".
	var op *net.OpError
	return errors.As(err, &op) && op.Op == "remote error"
}

func (c *Client) interpret(resp *http.Response, data []byte) attemptResult {
	status := resp.StatusCode
	quota := parseQuota(resp.Header.Get("X-Quota-Daily-Remaining"))
	retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
	res := attemptResult{status: status}

	if status == http.StatusOK {
		d, problem := parseDecision(data)
		if problem != "" {
			res.err = &Error{Kind: KindUnexpectedResponse, Status: status, Message: problem, QuotaDailyRemaining: quota}
			return res
		}
		d.QuotaDailyRemaining = quota
		res.decision = d
		return res
	}

	code, message := c.errorBody(data)
	e := &Error{Status: status, Code: code, Message: message, QuotaDailyRemaining: quota}
	res.err = e
	switch {
	case status == 400:
		e.Kind = KindInvalidRequest
	case status == 401:
		e.Kind = KindUnauthorized
	case status == 403:
		e.Kind = KindForbidden
	case status == 413:
		e.Kind = KindRequestTooLarge
	case status == 429:
		e.RetryAfter = retryAfter
		switch {
		case c.backend == BackendSpeedle, code == "rate_limited":
			e.Kind = KindRateLimited
			// A burst 429 is retried only with a usable Retry-After.
			res.retryable = retryAfter > 0
			res.retryAfter = retryAfter
		case code == "daily_quota_exceeded":
			e.Kind = KindQuotaExceeded
		default:
			e.Kind = KindTooManyRequests
		}
	case status == 503:
		e.Kind = KindServiceUnavailable
		res.retryable, res.retryAfter = true, retryAfter
	case status == 502 || status == 504:
		e.Kind = KindServer
		res.retryable, res.retryAfter = true, retryAfter
	case status >= 500 && status <= 599:
		e.Kind = KindServer
	default:
		e.Kind = KindHTTP
	}
	return res
}

// errorBody reads {"error": "..."}. For Speedle Cloud a known contract code
// becomes Code and anything else Message; for self-hosted Speedle it is always
// Message.
func (c *Client) errorBody(data []byte) (code, message string) {
	var body struct {
		Error *string `json:"error"`
	}
	if err := json.Unmarshal(data, &body); err != nil || body.Error == nil {
		return "", ""
	}
	text := sanitize(*body.Error)
	if c.backend == BackendSpeedleCloud && cloudCodes[text] {
		return text, ""
	}
	return "", text
}

// sanitize bounds a server message and drops control characters.
func sanitize(s string) string {
	const max = 512
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	if len(s) > max {
		cut := max
		for cut > 0 && !utf8Start(s[cut]) {
			cut--
		}
		s = s[:cut] + "…"
	}
	return s
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }

func parseDecision(data []byte) (*Decision, string) {
	if len(data) > maxResponseBody {
		return nil, "the response body is too large"
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil || raw == nil {
		return nil, "the response body is not a JSON object"
	}
	d := &Decision{}
	switch string(bytes.TrimSpace(raw["allowed"])) {
	case "true":
		d.Allowed = true
	case "false":
	default:
		return nil, "the response has no boolean allowed field"
	}
	reasonRaw, ok := raw["reason"]
	if !ok {
		return nil, "the response has no reason field"
	}
	var n json.Number
	trimmed := bytes.TrimSpace(reasonRaw)
	if len(trimmed) == 0 || (trimmed[0] != '-' && (trimmed[0] < '0' || trimmed[0] > '9')) {
		return nil, "the response reason is not a number"
	}
	if err := json.Unmarshal(trimmed, &n); err != nil {
		return nil, "the response reason is not a number"
	}
	r, err := strconv.ParseInt(n.String(), 10, 32)
	if err != nil {
		return nil, "the response reason is not an integer"
	}
	d.Reason = Reason(r)
	if m, ok := raw["errorMessage"]; ok && string(bytes.TrimSpace(m)) != "null" {
		var s string
		if err := json.Unmarshal(m, &s); err != nil {
			return nil, "the response errorMessage is not a string"
		}
		d.ErrorMessage = s
	}
	return d, ""
}

// parseQuota reads a non-negative integer header, or nil.
func parseQuota(v string) *int64 {
	if v == "" || strings.TrimLeft(v, "0123456789") != "" {
		return nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return nil
	}
	return &n
}

// parseRetryAfter reads a positive integer number of seconds, or 0.
func parseRetryAfter(v string) time.Duration {
	if v == "" || strings.TrimLeft(v, "0123456789") != "" {
		return 0
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 || n > int64(time.Duration(1<<62)/time.Second) {
		return 0
	}
	return time.Duration(n) * time.Second
}
