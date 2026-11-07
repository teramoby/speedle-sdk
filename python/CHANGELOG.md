# Changelog

## 0.2.0 (2026-11-07)

- Breaking: the self-hosted backend value `"speedle-plus"` is now
  `"speedle"`. There is no alias for the old name.

## 0.1.0 (2026-11-05)

- First version: `Client` and `AsyncClient` with `is_allowed` and `check` for
  Speedle Cloud and Speedle+, typed and inferred attributes, pluggable auth,
  retries with deadline and cancellation, fail-closed mode, circuit breaker
  and hooks.
