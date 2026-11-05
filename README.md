# Speedle SDK

Client libraries for the Speedle authorization decision API,
`POST /authz-check/v1/is-allowed`. The goal is to let an application ask
"is this subject allowed to perform this action on this resource?" with a few
lines of code.

**Status:** all six SDKs are implemented at version 0.1.0.

## Supported backends

- **Speedle Cloud** ([cloud.speedle.io](https://cloud.speedle.io)): the hosted
  service. Requests authenticate with a service-scoped bearer token.
- **Speedle+ self-hosted ADS** ([teramoby/speedle-plus](https://github.com/teramoby/speedle-plus)):
  the Authorization Decision Service you run yourself, with no client
  authentication or with mutual TLS.

## Packages

All six SDKs are built together, with the same API, retry behaviour and
conformance suite. Five packages are published; the npm release is pending.

| Language | Package | Status |
| --- | --- | --- |
| Node.js/TypeScript | [npm `@speedle/sdk`](https://www.npmjs.com/package/@speedle/sdk) | Not yet published (0.1.0 pending) |
| Python | [PyPI `speedle`](https://pypi.org/project/speedle/) | 0.1.0 (2026-11-05) |
| Java | [Maven Central `io.speedle:speedle-sdk`](https://central.sonatype.com/artifact/io.speedle/speedle-sdk) | 0.1.0 (2026-11-05) |
| Go | [`github.com/teramoby/speedle-sdk/go`](https://pkg.go.dev/github.com/teramoby/speedle-sdk/go) | 0.1.0 (2026-11-05) |
| C# | [NuGet `Speedle.Sdk`](https://www.nuget.org/packages/Speedle.Sdk) | 0.1.0 (2026-11-05) |
| Rust | [crates.io `speedle`](https://crates.io/crates/speedle) | 0.1.0 (2026-11-05) |

## Security

A Speedle Cloud token is a server-side secret. Never ship it to browsers,
mobile apps or any other client you do not control.

## License

Copyright (c) 2026 The Speedle Authors. Licensed under the
[Universal Permissive License (UPL), Version 1.0](LICENSE), the same license
as [Speedle+](https://github.com/teramoby/speedle-plus).
