# Speedle SDK for Java

Client for the `is-allowed` authorization check of
[Speedle Cloud](https://cloud.speedle.io) and
[self-hosted Speedle](https://github.com/teramoby/speedle-plus) ADS.

- Maven `io.speedle:speedle-sdk`, Java package and JPMS module `io.speedle.sdk`.
- Java 17 or later. No runtime dependencies: transport is `java.net.http.HttpClient`.
- License: UPL-1.0.

Published on Maven Central: 0.1.0 (2026-11-05). This README describes 0.2.0,
which is not yet released. The design is in [docs/design.md](../docs/design.md).
See the [Java SDK documentation](https://engine.speedle.io/docs/sdks/java/).

## Install

```xml
<dependency>
  <groupId>io.speedle</groupId>
  <artifactId>speedle-sdk</artifactId>
  <version>0.2.0</version>
</dependency>
```

With JPMS, add `requires io.speedle.sdk;`.

## Quickstart: self-hosted Speedle

```java
try (SpeedleClient speedle = SpeedleClient.builder()
    .backend(Backend.SPEEDLE)
    .endpoint(URI.create("http://127.0.0.1:6734"))
    .auth(Auth.none())
    .serviceName("books")
    .build()) {
  Decision d = speedle.check(Subject.of(Principal.user("alice"), Principal.group("eng")),
      "get", "/books/123", List.of());
  System.out.println(d.allowed() + " " + d.reason());
}
```

For HTTPS, use `Auth.mutualTls(keyStore, password)` when required and set
`trustStore(myCaTrustStore)` for a private CA. `http://` is accepted only for
`Backend.SPEEDLE` with `Auth.none()`, and only for loopback hosts unless you set
`allowInsecureHttp(true)`. A bearer token is never sent over plaintext.
Self-hosted Speedle itself warns against unauthenticated plaintext endpoints;
keep them on a trusted network.

## Quickstart: Speedle Cloud (coming soon)

```java
import io.speedle.sdk.*;
import java.net.URI;
import java.time.Instant;
import java.util.List;
import java.util.Map;

try (SpeedleClient speedle = SpeedleClient.builder()
    .backend(Backend.SPEEDLE_CLOUD)
    .endpoint(URI.create("https://cloud.speedle.io"))
    .auth(Auth.bearer(System.getenv("SPEEDLE_TOKEN")))
    .serviceName("books")               // must be the token's service
    .build()) {

  boolean ok = speedle.isAllowed(Subject.user("alice"), "get", "/books/123", List.of(
      Attribute.string("team", "eng"),
      Attribute.numeric("clearance", 4),
      Attribute.datetime("issuedAt", Instant.parse("2026-11-05T03:00:00Z"))));

  // Inferred form: types come from the Java values; sent sorted by name.
  speedle.isAllowed(Subject.user("alice"), "get", "/books/123",
      Map.of("team", "eng", "clearance", 4));
}
```

The token is a server-side secret. Never ship it to browsers, mobile apps or
desktop clients.

## API

- `isAllowed(subject, action, resource[, attributes[, options]])` returns the
  server's `allowed` value.
- `check(...)` returns a `Decision`: `allowed`, `reason` (enum; `UNKNOWN` keeps
  unknown codes in `reasonCode`), `errorMessage` (self-hosted Speedle evaluator
  errors) and `quotaDailyRemaining` (Speedle Cloud).
- `Reason.DISCOVER_MODE` means the server allows everything while it records
  requests. It is not a production grant.
- Subjects: `Subject.user(name)` or `Subject.of(principals...)`. Principal
  types are open strings with constants `USER`, `GROUP`, `ROLE`, `ENTITY`.
- Typed attributes: `Attribute.string`, `numeric`, `bool`, `datetime`, each
  with a single value or a `List`. `datetime` takes an `Instant` and is sent as
  RFC 3339 in UTC. Numbers are sent as 64-bit floats; integers above 2^53 lose
  precision on the server.
- Inferred attributes: a `Map<String, ?>` of `String`, finite `Number`,
  `Boolean`, `Instant`, or a non-empty `List` of one of these. A `String` is
  always `string`, even if it looks like a date. Anything else throws
  `InvalidRequestError`; use the typed form then.
- `CallOptions.withServiceName("orders")` and `CallOptions.withDeadline(...)`
  override the client settings for one call.
- Subjects and attributes must come from your application's own
  authenticated context, never from untrusted client input.

The client is thread-safe and owns one keep-alive connection pool. Create it
once per endpoint and share it; `close()` releases the pool.

## Errors

All errors are unchecked and extend `SpeedleException`, which carries
`status()`, `code()` (Speedle Cloud error code), `getMessage()` and
`quotaDailyRemaining()`. Messages never contain the token or the request.

| Error | When | Retried |
| --- | --- | --- |
| `ConfigurationError` | Invalid builder settings | |
| `InvalidRequestError` | 400, or a client-side check failed (nothing sent) | No |
| `UnauthorizedError` | 401 | No |
| `ForbiddenError` | 403, including Cloud `service_mismatch` | No |
| `RequestTooLargeError` | 413 | No |
| `TooManyRequestsError` | 429 with an unknown code; has `retryAfter()` | No |
| `RateLimitedError` | Burst 429 | If `Retry-After` fits the deadline |
| `QuotaExceededError` | 429 `daily_quota_exceeded` | Never |
| `ServiceUnavailableError` | 503 | Yes |
| `ServerError` | 500 and other 5xx | 502 and 504 only |
| `HttpError` | Other statuses, including 3xx (redirects are never followed) and 404 | No |
| `UnexpectedResponseError` | Body is not the expected JSON | No |
| `NetworkError` | Transport failure | Yes, except TLS failures |
| `TimeoutError` | The last attempt hit `attemptTimeout` | Yes |
| `DeadlineExceededError` | The call's deadline ran out | |
| `CircuitOpenError` | The breaker rejected the call | |
| `CancelledError` | The calling thread was interrupted | |

`RateLimitedError` and `QuotaExceededError` extend `TooManyRequestsError`;
`DeadlineExceededError` extends `TimeoutError`, which extends `NetworkError`.

## Retries, deadline and cancellation

`RetryPolicy.defaults()`: 3 attempts, 100 ms base delay, 2 s cap, 2 s per
attempt, 5 s per call. Backoff is exponential with full jitter, raised to the
server's `Retry-After`. With Cloud's `Retry-After: 60` and the 5 s deadline, a
burst 429 is raised at once.

On Speedle Cloud every attempt that reaches the server is charged against the
daily quota, including a retried 503 `snapshot_unavailable`.
`RetryPolicy.disabled()` turns retries off.

Interrupting the calling thread aborts the attempt or the retry delay and
throws `CancelledError` with the interrupt flag restored.

## Failure mode

`failureMode(FailureMode.CLOSED)` makes `isAllowed` return `false` instead of
throwing any SDK error. `check` still throws, and `CancelledError` is never
converted. There is no fail-open mode: `isAllowed` returns `true` only for an
HTTP 200 whose `allowed` is `true`.

## Circuit breaker

Off by default. `circuitBreaker(CircuitBreakerPolicy.defaults())` opens after
5 consecutive failed calls (network errors, timeouts and 5xx), fails fast with
`CircuitOpenError` for 30 s, then lets one trial call through.

## Hooks

`hooks(new SpeedleHooks() { ... })` receives `onAttempt`, `onRetry`, `onGiveUp`
and `onBreakerStateChange` on the calling thread. Exceptions from hooks are
ignored. Payloads never contain the token, headers, subject, action, resource,
attributes or body.

The SDK has no logging and no telemetry: it sends nothing but the
`is-allowed` request. It sets `User-Agent: speedle-sdk-java/<version>
(java/<version>; <os>)`, plus `userAgentSuffix` if set.

## Development

```sh
mvn -B spotless:check compile javadoc:javadoc   # lint (JDK 21 or later)
mvn -B test                                     # unit tests
go build -C ../conformance/mock -o /tmp/speedle-mock .
mvn -B test -Pconformance -Dspeedle.mock=/tmp/speedle-mock
```

Without `-Dspeedle.mock`, the conformance runner uses `go run` in
`conformance/mock`.
