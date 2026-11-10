# Speedle SDK

Client libraries for the Speedle authorization decision API,
`POST /authz-check/v1/is-allowed`. The goal is to let an application ask
"is this subject allowed to perform this action on this resource?" with a few
lines of code.

**Status:** all six SDKs are implemented. The Python, Java, Go, C# and Rust
packages are published at 0.2.0. `@speedle/sdk` 0.2.0 is on npm; the Node.js
package in this tree is 0.2.1.
See the [Speedle SDK documentation](https://engine.speedle.io/docs/sdks/).

## Supported backends

- **Self-hosted Speedle ADS** ([teramoby/speedle-plus](https://github.com/teramoby/speedle-plus)):
  the Authorization Decision Service you run yourself, with no client
  authentication or with mutual TLS.
- **Speedle Cloud (coming soon)** ([cloud.speedle.io](https://cloud.speedle.io)):
  the hosted service. Requests authenticate with a service-scoped bearer token.

## Packages

All six SDKs are built together, with the same API, retry behaviour and
conformance suite. All six packages are published.

| Language | Package | Status |
| --- | --- | --- |
| Node.js/TypeScript | [npm `@speedle/sdk`](https://www.npmjs.com/package/@speedle/sdk) | 0.2.0 (2026-11-10) |
| Python | [PyPI `speedle`](https://pypi.org/project/speedle/) | 0.2.0 (2026-11-07) |
| Java | [Maven Central `io.speedle:speedle-sdk`](https://central.sonatype.com/artifact/io.speedle/speedle-sdk) | 0.2.0 (2026-11-07) |
| Go | [`github.com/teramoby/speedle-sdk/go`](https://pkg.go.dev/github.com/teramoby/speedle-sdk/go) | 0.2.0 (2026-11-07) |
| C# | [NuGet `Speedle.Sdk`](https://www.nuget.org/packages/Speedle.Sdk) | 0.2.0 (2026-11-07) |
| Rust | [crates.io `speedle`](https://crates.io/crates/speedle) | 0.2.0 (2026-11-07) |

## Quickstart: self-hosted Speedle (local ADS)

Start with a local ADS; no token is needed for an unauthenticated loopback
endpoint. Each package README puts its self-hosted example first:

- [Node.js/TypeScript](node/README.md#quickstart-self-hosted-speedle)
- [Python](python/README.md#quickstart-self-hosted-speedle-with-asyncio)
- [Go](go/README.md#quickstart-self-hosted-speedle)
- [Java](java/README.md#quickstart-self-hosted-speedle)
- [C#](dotnet/README.md#quickstart-self-hosted-speedle)
- [Rust](rust/README.md#quickstart-self-hosted-speedle)

## Quickstart: Speedle Cloud (coming soon)

Speedle Cloud sign-in is not open yet. Each package README includes a Cloud
configuration example for use when access is available.

## Security

A Speedle Cloud token is a server-side secret. Never ship it to browsers,
mobile apps or any other client you do not control.

To report a security issue, see [SECURITY.md](SECURITY.md).

## License

Copyright (c) 2026 The Speedle Authors. Licensed under the
[Universal Permissive License (UPL), Version 1.0](LICENSE), the same license
as [self-hosted Speedle](https://github.com/teramoby/speedle-plus).
