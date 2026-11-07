# @speedle/sdk

Node.js/TypeScript SDK for Speedle authorization checks: `is-allowed` against
[Speedle Cloud](https://cloud.speedle.io) and [self-hosted
Speedle](https://github.com/teramoby/speedle-plus). Node.js 22 or later, ESM and
CommonJS, no runtime dependencies. See the [Node.js SDK
documentation](https://engine.speedle.io/docs/sdks/node/) and [design
specification](https://github.com/teramoby/speedle-sdk/blob/main/docs/design.md).

## Install

```sh
npm install @speedle/sdk
```

## Quickstart: self-hosted Speedle

```ts
import { Auth, SpeedleClient } from "@speedle/sdk";

const speedle = new SpeedleClient({
  backend: "speedle-plus",
  endpoint: "http://127.0.0.1:6734",
  auth: Auth.none(),
  serviceName: "books",
});

try {
  const decision = await speedle.check("alice", "get", "/books/123", { team: "eng", clearance: 4 });
  console.log(decision); // { allowed: true, reason: 0 }
} finally {
  await speedle.close();
}
```

For HTTPS, use `Auth.mutualTls({ cert, key })` when ADS runs with
`--force-client-cert`, and set `caCert` to an extra PEM trust anchor for a
private CA. `http://` is accepted only for `speedle-plus` with `Auth.none()`,
and only for loopback hosts unless you set `allowInsecureHttp: true`.
Self-hosted Speedle warns that an unauthenticated plaintext endpoint lets
anyone on the network ask for decisions; prefer HTTPS.

## Quickstart: Speedle Cloud (coming soon)

```ts
import { Attr, Auth, SpeedleClient } from "@speedle/sdk";

const speedle = new SpeedleClient({
  backend: "speedle-cloud",
  endpoint: "https://cloud.speedle.io",
  auth: Auth.bearer(process.env.SPEEDLE_TOKEN!),
  serviceName: "books", // the token's service
});

if (await speedle.isAllowed("alice", "get", "/books/123", [
  Attr.string("team", "eng"),
  Attr.numeric("clearance", 4),
  Attr.datetime("issuedAt", new Date("2026-11-05T03:00:00Z")),
])) {
  // allowed
}
```

CommonJS works the same way: `const { SpeedleClient } = require("@speedle/sdk")`.

## API

- `new SpeedleClient(options)`: create one client per endpoint and share it.
  It is safe for concurrent use and keeps one keep-alive connection pool.
  `close()` releases the pool.
- `isAllowed(subject, action, resource, attributes?, options?)` returns the
  server's `allowed` value.
- `check(...)` returns a `Decision`: `allowed`, `reason` (a `Reason`, or an
  unknown integer as is), `errorMessage` (self-hosted Speedle evaluator errors)
  and `quotaDailyRemaining` (Speedle Cloud).
- `subject` is a user name or a list of principals `{ type?, name, idd? }`;
  `type` defaults to `"user"` (`PrincipalType` has the Speedle values).
- `attributes` is either an array built with `Attr.string`, `Attr.numeric`,
  `Attr.bool` and `Attr.datetime` (each takes one value or an array), or a
  plain object whose types are inferred: string, number, boolean, `Date`, or
  a non-empty array of one of them. A string is always a string, even if it
  looks like a date. Inferred attributes are sent sorted by name. Invalid
  values (empty or duplicate names, `null`, NaN, infinities, invalid dates,
  mixed or empty inferred arrays) raise `InvalidRequestError` before any
  request. Numbers above 2^53 lose precision, because self-hosted Speedle
  decodes numbers as `float64`.
- `options.serviceName` overrides the client's `serviceName` for one call.
  On Speedle Cloud a service other than the token's fails with
  `ForbiddenError` (`code` `service_mismatch`); that call is charged.
- `options.signal`: aborting stops the call, also during a retry delay, and
  rejects with `signal.reason` in every failure mode.
  `AbortSignal.timeout(ms)` gives a shorter bound for one call.

`Reason.DiscoverMode` (5) returns `allowed: true` but is not a production
grant.

## Retries and deadlines

| Setting (`retry`) | Default |
| --- | --- |
| `maxAttempts` (counts the first; `1` disables retries) | 3 |
| `baseDelayMs` | 100 |
| `maxDelayMs` | 2000 |
| `attemptTimeoutMs` | 2000 |
| `deadlineMs` (the whole call) | 5000 |

Backoff is exponential with full jitter, raised to `Retry-After` when the
server sends one. Retried: connection errors, attempt timeouts, 502, 503 and
504, and a burst 429 whose `Retry-After` fits the remaining deadline. Never
retried: 400, 401, 403, 404, the daily-quota 429, 500 and other 5xx, 413, 415,
TLS failures and unexpected bodies. Speedle Cloud charges every call that
reaches its handler, including a retried `snapshot_unavailable` 503, so a
call costs at most `maxAttempts` units.

## Errors

Every error extends `SpeedleError` and carries `status`, `code` (Speedle Cloud
error code), `message` and `quotaDailyRemaining`. The types are
`ConfigurationError`, `InvalidRequestError`, `UnauthorizedError`,
`ForbiddenError`, `RequestTooLargeError`, `TooManyRequestsError` (with
`retryAfter` in seconds; subtypes `RateLimitedError` and
`QuotaExceededError`), `ServiceUnavailableError`, `ServerError`, `HttpError`,
`UnexpectedResponseError`, `NetworkError` (subtype `TimeoutError`, whose
subtype is `DeadlineExceededError`) and `CircuitOpenError`. The SDK's
`TimeoutError` is its own class, not the global one.

The ESM and CommonJS builds are separate module instances: use one of them
in an application so that `instanceof` checks match.

## Failure mode

`failureMode: "closed"` makes `isAllowed` return `false` instead of raising
any SDK error, including `CircuitOpenError` and validation errors; `onGiveUp`
still reports the error. `check` always raises. Constructor errors and
cancellation are never converted. There is no fail-open mode: `isAllowed`
returns `true` only for a 200 response whose `allowed` is `true`.

## Circuit breaker

Off by default. `circuitBreaker: { failureThreshold: 5, coolDownMs: 30000 }`
opens after that many consecutive failed calls (network errors, timeouts and
5xx), fails fast with `CircuitOpenError` during the cool-down, then lets one
trial call through.

## Hooks

`hooks: { onAttempt, onRetry, onGiveUp, onBreakerStateChange }` are called
synchronously; exceptions and return values are ignored. Payloads never
contain the token, headers, subject, action, resource or attributes. The SDK
sends no telemetry and does no logging; it sends only the `is-allowed`
request, with `User-Agent: speedle-sdk-node/<version> (node/<version>; <os>)`
plus `userAgentSuffix`.

## Security

- The Speedle Cloud token is a server-side secret. Never ship it to browsers,
  mobile apps or desktop clients. This package targets Node.js only.
- `Auth.bearer` requires HTTPS. The token is redacted from `toString`,
  `JSON.stringify`, `util.inspect`, hook payloads and error messages.
- Redirects are never followed. TLS verification cannot be disabled.
- Take subjects and attributes from your own authenticated context, never
  from untrusted client input.
- Decisions are never cached.

Report security issues privately using the [Speedle security
policy](https://github.com/teramoby/speedle-sdk/blob/main/SECURITY.md).

## Development

```sh
npm ci
npm run lint
npm test             # unit tests
npm run conformance  # every vector in ../conformance/vectors
npm run test:package # build ESM and CJS, then load both
```

## License

[UPL-1.0](https://github.com/teramoby/speedle-sdk/blob/main/node/LICENSE). Copyright (c) 2026 The Speedle Authors.
