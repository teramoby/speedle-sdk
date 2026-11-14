# Changelog

## 1.0.0 (unreleased)

- Mark the existing public API stable; no API or decision behaviour changes.
- Document compatibility with self-hosted Engine >= 1.6 and Speedle Cloud
  API v1. SDK, Engine and Cloud versions are independent.

## 0.2.0 (2026-11-07)

- Breaking: `Backend.SPEEDLE_PLUS` is now `Backend.SPEEDLE`. There is no
  alias for the old name.

## 0.1.0 (2026-11-05)

- First version: `SpeedleClient` for `POST /authz-check/v1/is-allowed` on
  Speedle Cloud and Speedle+, typed and inferred attributes, pluggable auth
  (none, bearer, mutual TLS), retries with deadline and cancellation, typed
  errors, fail-closed mode, circuit breaker and hooks.
