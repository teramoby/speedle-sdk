# Changelog

## 1.9.1 (2026-11-19)

- The module now declares `go 1.22`, down from `go 1.26` in the released
  1.9.0 module, so the SDK supports every Go release from the last three
  years. CI builds and tests Go 1.22 and the latest release.

## 1.9.0 (2026-11-17)

- Version aligned with Speedle Engine 1.9.0; earlier published releases were
  0.x.
- From this release, SDK major and minor versions are aligned with the
  Engine, and patch numbers are independent per package. This supersedes the
  independent-versioning note in the 1.0.0 entry.

## 1.0.0 (2026-11-15)

- Mark the existing public API stable; no API or decision behaviour changes.
- Document compatibility with self-hosted Engine >= 1.6 and Speedle Cloud
  API v1. SDK, Engine and Cloud versions are independent.

## 0.2.0 (2026-11-07)

- Breaking: `BackendSpeedlePlus` (`"speedle-plus"`) is now `BackendSpeedle`
  (`"speedle"`). There is no alias for the old name.

## 0.1.0 (2026-11-05)

- Initial Go SDK: `IsAllowed` and `Check` for Speedle Cloud and Speedle+,
  typed attributes and `Infer`, pluggable auth (`NoAuth`, `BearerToken`,
  `MutualTLS`), retries with deadline and cancellation, typed errors,
  fail-closed mode, optional circuit breaker and hooks.
