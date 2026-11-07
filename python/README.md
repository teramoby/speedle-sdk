# speedle

Python SDK for Speedle authorization checks: `is-allowed` against
[Speedle Cloud](https://cloud.speedle.io) and [self-hosted
Speedle](https://github.com/teramoby/speedle-plus). Python 3.10 or later, sync
and asyncio clients, fully typed. The only runtime dependency is `httpx`.
The behaviour follows [docs/design.md](../docs/design.md).

> Published on PyPI: 0.1.0 (2026-11-05).

## Install

```sh
pip install speedle
```

## Quickstart: Speedle Cloud

```python
import datetime
import os

from speedle import Attribute, Auth, Client

with Client(
    backend="speedle-cloud",
    endpoint="https://cloud.speedle.io",
    auth=Auth.bearer(os.environ["SPEEDLE_TOKEN"]),
    service_name="books",  # the token's service
) as speedle:
    if speedle.is_allowed(
        "alice",
        "get",
        "/books/123",
        [
            Attribute.string("team", "eng"),
            Attribute.numeric("clearance", 4),
            Attribute.datetime("issued_at", datetime.datetime(2026, 11, 5, 3, tzinfo=datetime.timezone.utc)),
        ],
    ):
        ...
```

## Quickstart: self-hosted Speedle with asyncio

```python
from speedle import AsyncClient, Auth

async with AsyncClient(
    backend="speedle",
    endpoint="https://ads.internal:6734",
    auth=Auth.none(),  # or Auth.mutual_tls("client.pem", "client.key") with --force-client-cert
    ca_file="ca.pem",  # extra trust anchor for a private CA
    service_name="books",
) as speedle:
    decision = await speedle.check("alice", "get", "/books/123", {"team": "eng", "clearance": 4})
    # Decision(allowed=False, reason=<Reason.NO_APPLICABLE_POLICIES: 3>, ...)
```

`http://` is accepted only for backend `speedle` with `Auth.none()`, and only for
loopback hosts unless you pass `allow_insecure_http=True`. Self-hosted Speedle
warns that an unauthenticated plaintext endpoint lets anyone on the network ask
for decisions; prefer HTTPS.

## API

- `Client(...)` and `AsyncClient(...)` take the same keyword arguments. Create
  one per endpoint and share it; it is thread-safe and owns one pooled
  `httpx` client. Use it as a context manager, or call `close()` /
  `aclose()`.
- `is_allowed(subject, action, resource, attributes=None, *, deadline=None, service_name=None)`
  returns the server's `allowed` value.
- `check(...)` returns a `Decision`: `allowed`, `reason` (a `Reason`, or an
  unknown integer as is), `error_message` (self-hosted Speedle evaluator
  errors) and `quota_daily_remaining` (Speedle Cloud).
- `subject` is a user name or a list of `Principal(name, type="user", idd=None)`
  (`PrincipalType` has the Speedle values).
- `attributes` is either a list built with `Attribute.string`,
  `Attribute.numeric`, `Attribute.boolean` (type `bool`) and
  `Attribute.datetime` (each takes one value or a list), or a mapping whose
  types are inferred: `str`, `int`/`float`, `bool` (checked before `int`),
  time-zone-aware `datetime`, or a non-empty list of one of them. A `str` is
  always a string, even if it looks like a date. Inferred attributes are sent
  sorted by name. Invalid values (empty or duplicate names, `None`, NaN,
  infinities, `bool` as a number, naive datetimes, mixed or empty inferred
  lists) raise `InvalidRequestError` before any request. Numbers above 2^53
  lose precision, because self-hosted Speedle decodes numbers as `float64`.
- `service_name=` overrides the client's service for one call. On Speedle
  Cloud a service other than the token's fails with `ForbiddenError` (`code`
  `service_mismatch`); that call is charged.
