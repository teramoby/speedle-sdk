// Copyright (c) 2026 The Speedle Authors
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl.

package speedle

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrorKind names the kind of an [Error]. Its value is the error name shared
// by every Speedle SDK (design §7), such as "ServiceUnavailableError"; hook
// events carry the same string.
type ErrorKind string

const (
	KindConfiguration      ErrorKind = "ConfigurationError"
	KindInvalidRequest     ErrorKind = "InvalidRequestError"
	KindUnauthorized       ErrorKind = "UnauthorizedError"
	KindForbidden          ErrorKind = "ForbiddenError"
	KindRequestTooLarge    ErrorKind = "RequestTooLargeError"
	KindTooManyRequests    ErrorKind = "TooManyRequestsError"
	KindRateLimited        ErrorKind = "RateLimitedError"
	KindQuotaExceeded      ErrorKind = "QuotaExceededError"
	KindServiceUnavailable ErrorKind = "ServiceUnavailableError"
	KindServer             ErrorKind = "ServerError"
	KindHTTP               ErrorKind = "HttpError"
	KindUnexpectedResponse ErrorKind = "UnexpectedResponseError"
	KindNetwork            ErrorKind = "NetworkError"
	KindTimeout            ErrorKind = "TimeoutError"
	KindDeadlineExceeded   ErrorKind = "DeadlineExceededError"
	KindCircuitOpen        ErrorKind = "CircuitOpenError"
)

// Sentinels for errors.Is. Every error the SDK returns, except caller
// cancellation, is an *[Error]; errors.Is(err, ErrX) holds when its kind is
// X or a subtype of X: ErrRateLimited and ErrQuotaExceeded are also
// ErrTooManyRequests, ErrDeadlineExceeded is also ErrTimeout, and ErrTimeout
// is also ErrNetwork.
var (
	ErrConfiguration      = errors.New("speedle: configuration error")
	ErrInvalidRequest     = errors.New("speedle: invalid request")
	ErrUnauthorized       = errors.New("speedle: unauthorized")
	ErrForbidden          = errors.New("speedle: forbidden")
	ErrRequestTooLarge    = errors.New("speedle: request too large")
	ErrTooManyRequests    = errors.New("speedle: too many requests")
	ErrRateLimited        = errors.New("speedle: rate limited")
	ErrQuotaExceeded      = errors.New("speedle: daily quota exceeded")
	ErrServiceUnavailable = errors.New("speedle: service unavailable")
	ErrServer             = errors.New("speedle: server error")
	ErrHTTP               = errors.New("speedle: unexpected HTTP status")
	ErrUnexpectedResponse = errors.New("speedle: unexpected response")
	ErrNetwork            = errors.New("speedle: network error")
	ErrTimeout            = errors.New("speedle: attempt timed out")
	ErrDeadlineExceeded   = errors.New("speedle: deadline exceeded")
	ErrCircuitOpen        = errors.New("speedle: circuit open")
)

var sentinels = map[ErrorKind]error{
	KindConfiguration:      ErrConfiguration,
	KindInvalidRequest:     ErrInvalidRequest,
	KindUnauthorized:       ErrUnauthorized,
	KindForbidden:          ErrForbidden,
	KindRequestTooLarge:    ErrRequestTooLarge,
	KindTooManyRequests:    ErrTooManyRequests,
	KindRateLimited:        ErrRateLimited,
	KindQuotaExceeded:      ErrQuotaExceeded,
	KindServiceUnavailable: ErrServiceUnavailable,
	KindServer:             ErrServer,
	KindHTTP:               ErrHTTP,
	KindUnexpectedResponse: ErrUnexpectedResponse,
	KindNetwork:            ErrNetwork,
	KindTimeout:            ErrTimeout,
	KindDeadlineExceeded:   ErrDeadlineExceeded,
	KindCircuitOpen:        ErrCircuitOpen,
}

var parentKind = map[ErrorKind]ErrorKind{
	KindRateLimited:      KindTooManyRequests,
	KindQuotaExceeded:    KindTooManyRequests,
	KindTimeout:          KindNetwork,
	KindDeadlineExceeded: KindTimeout,
}

// Error is the one error type of the SDK. Use errors.Is with the Err*
// sentinels to test the kind and errors.As to read the fields.
type Error struct {
	Kind ErrorKind
	// Status is the HTTP status, or 0 when there was no response.
	Status int
	// Code is the Speedle Cloud error code, such as "service_mismatch", or
	// "" (always "" for Speedle+).
	Code string
	// Message is the server's error text or the SDK's description. It never
	// contains the token, attributes or the request body.
	Message string
	// RetryAfter is the 429 Retry-After value, or 0 when absent or invalid.
	RetryAfter time.Duration
	// QuotaDailyRemaining is the Speedle Cloud X-Quota-Daily-Remaining
	// header, or nil when absent or malformed.
	QuotaDailyRemaining *int64

	cause error
}

func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString("speedle: ")
	b.WriteString(string(e.Kind))
	if e.Status != 0 {
		fmt.Fprintf(&b, " (HTTP %d)", e.Status)
	}
	if e.Code != "" {
		b.WriteString(" ")
		b.WriteString(e.Code)
	}
	if e.Message != "" {
		b.WriteString(": ")
		b.WriteString(e.Message)
	}
	return b.String()
}

// Is reports whether target is the sentinel of e's kind or of a parent kind.
func (e *Error) Is(target error) bool {
	for k := e.Kind; k != ""; k = parentKind[k] {
		if sentinels[k] == target {
			return true
		}
	}
	return false
}

// Unwrap returns the underlying transport error, if any.
func (e *Error) Unwrap() error { return e.cause }

func newError(kind ErrorKind, format string, args ...any) *Error {
	return &Error{Kind: kind, Message: fmt.Sprintf(format, args...)}
}

func configError(format string, args ...any) *Error {
	return newError(KindConfiguration, format, args...)
}

func invalidRequest(format string, args ...any) *Error {
	return newError(KindInvalidRequest, format, args...)
}
