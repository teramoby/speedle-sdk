// Copyright (c) 2026 The Speedle Authors
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl.

// Package speedle is the Go client for the Speedle authorization decision
// service: POST /authz-check/v1/is-allowed on Speedle Cloud or on a
// self-hosted Speedle ADS.
//
// Create one [Client] per endpoint with [NewClient] and share it; it is safe
// for concurrent use and keeps a pool of keep-alive connections. Every call
// is retried with exponential backoff and full jitter within a deadline
// (see [RetryPolicy]), fails with a typed [*Error] (or, with
// [FailureModeClosed], makes IsAllowed return false), and never returns
// true without a 200 response whose allowed field is true.
//
// The Speedle Cloud token is a server-side secret. Never ship it to
// browsers, mobile apps or desktop clients. Subjects and attributes must
// come from the application's own authenticated context, not from untrusted
// client input.
//
// The SDK sends nothing but the is-allowed request: no telemetry, no
// logging. [Hooks] are the only observability interface.
package speedle

import (
	"strconv"
	"time"
)

// Version is the SDK version sent in the User-Agent header.
const Version = "0.1.0"

// Backend selects the auth rules and the error body interpretation. The
// request format is the same for both.
type Backend string

const (
	BackendSpeedleCloud Backend = "speedle-cloud"
	BackendSpeedlePlus  Backend = "speedle-plus"
)

// Principal types known to Speedle. Type is an open string.
const (
	PrincipalUser   = "user"
	PrincipalGroup  = "group"
	PrincipalRole   = "role"
	PrincipalEntity = "entity"
)

// Principal is one subject principal. An empty Type is sent as "user"; IDD is
// sent only when set.
type Principal struct {
	Type string // defaults to "user"
	Name string
	IDD  string
}

// Subject is the list of principals of a call. It must not be empty.
type Subject []Principal

// User is a subject with one user principal.
func User(name string) Subject { return Subject{{Type: PrincipalUser, Name: name}} }

// Reason is the decision reason code, the same for both backends. Unknown
// codes are kept as their integer value.
type Reason int

const (
	GrantPolicyFound Reason = iota
	DenyPolicyFound
	ServiceNotFound
	NoApplicablePolicies
	ErrorInEvaluation
	// DiscoverMode comes with Allowed true but is not a production grant.
	DiscoverMode
	ReasonNotAvailable
)

var reasonNames = [...]string{
	"GRANT_POLICY_FOUND", "DENY_POLICY_FOUND", "SERVICE_NOT_FOUND",
	"NO_APPLICABLE_POLICIES", "ERROR_IN_EVALUATION", "DISCOVER_MODE",
	"REASON_NOT_AVAILABLE",
}

func (r Reason) String() string {
	if r >= 0 && int(r) < len(reasonNames) {
		return reasonNames[r]
	}
	return "Reason(" + strconv.Itoa(int(r)) + ")"
}

// Decision is the result of [Client.Check].
type Decision struct {
	Allowed             bool
	Reason              Reason
	ErrorMessage        string // self-hosted Speedle only: set when evaluation failed
	QuotaDailyRemaining *int64 // Speedle Cloud only
}

// RetryPolicy bounds the retries of one call. Zero fields take the defaults.
type RetryPolicy struct {
	MaxAttempts    int           // default 3, counting the first; 1 disables retries
	BaseDelay      time.Duration // default 100ms
	MaxDelay       time.Duration // default 2s (backoff cap)
	AttemptTimeout time.Duration // default 2s: connect, TLS, request and response
	Deadline       time.Duration // default 5s for the whole call; a shorter ctx deadline wins
}

// FailureMode decides what IsAllowed does when a call gives up.
type FailureMode int

const (
	// FailureModeError returns the typed error (default).
	FailureModeError FailureMode = iota
	// FailureModeClosed makes IsAllowed return (false, nil) on every SDK
	// error, including circuit-open and validation errors. OnGiveUp still
	// fires. Check always returns the error, and caller cancellation is
	// never converted. There is no fail-open mode.
	FailureModeClosed
)

// CircuitBreaker configures the optional per-client breaker. Zero fields
// take the defaults.
type CircuitBreaker struct {
	FailureThreshold int           // default 5 consecutive failed calls
	CoolDown         time.Duration // default 30s
}

// BreakerState is "closed", "open" or "half_open".
type BreakerState string

const (
	BreakerClosed   BreakerState = "closed"
	BreakerOpen     BreakerState = "open"
	BreakerHalfOpen BreakerState = "half_open"
)

// GiveUpReason says why a call ended in an error.
type GiveUpReason string

const (
	GiveUpNonRetryable              GiveUpReason = "non_retryable"
	GiveUpAttemptsExhausted         GiveUpReason = "attempts_exhausted"
	GiveUpDeadline                  GiveUpReason = "deadline"
	GiveUpRetryAfterExceedsDeadline GiveUpReason = "retry_after_exceeds_deadline"
	GiveUpCircuitOpen               GiveUpReason = "circuit_open"
)

// AttemptEvent is passed to Hooks.OnAttempt after every attempt.
type AttemptEvent struct {
	Attempt   int
	Duration  time.Duration
	Status    int    // 0 when no response
	ErrorType string // §7 name, "" on success
}

// RetryEvent is passed to Hooks.OnRetry before every retry delay.
type RetryEvent struct {
	Attempt   int // the next attempt
	Delay     time.Duration
	ErrorType string
}

// GiveUpEvent is passed to Hooks.OnGiveUp when a call ends in an error.
type GiveUpEvent struct {
	Attempts int
	Reason   GiveUpReason
	Err      *Error
}

// BreakerStateChangeEvent is passed to Hooks.OnBreakerStateChange.
type BreakerStateChangeEvent struct{ From, To BreakerState }

// Hooks are optional callbacks. They run synchronously on the calling
// goroutine and should be cheap; a panic in a hook is recovered and ignored.
// Payloads never contain the token, headers, subject, action, resource,
// attributes or request body.
type Hooks struct {
	OnAttempt            func(AttemptEvent)
	OnRetry              func(RetryEvent)
	OnGiveUp             func(GiveUpEvent)
	OnBreakerStateChange func(BreakerStateChangeEvent)
}
