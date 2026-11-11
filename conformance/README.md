# Conformance suite

Language-neutral test vectors that every SDK must pass in CI. The design is
in [docs/design.md](../docs/design.md) §11. The shared mock server is in
`mock/` (see [Mock server](#mock-server)); each SDK's runner lives in that
SDK's directory.

## Files

- `schema.json`: JSON Schema (draft 2020-12) for one vector.
- `vectors/<id>.json`: one vector per file; `id` equals the file name.

## Vector format

| Field | Meaning |
| --- | --- |
| `backend` | `speedle-cloud` or `speedle`. Both get the same Speedle `JsonContext` body; the backend changes only auth and error handling. |
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

## Mock server

`conformance/mock` is a Go program with no dependencies outside the
standard library (Go 1.26 or later). A runner starts it once per test run
and loads one vector at a time.

Start it from the repository root:

```sh
go build -C conformance/mock -o "$TMPDIR/speedle-mock" . && "$TMPDIR/speedle-mock" -exit-on-stdin-close
# or, without a separate build step:
go -C conformance/mock run . -exit-on-stdin-close
```

Once both ports listen, it prints exactly one JSON line on stdout and
nothing else:

```json
{"endpoint":"https://127.0.0.1:41234","control":"http://127.0.0.1:41235","caFile":"/tmp/speedle-mock-ca-123.pem","caPem":"-----BEGIN CERTIFICATE-----\n..."}
```

| Field | Meaning |
| --- | --- |
| `endpoint` | The SDK's base URL: HTTPS on an ephemeral loopback port, HTTP/1.1 only. Every path and method is answered from the script, so a wrong path is reported as a mismatch. |
| `control` | The control API: plain HTTP on another ephemeral loopback port. |
| `caFile`, `caPem` | The throwaway CA that signed the server certificate (for `127.0.0.1`, `::1` and `localhost`), as a file and inline. Pass it as the SDK's extra trust anchor. |

Flags: `-exit-on-stdin-close` exits when stdin reaches EOF, so the mock dies
with the runner that holds its stdin pipe; `-ca-file <path>` writes the CA to
a fixed path instead of a temporary file; `-host` changes the listen address
(default `127.0.0.1`); `-v` logs server errors to stderr. `SIGINT` and
`SIGTERM` also stop it.

Control API:

| Request | Effect |
| --- | --- |
| `POST /load` with the vector file as the body | Replaces the script with the vector's `exchanges` and clears all recorded results. Requests still held by `no_response` are released (their connections are reset). 204, or 400 with `{"error": "..."}` when the vector is invalid. The mock reads only `id` and `exchanges`. |
| `GET /verify` | `{"ok": true, "vector": "<id>", "expected": 2, "received": 2, "errors": []}`. `ok` is false when a request did not match its exchange, a request came after the last exchange, or an exchange was never requested. `errors` names the request number and the JSON Pointer or header that differs. |
| `POST /reset` | Loads an empty script: any request is then an error. |
| `GET /ca.pem` | The CA certificate. |
| `GET /healthz` | 204. |
| `POST /shutdown` | 204, then the mock exits. |

Replay rules:

- Requests are matched to `exchanges` in arrival order, across all calls and
  connections of the vector. Each request consumes one exchange, matched or
  not.
- A request after the last exchange is answered with 418 and
  `{"error": "conformance mock: unexpected request"}` and fails `verify`.
- A `status` response sends the scripted `headers` and either `body` as JSON
  (with `Content-Type: application/json` unless the vector sets it) or
  `rawBody` as is.
- `connection_reset` reads the whole request, then closes the TCP connection
  with a reset (`SO_LINGER` 0) before any response byte.
- `no_response` reads the whole request and holds it until the client closes
  the connection or the next `load`, then resets the connection.
- Header `pattern` matchers use Go's RE2 syntax, which covers the
  ECMAScript subset the vectors use, and must match the whole value.
  `Authorization` values are redacted in `errors`.
- Numbers in bodies are compared by value, so `4` equals `4.0`.

A runner loop:

1. Start the mock, read the first stdout line, keep the process handle.
2. For each vector: `POST /load`, build the client with `endpoint` and the
   CA, run the call or steps, compare with `expect` and `events`, then
   `GET /verify` and fail the vector unless `ok` is true.
3. `POST /shutdown`, or close the mock's stdin.

The mock's own tests (`go test ./...` in `conformance/mock`) replay every
vector as a conforming SDK would send it, so a vector the mock cannot serve
fails the `conformance` workflow.

## Examples

| Vector | Covers |
| --- | --- |
| `cloud-allowed` | 200 allow; every attribute type in single and array form, including `datetime` and an empty array; bearer header; `X-Quota-Daily-Remaining` on the decision |
| `speedle-typed-attributes` | The same call and request body as `cloud-allowed` against self-hosted Speedle, without `Authorization` |
| `speedle-denied` | 200 deny, no `Authorization`, inferred attributes sent as typed items sorted by name |
| `cloud-invalid-request` | 400 `invalid_request`, charged, no retry |
| `cloud-service-mismatch` | Older Speedle Cloud: a per-call `serviceName` that is not the API key's service returns 403 `service_mismatch`, `ForbiddenError`, charged, no retry |
| `attribute-null-rejected` | Client-side rejection of a `null` inferred attribute, no request sent |
| `cloud-unauthorized` | 401, not charged, never retried |
| `cloud-daily-quota-exceeded` | 429 `daily_quota_exceeded` with `Retry-After: 3600`, never retried although attempts remain |
| `retry-503-503-200` | 503, 503, 200: succeeds on attempt 3 after 100 ms and 200 ms delays |
| `retry-network-error-then-200` | Self-hosted Speedle: connection reset, then an allow |
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