- `deadline=` replaces the client's deadline (seconds) for one call.
- `AsyncClient` honours task cancellation and `asyncio.timeout()`, also during
  a retry delay; `asyncio.CancelledError` is re-raised in every failure mode.

`Reason.DISCOVER_MODE` (5) returns `allowed=True` but is not a production
grant.

## Retries and deadlines

| `RetryPolicy` field (seconds) | Default |
| --- | --- |
| `max_attempts` (counts the first; `1` disables retries) | 3 |
| `base_delay` | 0.1 |
| `max_delay` | 2.0 |
| `attempt_timeout` | 2.0 |
| `deadline` (the whole call) | 5.0 |

Backoff is exponential with full jitter, raised to `Retry-After` when the
server sends one. Retried: connection errors, attempt timeouts, 502, 503 and
504, and a burst 429 whose `Retry-After` fits the remaining deadline. Never
retried: 400, 401, 403, 404, the daily-quota 429, 500 and other 5xx, 413, 415,
TLS failures and unexpected bodies. Speedle Cloud charges every call that
reaches its handler, including a retried `snapshot_unavailable` 503, so a
call costs at most `max_attempts` units.

In the sync client, `attempt_timeout` bounds each phase of an attempt
(connect, write and each read), because `httpx` has no single total timeout
for blocking requests. The async client bounds the whole attempt.

## Errors

Every error extends `SpeedleError` and carries `status`, `code` (Speedle Cloud
error code), `message` and `quota_daily_remaining`. The types are
`ConfigurationError`, `InvalidRequestError`, `UnauthorizedError`,
`ForbiddenError`, `RequestTooLargeError`, `TooManyRequestsError` (with
`retry_after` in seconds; subtypes `RateLimitedError` and
`QuotaExceededError`), `ServiceUnavailableError`, `ServerError`, `HttpError`,
`UnexpectedResponseError`, `NetworkError` (subtype `speedle.TimeoutError`,
whose subtype is `DeadlineExceededError`) and `CircuitOpenError`.
`speedle.TimeoutError` is the SDK's own class, not the built-in.

## Failure mode

`failure_mode="closed"` makes `is_allowed` return `False` instead of raising
any SDK error, including `CircuitOpenError` and validation errors;
`on_give_up` still reports the error. `check` always raises. Constructor
errors and cancellation are never converted. There is no fail-open mode:
`is_allowed` returns `True` only for a 200 response whose `allowed` is `true`.

## Circuit breaker

Off by default. `circuit_breaker=CircuitBreaker(failure_threshold=5, cool_down=30.0)`
opens after that many consecutive failed calls (network errors, timeouts and
5xx), fails fast with `CircuitOpenError` during the cool-down, then lets one
trial call through.

## Hooks

`hooks=Hooks(on_attempt=..., on_retry=..., on_give_up=..., on_breaker_state_change=...)`
are called synchronously; exceptions and return values are ignored. Payloads
never contain the token, headers, subject, action, resource or attributes.
The SDK sends no telemetry and does no logging; it sends only the
`is-allowed` request, with
`User-Agent: speedle-sdk-python/<version> (python/<version>; <os>)` plus
`user_agent_suffix`. Proxy, netrc and certificate settings from environment
variables are not used.

## Security

- The Speedle Cloud token is a server-side secret. Never ship it to browsers,
  mobile apps or desktop clients.
- `Auth.bearer` requires HTTPS. The token is redacted from `repr`, hook
  payloads and error messages, and `Auth` cannot be pickled.
- Redirects are never followed. TLS verification cannot be disabled.
- Take subjects and attributes from your own authenticated context, never
  from untrusted client input.
- Decisions are never cached.

## Development

```sh
uv sync
uv run ruff check && uv run ruff format --check && uv run mypy
uv run pytest tests --ignore=tests/conformance  # unit tests
uv run pytest tests/conformance                 # every vector in ../conformance/vectors
```

## License

[UPL-1.0](LICENSE). Copyright (c) 2026 The Speedle Authors.
