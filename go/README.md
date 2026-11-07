# Speedle Go SDK

Go client for the Speedle authorization decision service
(`POST /authz-check/v1/is-allowed`) on
[Speedle Cloud](https://cloud.speedle.io) and on
[self-hosted Speedle](https://github.com/teramoby/speedle-plus) ADS.

- Module: `github.com/teramoby/speedle-sdk/go`, package `speedle`
- Go: the two latest releases (1.26 and 1.27)
- Dependencies: the standard library only
- License: UPL-1.0, © 2026 The Speedle Authors
- Status: 0.1.0 published in the Go module proxy (2026-11-05); this README
  describes 0.2.0, which is not yet released

## Install

```sh
go get github.com/teramoby/speedle-sdk/go
```

## Quickstart: Speedle Cloud

```go
import speedle "github.com/teramoby/speedle-sdk/go"

client, err := speedle.NewClient(speedle.Config{
	Backend:     speedle.BackendSpeedleCloud,
	Endpoint:    "https://cloud.speedle.io",
	Auth:        speedle.BearerToken(os.Getenv("SPEEDLE_TOKEN")),
	ServiceName: "books", // must be the token's service
})
if err != nil {
	return err
}
defer client.Close()

ok, err := client.IsAllowed(ctx, speedle.User("alice"), "get", "/books/123", speedle.Attributes{
	speedle.String("team", "eng"),
	speedle.Numeric("clearance", 4),
	speedle.Datetime("issuedAt", time.Date(2026, 11, 5, 3, 0, 0, 0, time.UTC)),
})
if err != nil {
	return err // a typed *speedle.Error, or the ctx error
}
if ok {
	// ...
}
```

The Speedle Cloud token is a server-side secret. Never ship it to browsers,
mobile apps or desktop clients.

## Quickstart: self-hosted Speedle

```go
client, err := speedle.NewClient(speedle.Config{
	Backend:     speedle.BackendSpeedle,
	Endpoint:    "https://ads.internal:6734",
	Auth:        speedle.NoAuth(), // or speedle.MutualTLS(cert)
	ServiceName: "books",
	RootCAs:     pool, // your server's CA; nil means the system roots
})

d, err := client.Check(ctx, speedle.Subject{
	{Type: speedle.PrincipalUser, Name: "bob"},
	{Type: speedle.PrincipalGroup, Name: "readers"},
}, "download", "/books/HarryPotter", nil)
// d.Allowed, d.Reason, d.ErrorMessage
```

`http://` is accepted only for `BackendSpeedle` with `NoAuth()`, and only
for loopback hosts unless `AllowInsecureHTTP` is set. Self-hosted Speedle warns
that a plaintext endpoint has no authentication; keep it on a trusted network.
An unknown service on self-hosted Speedle is not an error: the decision is a
deny with reason `ServiceNotFound`.

## Calls

- `IsAllowed(ctx, subject, action, resource, attrs, opts...)` returns the
  `allowed` value of a 200 response.
- `Check(...)` returns a `*Decision` with `Allowed`, `Reason`,
  `ErrorMessage` (self-hosted Speedle evaluation errors) and
  `QuotaDailyRemaining` (Speedle Cloud).
- `speedle.WithServiceName("orders")` overrides `Config.ServiceName` for one
  call.
- `Reason` `DiscoverMode` comes with `Allowed` true but is not a production
  grant.

Create one client per endpoint and share it: it is safe for concurrent use
and keeps a keep-alive connection pool. `Close` releases idle connections.

## Attributes

Each attribute has an explicit Speedle type: `String`/`Strings`,
`Numeric`/`Numerics`, `Bool`/`Bools`, `Datetime`/`Datetimes`
(a `time.Time`, sent as RFC 3339 in UTC). They are sent in your order.

`speedle.Infer(map[string]any{...})` builds them from native values: a
string is always `string` (never a date), any integer or float is
`numeric`, a `bool` is `bool`, a `time.Time` is `datetime`, and a non-empty
homogeneous slice is the array form. Anything else (nil, an empty or mixed
slice, a map) returns an error wrapping `ErrInvalidRequest`. The result is
sorted by name.

A call fails with `ErrInvalidRequest`, without sending anything, for an empty
or duplicate attribute name, a NaN or infinite number, or a zero
`Attribute`. Self-hosted Speedle decodes numbers as `float64`, so integers
above 2^53 lose precision.

Subjects and attributes must come from your application's own authenticated
context, never from untrusted client input.

## Errors

Every SDK error is a `*speedle.Error` with `Kind`, `Status`, `Code` (Speedle
Cloud error code), `Message`, `RetryAfter` and `QuotaDailyRemaining`. Test the
kind with `errors.Is`:

| Sentinel | When |
| --- | --- |
| `ErrConfiguration` | `NewClient` rejected the config |
| `ErrInvalidRequest` | 400, or the attribute and subject checks before sending |
| `ErrUnauthorized` | 401 |
| `ErrForbidden` | 403, including Cloud `service_mismatch` |
| `ErrRequestTooLarge` | 413 |
| `ErrTooManyRequests` | 429; also matches the two below |
| `ErrRateLimited` | burst 429 |
| `ErrQuotaExceeded` | Cloud daily quota (`daily_quota_exceeded`) |
| `ErrServiceUnavailable` | 503 |
| `ErrServer` | 500, 502, 504 and other 5xx |
| `ErrHTTP` | any other status, including 3xx (redirects are never followed) and 404 |
| `ErrUnexpectedResponse` | a 200 whose body is not a decision |
| `ErrNetwork` | transport failure, including TLS; also matches the two below |
| `ErrTimeout` | the last attempt hit `AttemptTimeout` |
| `ErrDeadlineExceeded` | the call's deadline ended it |
| `ErrCircuitOpen` | the breaker rejected the call |

Cancellation is not a `*speedle.Error`: when `ctx` is cancelled or its
deadline passes, the call stops (also during a retry delay) and
`errors.Is(err, ctx.Err())` holds.

## Retries and deadline

| `RetryPolicy` field | Default |
| --- | --- |
| `MaxAttempts` (counting the first; 1 disables retries) | 3 |
| `BaseDelay` | 100 ms |
| `MaxDelay` | 2 s |
| `AttemptTimeout` | 2 s |
| `Deadline` (whole call; a shorter `ctx` deadline wins) | 5 s |

Backoff is exponential with full jitter, raised to `Retry-After` when the
server sends one. Retried: connection errors, attempt timeouts, 502, 503,
504, and a burst 429 whose `Retry-After` fits in the remaining deadline.
Never retried: 400, 401, 403, 404, the daily-quota 429, 500 and other 5xx,
413, 415, unexpected bodies and TLS failures. On Speedle Cloud every attempt
that reaches the service is charged against the daily quota, so a retried
`snapshot_unavailable` 503 costs one unit per attempt; `MaxAttempts: 1`
turns retries off.

## Failure mode

`FailureModeClosed` makes `IsAllowed` return `(false, nil)` instead of any
SDK error, including circuit-open and validation errors; `OnGiveUp` still
fires. `Check` always returns the error, and cancellation is never
converted. There is no fail-open mode.

## Circuit breaker

Off by default. `CircuitBreaker: &speedle.CircuitBreaker{}` turns it on with
5 consecutive failed calls and a 30 s cool-down. Network errors, timeouts and
5xx count as failures; a 200 or 4xx resets the count. While open, calls fail
at once with `ErrCircuitOpen`; after the cool-down one trial call is let
through.

## Hooks

`Hooks{OnAttempt, OnRetry, OnGiveUp, OnBreakerStateChange}` run synchronously
and should be cheap; a panic in a hook is ignored. Payloads never contain the
token, headers, subject, action, resource or attributes. The SDK sends
nothing but the is-allowed request: no telemetry, no logging.

## Requests on the wire

`User-Agent: speedle-sdk-go/0.2.0 (go/<version>; <os>)`, plus
`UserAgentSuffix`. The body is the Speedle `JsonContext` for both backends.
The transport honours the standard `HTTPS_PROXY`/`NO_PROXY` environment
variables, as `net/http` does. Certificate verification cannot be disabled;
`RootCAs` replaces the system pool, so to add a CA to the system roots start
from `x509.SystemCertPool()`.

## Development

```sh
go test -short ./...                  # unit tests
go test -run TestConformance -v .     # conformance vectors against ../conformance/mock
```
