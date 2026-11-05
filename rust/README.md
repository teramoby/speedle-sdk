# speedle (Rust)

Rust client for the Speedle authorization decision service
(`POST /authz-check/v1/is-allowed`) on
[Speedle Cloud](https://cloud.speedle.io) and on self-hosted
[Speedle+](https://github.com/teramoby/speedle-plus) ADS.

- Crate: `speedle` (`use speedle::Client`)
- Rust: stable, MSRV 1.75, edition 2021
- Runtime and HTTP: `tokio` with `reqwest` (rustls with the platform's root
  store, HTTP/1.1 and HTTP/2), `serde`/`serde_json`
- License: UPL-1.0, © 2026 The Speedle Authors
- Status: 0.1.0, published on crates.io (2026-11-05)

## Install

Until the first release, use the repository:

```toml
[dependencies]
speedle = { git = "https://github.com/teramoby/speedle-sdk" }
# speedle = { git = "https://github.com/teramoby/speedle-sdk", features = ["blocking"] }
```

## Quickstart: Speedle Cloud

```rust
use std::time::{Duration, UNIX_EPOCH};
use speedle::{Attribute, Auth, Backend, Client};

let speedle = Client::builder(Backend::SpeedleCloud, "https://cloud.speedle.io",
                              Auth::bearer(std::env::var("SPEEDLE_TOKEN")?), "books")
    .build()?;
let attrs = [
    Attribute::string("team", "eng"),
    Attribute::numeric("clearance", 4.0),
    Attribute::datetime("issuedAt", UNIX_EPOCH + Duration::from_secs(1_793_847_600)), // 2026-11-05T03:00:00Z
];
if speedle.is_allowed(&"alice".into(), "get", "/books/123", &attrs).await? {
    // ...
}
```

The service name (`"books"`) is sent on every call; on Speedle Cloud it must
be the token's service, otherwise the call fails with `ErrorKind::Forbidden`
and code `service_mismatch`. The token is a server-side secret: never ship it
to browsers, mobile apps or desktop clients.

## Quickstart: Speedle+

```rust
use speedle::{Auth, Backend, Client, Principal, Subject};

let ca_pem = std::fs::read("ads-ca.pem")?; // your server's CA, if not in the platform store
let client = Client::builder(Backend::SpeedlePlus, "https://ads.internal:6734", Auth::none(), "books")
    .add_root_certificate_pem(&ca_pem)
    .build()?;
let subject = Subject::of([Principal::new(Principal::USER, "bob"), Principal::new(Principal::GROUP, "readers")]);
let decision = client.check(&subject, "download", "/books/HarryPotter", &[]).await?;
println!("{} {:?} {:?}", decision.allowed, decision.reason, decision.error_message);
```

For mutual TLS use `Auth::mutual_tls(cert_pem, key_pem)?`. `http://` is
accepted only for `Backend::SpeedlePlus` with `Auth::none()`, and only for
loopback hosts unless `allow_insecure_http(true)` is set. Speedle+ warns that
a plaintext endpoint has no authentication; keep it on a trusted network.
An unknown service on Speedle+ is not an error: the decision is a deny with
reason `ServiceNotFound`.

## Calls

- `is_allowed(subject, action, resource, attributes)` returns the `allowed`
  value of a 200 response.
- `check(...)` returns a `Decision` with `allowed`, `reason`, `error_message`
  (Speedle+ evaluation errors) and `quota_daily_remaining` (Speedle Cloud).
- `is_allowed_with` and `check_with` take `CallOptions { service_name }` to
  override the service name for one call.
- `Reason::DiscoverMode` comes with `allowed: true` but is not a production
  grant.

`Client` is cheap to clone; clones share the connection pool and the breaker.
Build it once per endpoint. Dropping a call's future cancels it, including
during a retry delay; `tokio::time::timeout` gives a shorter bound.

## Attributes

Each attribute has an explicit Speedle+ type: `Attribute::string`/`strings`,
`numeric`/`numerics`, `bool`/`bools`, `datetime`/`datetimes` (a
`SystemTime`, sent as RFC 3339 in UTC). They are sent in your order.

`Attribute::infer(&serde_json::Map)` builds them from JSON values: strings,
numbers, booleans and non-empty homogeneous arrays, sorted by name. JSON has
no time type, so it never yields `datetime`, and a string that looks like a
date stays a string. `null`, empty or mixed arrays and objects are rejected.

A call fails with `ErrorKind::InvalidRequest`, without sending anything, for
an empty or duplicate attribute name, a NaN or infinite number, or an empty
subject. Speedle+ decodes numbers as `f64`, so integers above 2^53 lose
precision.

Subjects and attributes must come from your application's own authenticated
context, never from untrusted client input.

## Errors

`speedle::Error` has `kind()`, `status()`, `code()` (Speedle Cloud error code),
`retry_after()` and `quota_daily_remaining()`.

| `ErrorKind` | When |
| --- | --- |
| `Configuration` | `build` rejected the options |
| `InvalidRequest` | 400, or the checks before sending |
| `Unauthorized` | 401 |
| `Forbidden` | 403, including Cloud `service_mismatch` |
| `RequestTooLarge` | 413 |
| `TooManyRequests` | 429 with an unknown Cloud code |
| `RateLimited` | burst 429 |
| `QuotaExceeded` | Cloud daily quota (`daily_quota_exceeded`) |
| `ServiceUnavailable` | 503 |
| `Server` | 500, 502, 504 and other 5xx |
| `Http` | any other status, including 3xx (redirects are never followed) and 404 |
| `UnexpectedResponse` | a 200 whose body is not a decision |
| `Network` | transport failure, including TLS |
| `Timeout` | the last attempt hit `attempt_timeout` |
| `DeadlineExceeded` | the call's deadline ended it |
| `CircuitOpen` | the breaker rejected the call |

## Retries and deadline

| `RetryPolicy` field | Default |
| --- | --- |
| `max_attempts` (counting the first; `RetryPolicy::disabled()` sets 1) | 3 |
| `base_delay` | 100 ms |
| `max_delay` | 2 s |
| `attempt_timeout` | 2 s |
| `deadline` (whole call) | 5 s |

Backoff is exponential with full jitter, raised to `Retry-After` when the
server sends one. Retried: connection errors, attempt timeouts, 502, 503,
504, and a burst 429 whose `Retry-After` fits in the remaining deadline.
Never retried: 400, 401, 403, 404, the daily-quota 429, 500 and other 5xx,
413, 415, unexpected bodies and TLS failures. On Speedle Cloud every attempt
that reaches the service is charged against the daily quota, so a retried
`snapshot_unavailable` 503 costs one unit per attempt; `max_attempts: 1`
turns retries off.

## Failure mode

`.failure_mode(FailureMode::Closed)` makes `is_allowed` return `Ok(false)`
instead of any crate error, including circuit-open and validation errors;
`on_give_up` still fires. `check` always returns the error. There is no
fail-open mode.

## Circuit breaker

Off by default. `.circuit_breaker(CircuitBreaker::default())` turns it on
with 5 consecutive failed calls and a 30 s cool-down. Network errors,
timeouts and 5xx count as failures; a 200 or 4xx resets the count. While
open, calls fail at once with `CircuitOpen`; after the cool-down one trial
call is let through.

## Hooks

Implement `speedle::Hooks` (`on_attempt`, `on_retry`, `on_give_up`,
`on_breaker_state_change`, all with empty defaults) and pass it to
`.hooks(...)`. Hooks run synchronously and should be cheap; a panic in a hook
is caught and ignored. Payloads never contain the token, headers, subject,
action, resource or attributes. The crate sends nothing but the is-allowed
request: no telemetry and no logging.

## Blocking client

With the `blocking` feature, `speedle::blocking::Client` has the same builder
and methods without `async`. It runs on its own small Tokio runtime and must
not be called from inside an async runtime. A call is bounded by the retry
deadline only.

## Requests on the wire

`User-Agent: speedle-sdk-rust/0.1.0 (rust/<version>; <os>)`, plus
`user_agent_suffix`. The body is the Speedle+ `JsonContext` for both
backends. Server trust is the platform's root store plus any
`add_root_certificate_pem` CAs; certificate verification cannot be disabled.
Like `reqwest`, the client honours the standard `HTTPS_PROXY`/`NO_PROXY`
environment variables.

## Development

```sh
cargo test --all-features                          # unit tests
cargo test --lib conformance -- --ignored          # vectors against ../conformance/mock (needs Go)
```
