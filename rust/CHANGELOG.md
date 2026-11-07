# Changelog

## 0.2.0 (unreleased)

- Breaking: `Backend::SpeedlePlus` is now `Backend::Speedle`. There is no
  alias for the old name.

## 0.1.0 (2026-11-05)

- Initial Rust SDK: async `Client` (`is_allowed`, `check` and the `_with`
  variants) for Speedle Cloud and Speedle+ on `tokio` and `reqwest` with
  rustls, typed attributes and `Attribute::infer`, `Auth::none`,
  `Auth::bearer` and `Auth::mutual_tls`, retries with deadline, typed errors,
  fail-closed mode, optional circuit breaker, hooks, and the `blocking`
  feature with `speedle::blocking::Client`.
