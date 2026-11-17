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

## 0.2.0 (2026-11-07)

- Breaking: `Backend::SpeedlePlus` is now `Backend::Speedle`. There is no
  alias for the old name.

## 0.1.0 (2026-11-05)

- Initial Rust SDK: async `Client` (`is_allowed`, `check` and the `_with`
  variants) for Speedle Cloud and Speedle+ on `tokio` and `reqwest` with
  rustls, typed attributes and `Attribute::infer`, `Auth::none`,
  `Auth::bearer` and `Auth::mutual_tls`, retries with deadline, typed errors,
  fail-closed mode, optional circuit breaker, hooks, and the `blocking`
  feature with `speedle::blocking::Client`.
