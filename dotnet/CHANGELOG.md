# Changelog

## 0.2.0 (2026-11-07)

- Breaking: `Backend.SpeedlePlus` is now `Backend.Speedle`. There is no
  alias for the old name.

## 0.1.0 (2026-11-05)

- First version: `SpeedleClient` for `POST /authz-check/v1/is-allowed` on
  Speedle Cloud and Speedle+, typed and inferred attributes, pluggable auth
  (none, bearer, mutual TLS), retries with deadline and cancellation, typed
  exceptions, fail-closed mode, circuit breaker and hooks.
