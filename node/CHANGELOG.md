# Changelog

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
