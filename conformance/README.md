# Conformance suite

Language-neutral test vectors that every SDK must pass in CI. The design is
in [docs/design.md](../docs/design.md) §11. Only the format and examples
exist so far; the mock server and the per-language runners come with the
implementation.

## Files

- `schema.json`: JSON Schema (draft 2020-12) for one vector.
- `vectors/<id>.json`: one vector per file; `id` equals the file name.

## Vector format

| Field | Meaning |
| --- | --- |
| `backend` | `speedle-cloud` or `speedle-plus`. Both get the same Speedle+ `JsonContext` body; the backend changes only auth and error handling. |
| `config` | Client options: `auth` (`none` or `bearer`), `serviceName` (required), `retry` (`maxAttempts`, `baseDelayMs`, `maxDelayMs`, `attemptTimeoutMs`, `deadlineMs`), `failureMode` (`error` or `closed`), `circuitBreaker` (`failureThreshold`, `coolDownMs`). Unset values take the SDK defaults. The runner adds the endpoint (the mock's base URL) and the mock's test CA. |
| `jitterFraction` | The value in [0, 1] that the injected random source returns; each backoff delay is `jitterFraction × min(maxDelay, baseDelay × 2^(n-1))`. Default 1. |
| `call`, `expect` | One call: `method` (`isAllowed` or `check`), `subject` (a user name or a list of principals), `action`, `resource`, an optional `serviceName` override, and at most one of `attributes` (typed `{name, type, value}` items) or `inferredAttributes` (a name-to-value map for the inferred overload); and its outcome. |
| `steps` | Instead of `call` and `expect`: several calls on the same client, each with optional `advanceMs` (virtual time added before the call), `call` and `expect`. Used for breaker vectors. |
| `exchanges` | The requests the SDK must send across all calls, in order, and the mock's answer to each: a response or a fault. An empty list means the SDK must fail before sending anything. |
| `events` | Optional. The complete, ordered list of hook events across all calls: `attempt`, `retry`, `giveUp`, `breaker`. |

`expect` holds exactly one of `result` (the `isAllowed` boolean), `decision`
(the `check` result) or `error` (the error type and its fields), plus an
optional `delaysMs`: the exact retry sleeps the call requested, in order
(`[]` means none).

Attributes: for each `attributes` item the runner calls the SDK factory for
its `type` and form (single value or array). A `datetime` value is RFC 3339
in UTC with whole seconds, such as `2026-11-05T03:00:00Z`; the runner parses
it into the language's time type, and the SDK must send the same string.
`inferredAttributes` goes to the inferred-map overload as the language's
native map, so it cannot hold `datetime` values; each SDK tests time-type
inference in its own unit tests.

Matching rules:

- Request bodies are compared as parsed JSON. Key order is ignored. Array
  order matters except for arrays named in `unorderedArrays` (JSON Pointers).
- Request headers: names are case-insensitive. A value is an exact string,
  `{"pattern": "<regex>"}`, or `{"absent": true}`. Unlisted headers are not
  checked.
- A response is either `status` with optional `headers` and `body` (JSON) or
  `rawBody` (sent as is), or a `fault`: `connection_reset` or `no_response`.
- In `expect.error` and in `events`, a field that is present must match,
  `null` means the SDK must report no value, and an absent field is not
  checked. `type` uses the
  TypeScript, Python and Java error names; the Go runner maps them to the
  `Err*` sentinels.
- More requests than `exchanges`, fewer, or any mismatch fails the vector.
- `events` is compared as a complete sequence; the runner records every
  hook call.

## Running a vector

1. The mock server serves HTTPS on `127.0.0.1` with a throwaway CA created at
   start-up, so bearer-token vectors exercise the same HTTPS-only rule as
   production.
2. The runner loads a vector into the mock, builds one client from `config`
   plus the mock endpoint and CA, the virtual clock, the fixed random source
   and recording hooks, and makes `call` or each of `steps`.
3. The runner compares the outcome with `expect`, then asks the mock whether
   every exchange was consumed and matched.

Virtual clock: the SDK's retry loop and breaker read time through an
internal clock, which the runner replaces. Its `sleep` returns at once and
adds the duration to an offset, and `now` is real monotonic time plus that
offset. Backoff delays, `Retry-After` waits and breaker cool-downs therefore
cost no real time and are exact. Attempt timeouts against a `no_response`
fault are real, so such vectors keep `attemptTimeoutMs` or `deadlineMs`
small. See [docs/design.md](../docs/design.md) §6.3 for the retry algorithm
the vectors follow.

Mutual TLS is not covered here; each SDK tests it with its own TLS fixtures.

## Examples

| Vector | Covers |
| --- | --- |
| `cloud-allowed` | 200 allow; every attribute type in single and array form, including `datetime` and an empty array; bearer header; `X-Quota-Daily-Remaining` on the decision |
| `speedle-plus-typed-attributes` | The same call and request body as `cloud-allowed` against Speedle+, without `Authorization` |
| `speedle-plus-denied` | 200 deny, no `Authorization`, inferred attributes sent as typed items sorted by name |
| `cloud-invalid-request` | 400 `invalid_request`, charged, no retry |
| `cloud-service-mismatch` | Per-call `serviceName` override that is not the token's service: 403 `service_mismatch`, `ForbiddenError`, charged, no retry |
| `attribute-null-rejected` | Client-side rejection of a `null` inferred attribute, no request sent |
| `cloud-unauthorized` | 401, not charged, never retried |
| `cloud-daily-quota-exceeded` | 429 `daily_quota_exceeded` with `Retry-After: 3600`, never retried although attempts remain |
| `retry-503-503-200` | 503, 503, 200: succeeds on attempt 3 after 100 ms and 200 ms delays |
| `retry-network-error-then-200` | Speedle+: connection reset, then an allow |
| `retry-burst-429-retry-after` | Burst 429 `rate_limited` with `Retry-After: 1`: waits 1 s, then allowed |
| `retry-burst-429-exceeds-deadline` | Burst 429 with `Retry-After: 60` does not fit the 5 s deadline: `RateLimitedError`, no retry |
| `retry-deadline-exceeded` | 503, then no answer until the 300 ms deadline: `DeadlineExceededError` |
| `retry-fail-closed` | `failureMode: closed`: 503 twice, `isAllowed` returns `false` |
| `breaker-opens-and-recovers` | Breaker opens after 2 failed calls, fails fast, recovers through a half-open trial after the cool-down |

The bearer token in the vectors is the example credential from the Speedle
Cloud contract. It is not a real token.

## Adding a vector

Add `vectors/<id>.json`, validate it against `schema.json`, and make every
SDK pass it in the same PR, or mark the PR as contract-only and open one
issue per SDK.
