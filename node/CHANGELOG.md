# Changelog

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

## 0.2.1 (2026-11-10)

- Releases now publish to npm directly with provenance through trusted publishing. No API or behaviour changes.

## 0.2.0 (2026-11-10)

- Breaking: the self-hosted backend value `"speedle-plus"` is now
  `"speedle"`. There is no alias for the old name.
- First release on npm.

## 0.1.0 (2026-11-05)

- First version: `SpeedleClient` with `isAllowed` and `check` for Speedle
  Cloud and self-hosted Speedle, typed and inferred attributes, pluggable auth,
  retries with deadline and cancellation, fail-closed mode, circuit breaker and
  hooks.
