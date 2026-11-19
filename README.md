# Speedle SDK

Client libraries for the Speedle authorization decision API,
`POST /authz-check/v1/is-allowed`. The goal is to let an application ask
"is this subject allowed to perform this action on this resource?" with a few
lines of code.

**Status:** all six SDKs are implemented, on the Engine 1.9 line; each SDK's
release PR sets that SDK's version sources, and packages are published by a
repository admin after merge via the
[release workflow](RELEASING.md).
See the [Speedle SDK documentation](https://engine.speedle.io/docs/sdks/).

## Supported backends

- **Self-hosted Speedle ADS** ([teramoby/speedle-plus](https://github.com/teramoby/speedle-plus)):
  the Authorization Decision Service you run yourself, with no client
  authentication or with mutual TLS.
- **Speedle Cloud (coming soon)** ([cloud.speedle.io](https://cloud.speedle.io)):
  the hosted service. Requests authenticate with an account-level API key, sent as a bearer credential.

## Packages

All six SDKs are built together, with the same API, retry behaviour and
conformance suite. The versions below are the next release of each SDK; each
SDK's release PR sets its version sources, and nothing is released until the
release workflow runs.

| Language | Package | Next release |
| --- | --- | --- |
| Node.js/TypeScript | [npm `@speedle/sdk`](https://www.npmjs.com/package/@speedle/sdk) | 1.9.0 (unreleased) |
| Python | [PyPI `speedle`](https://pypi.org/project/speedle/) | 1.9.0 (unreleased) |
| Java | [Maven Central `io.speedle:speedle-sdk`](https://central.sonatype.com/artifact/io.speedle/speedle-sdk) | 1.9.0 (unreleased) |
| Go | [`github.com/teramoby/speedle-sdk/go`](https://pkg.go.dev/github.com/teramoby/speedle-sdk/go) | 1.9.1 (unreleased) |
| C# | [NuGet `Speedle.Sdk`](https://www.nuget.org/packages/Speedle.Sdk) | 1.9.0 (unreleased) |
| Rust | [crates.io `speedle`](https://crates.io/crates/speedle) | 1.9.0 (unreleased) |

## Runtime support

Each SDK supports every language or runtime version released in the past
three years; older versions may also work. A declared minimum is never raised
above the oldest release in that window just because the repository builds
with a newer toolchain, and CI builds and tests the declared minimum and the
latest release. Where a dependency would force a newer minimum, the dependency
changes instead of the minimum.

The table uses the three-year window ending 2026-11-19:

| SDK | Oldest in-window release | Declared minimum | CI test versions |
| --- | --- | --- | --- |
| Go | [Go 1.22](https://go.dev/doc/devel/release#go1.22.0) (2024-02-06) | `go 1.22` | 1.22, 1.27 |
| Python | [Python 3.13](https://www.python.org/downloads/release/python-3130/) (2024-10-07) | `requires-python = ">=3.10"` | 3.10, 3.11, 3.12, 3.13, 3.14, 3.15 |
| Node.js | [Node.js 22](https://nodejs.org/en/blog/announcements/v22-release-announce) (2024-04-24) | `engines.node >= 22` | 22, 24, 26 |
| Java | [JDK 22](https://openjdk.org/projects/jdk/22/) (2024-03-19) | `maven.compiler.release 17` | 17, 21, 25, 27 |
| .NET | [.NET 9](https://github.com/dotnet/core/blob/main/release-notes/9.0/9.0.0/9.0.0.md) (2024-11-12) | `net8.0` | net8.0, net10.0, net11.0 |
| Rust | [Rust 1.75](https://github.com/rust-lang/rust/blob/master/RELEASES.md#version-1750-2023-12-28) (2023-12-28) | `rust-version = "1.75"` | 1.75, stable |

Release dates come from the official release histories:
[Go](https://go.dev/doc/devel/release),
[Python](https://www.python.org/downloads/source/),
[Node.js](https://nodejs.org/dist/index.json),
[OpenJDK](https://openjdk.org/projects/jdk/),
[.NET](https://dotnet.microsoft.com/en-us/platform/support/policy/dotnet-core)
and [Rust](https://github.com/rust-lang/rust/blob/master/RELEASES.md).

## Compatibility

| SDK version (all six languages) | Self-hosted Speedle Engine | Speedle Cloud API |
| --- | --- | --- |
| 1.x | >= 1.6 | v1 |

SDK major and minor versions are aligned with the Engine; patch numbers are
independent per package. All six SDKs release `X.Y.0` together with Engine
`X.Y.0`, even without an SDK API change, and each SDK releases its own patch
versions within that `X.Y` line. Version alignment is not a compatibility
claim: SDK 1.9.0 keeps the 1.x support range, self-hosted Engine >= 1.6 and
Speedle Cloud API v1. The recorded real-Engine compatibility runs cover
Engine v1.6.0 and v1.8.0; Engine v1.9.0 is the installation reference for new
deployments. See the
[compatibility test evidence](conformance/README.md#engine-compatibility)
for the lower bound and the scope of the tests.

## Quickstart: self-hosted Speedle (local ADS)

Use [Engine v1.9.0](https://github.com/teramoby/speedle-plus/releases/tag/v1.9.0)
for a new self-hosted installation. Start with a local ADS; no token is
needed for an unauthenticated loopback endpoint. Each package README puts
its self-hosted example first:

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

A Speedle Cloud API key is a server-side secret. Never ship it to browsers,
mobile apps or any other client you do not control.

To report a security issue, see [SECURITY.md](SECURITY.md).

## License

Copyright (c) 2026 The Speedle Authors. Licensed under the
[Universal Permissive License (UPL), Version 1.0](LICENSE), the same license
as [self-hosted Speedle](https://github.com/teramoby/speedle-plus).
