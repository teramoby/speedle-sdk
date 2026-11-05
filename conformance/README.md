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
| `backend` | `speedle-cloud` or `speedle-plus`. Selects the wire format the SDK must produce. |
| `config` | Client options: `auth` (`none` or `bearer`), `serviceName`, `timeoutMs`, `retry`. The runner adds the endpoint (the mock's base URL) and the mock's test CA. |
| `call` | `method` (`isAllowed` or `check`), `subject` (a user name or a list of principals), `action`, `resource`, optional `attributes`. |
| `exchanges` | The requests the SDK must send, in order, and the mock's answer to each. An empty list means the SDK must fail before sending anything. |
| `expect` | Exactly one of `result` (the `isAllowed` boolean), `decision` (the `check` result) or `error` (the error type and its fields). |

Matching rules:

- Request bodies are compared as parsed JSON. Key order is ignored. Array
  order matters except for arrays named in `unorderedArrays` (JSON Pointers).
- Request headers: names are case-insensitive. A value is an exact string,
  `{"pattern": "<regex>"}`, or `{"absent": true}`. Unlisted headers are not
  checked.
- A response is either `status` with optional `headers` and `body` (JSON) or
  `rawBody` (sent as is), or a `fault`: `connection_reset` or `no_response`.
- In `expect.error`, a field that is present must match, `null` means the SDK
  must report no value, and an absent field is not checked. `type` uses the
  TypeScript, Python and Java error names; the Go runner maps them to the
  `Err*` sentinels.
- More requests than `exchanges`, fewer, or any mismatch fails the vector.

## Running a vector

1. The mock server serves HTTPS on `127.0.0.1` with a throwaway CA created at
   start-up, so bearer-token vectors exercise the same HTTPS-only rule as
   production.
2. The runner loads a vector into the mock, builds a client from `config`
   plus the mock endpoint and CA, and makes `call`.
3. The runner compares the outcome with `expect`, then asks the mock whether
   every exchange was consumed and matched.

Vectors set small retry delays (`baseDelayMs: 1`) so retry vectors run fast.
Mutual TLS is not covered here; each SDK tests it with its own TLS fixtures.

## Examples

| Vector | Covers |
| --- | --- |
| `cloud-allowed` | 200 allow, nested attributes sent as an object, bearer header, `X-Quota-Daily-Remaining` on the decision |
| `speedle-plus-denied` | 200 deny, no `Authorization`, attributes converted to Speedle+ `{name, type, value}` items |
| `cloud-invalid-request` | 400 `invalid_request`, charged, no retry |
| `speedle-plus-invalid-attribute` | Client-side rejection of a `null` attribute for Speedle+, no request sent |
| `cloud-unauthorized` | 401, not charged, no retry |
| `cloud-daily-quota-exceeded` | 429 `daily_quota_exceeded` with `Retry-After: 3600`, no retry |
| `cloud-5xx-then-success` | 503 followed by a successful retry |

The bearer token in the vectors is the example credential from the Speedle
Cloud contract. It is not a real token.

## Adding a vector

Add `vectors/<id>.json`, validate it against `schema.json`, and make every
SDK pass it in the same PR, or mark the PR as contract-only and open one
issue per SDK.
