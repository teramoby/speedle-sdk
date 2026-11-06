# Speedle SDK for .NET

Client for the `is-allowed` authorization check of
[Speedle Cloud](https://cloud.speedle.io) and
[self-hosted Speedle](https://github.com/teramoby/speedle-plus) ADS.

- NuGet `Speedle.Sdk`, namespace `Speedle.Sdk`, target `net8.0` (.NET 8 or later).
- No dependencies: `HttpClient` over `SocketsHttpHandler` and `System.Text.Json`
  source generation. Trimming and AOT compatible.
- License: UPL-1.0.

Published on NuGet: 0.1.0 (2026-11-05). The design is in [docs/design.md](../docs/design.md).

## Install

```sh
dotnet add package Speedle.Sdk
```

## Quickstart: Speedle Cloud

```csharp
using Speedle.Sdk;

await using var speedle = new SpeedleClient(new SpeedleClientOptions
{
    Backend = Backend.SpeedleCloud,
    Endpoint = new Uri("https://cloud.speedle.io"),
    Auth = Auth.Bearer(Environment.GetEnvironmentVariable("SPEEDLE_TOKEN")!),
    ServiceName = "books",              // must be the token's service
});

if (await speedle.IsAllowedAsync("alice", "get", "/books/123",
        [SpeedleAttribute.String("team", "eng"),
         SpeedleAttribute.Numeric("clearance", 4),
         SpeedleAttribute.Datetime("issuedAt", DateTimeOffset.Parse("2026-11-05T03:00:00Z"))],
        cancellationToken: ct))
{
    // ...
}

// Inferred form: types come from the .NET values; sent sorted by name.
await speedle.IsAllowedAsync("alice", "get", "/books/123",
    new Dictionary<string, object?> { ["team"] = "eng", ["clearance"] = 4 });
```

The token is a server-side secret. Never ship it to browsers, mobile apps or
desktop clients.

## Quickstart: self-hosted Speedle

```csharp
await using var speedle = new SpeedleClient(new SpeedleClientOptions
{
    Backend = Backend.SpeedlePlus,
    Endpoint = new Uri("https://ads.internal.example:6734"),
    Auth = Auth.None(),                 // or Auth.MutualTls(certificateWithKey)
    ServiceName = "books",
    ExtraTrustedCertificates = new X509Certificate2Collection(myCa), // optional
});

Decision d = await speedle.CheckAsync(
    Subject.Of(Principal.User("alice"), Principal.Group("eng")), "get", "/books/123");
Console.WriteLine($"{d.Allowed} {d.Reason}");
```

`http://` is accepted only for `SpeedlePlus` with `Auth.None()`, and only for
loopback hosts unless you set `AllowInsecureHttp = true`. A bearer token is
never sent over plaintext. Self-hosted Speedle itself warns against
unauthenticated plaintext endpoints; keep them on a trusted network.

## API

- `IsAllowedAsync(subject, action, resource, attributes?, options?, cancellationToken)`
  returns the server's `allowed` value.
- `CheckAsync(...)` returns a `Decision`: `Allowed`, `Reason` (unknown codes are
  kept as the cast integer), `ErrorMessage` (self-hosted Speedle evaluator
  errors) and `QuotaDailyRemaining` (Speedle Cloud).
- `Reason.DiscoverMode` means the server allows everything while it records
  requests. It is not a production grant.
- Subjects: a string (one `user` principal), `Subject.User(name)` or
  `Subject.Of(principals)`. Principal types are open strings; `PrincipalTypes`
  has the four Speedle values.
- Typed attributes: `SpeedleAttribute.String/Strings`, `Numeric/Numerics`,
  `Bool/Bools`, `Datetime/Datetimes`. `Datetime` takes a `DateTimeOffset` and is
  sent as RFC 3339 in UTC. Numbers are sent as 64-bit floats; integers above
  2^53 lose precision on the server.
- Inferred attributes: an `IReadOnlyDictionary<string, object?>` of `string`,
  numeric primitives, `bool`, `DateTimeOffset`, or a non-empty sequence of one
  of these. A `string` is always `string`, even if it looks like a date.
  `DateTime` is rejected because its time zone can be unspecified. Anything else
  throws `InvalidRequestException`; use the typed form then.
- `new CallOptions { ServiceName = "orders" }` overrides the service for one call.
- Subjects and attributes must come from your application's own
  authenticated context, never from untrusted client input.

The client is thread-safe and owns one keep-alive connection pool
(`PooledConnectionLifetime` 5 minutes). Create it once per endpoint and share
it; `Dispose` releases the pool.

## Errors

All errors derive from `SpeedleException`, with `Status`, `Code` (Speedle Cloud
error code), `Message` and `QuotaDailyRemaining`. Messages never contain the
token or the request.

| Exception | When | Retried |
| --- | --- | --- |
| `ConfigurationException` | Invalid options | |
| `InvalidRequestException` | 400, or a client-side check failed (nothing sent) | No |
| `UnauthorizedException` | 401 | No |
| `ForbiddenException` | 403, including Cloud `service_mismatch` | No |
| `RequestTooLargeException` | 413 | No |
| `TooManyRequestsException` | 429 with an unknown code; has `RetryAfter` | No |
| `RateLimitedException` | Burst 429 | If `Retry-After` fits the deadline |
| `QuotaExceededException` | 429 `daily_quota_exceeded` | Never |
| `ServiceUnavailableException` | 503 | Yes |
| `ServerException` | 500 and other 5xx | 502 and 504 only |
| `HttpException` | Other statuses, including 3xx (redirects are never followed) and 404 | No |
| `UnexpectedResponseException` | Body is not the expected JSON | No |
| `NetworkException` | Transport failure | Yes, except TLS failures |
| `SpeedleTimeoutException` | The last attempt hit `AttemptTimeout` | Yes |
| `DeadlineExceededException` | The call's deadline ran out | |
| `CircuitOpenException` | The breaker rejected the call | |

`RateLimitedException` and `QuotaExceededException` derive from
`TooManyRequestsException`; `DeadlineExceededException` derives from
`SpeedleTimeoutException`, which derives from `NetworkException`.

## Retries, deadline and cancellation

`new RetryPolicy()`: 3 attempts, 100 ms base delay, 2 s cap, 2 s per attempt,
5 s per call. Backoff is exponential with full jitter, raised to the server's
`Retry-After`. With Cloud's `Retry-After: 60` and the 5 s deadline, a burst 429
is raised at once.

On Speedle Cloud every attempt that reaches the server is charged against the
daily quota, including a retried 503 `snapshot_unavailable`.
`RetryPolicy.Disabled` turns retries off.

A cancelled `cancellationToken` stops the call, including during a retry delay,
and throws `OperationCanceledException`, also in `FailureMode.Closed`. Use
`CancellationTokenSource.CancelAfter` for a shorter per-call bound.

## Failure mode

`FailureMode = FailureMode.Closed` makes `IsAllowedAsync` return `false` instead
of throwing any SDK exception. `CheckAsync` still throws. There is no fail-open
mode: `IsAllowedAsync` returns `true` only for an HTTP 200 whose `allowed` is
`true`.

## Circuit breaker

Off by default. `CircuitBreaker = new CircuitBreakerPolicy()` opens after 5
consecutive failed calls (network errors, timeouts and 5xx), fails fast with
`CircuitOpenException` for 30 s, then lets one trial call through.

## Hooks

`Hooks = new SpeedleHooks { OnAttempt = ..., OnRetry = ..., OnGiveUp = ...,
OnBreakerStateChange = ... }` run synchronously on the calling task.
Exceptions from hooks are ignored. Payloads never contain the token, headers,
subject, action, resource, attributes or body.

The SDK has no logging and no telemetry: it sends nothing but the
`is-allowed` request. It sets `User-Agent: speedle-sdk-dotnet/<version>
(dotnet/<runtime version>; <os>)`, plus `UserAgentSuffix` if set.

## Development

```sh
dotnet format whitespace Speedle.Sdk.sln --verify-no-changes
dotnet build Speedle.Sdk.sln -c Release          # warnings are errors
dotnet test Speedle.Sdk.sln --filter "Category!=Conformance"
go build -C ../conformance/mock -o /tmp/speedle-mock .
SPEEDLE_MOCK=/tmp/speedle-mock dotnet test Speedle.Sdk.sln --filter "Category=Conformance"
```

Without `SPEEDLE_MOCK`, the conformance tests use `go run` in
`conformance/mock`.
