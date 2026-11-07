# Changelog

## 0.2.0 (unreleased)

- Breaking: `BackendSpeedlePlus` (`"speedle-plus"`) is now `BackendSpeedle`
  (`"speedle"`). There is no alias for the old name.

## 0.1.0 (2026-11-05)

- Initial Go SDK: `IsAllowed` and `Check` for Speedle Cloud and Speedle+,
  typed attributes and `Infer`, pluggable auth (`NoAuth`, `BearerToken`,
  `MutualTLS`), retries with deadline and cancellation, typed errors,
  fail-closed mode, optional circuit breaker and hooks.
