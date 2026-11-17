# Speedle SDK

Client libraries for the Speedle authorization decision API,
`POST /authz-check/v1/is-allowed`. The goal is to let an application ask
"is this subject allowed to perform this action on this resource?" with a few
lines of code.

**Status:** all six SDKs are implemented. This tree prepares their stable
1.9.0 releases; packages are published by a repository admin after merge via
the [release workflow](RELEASING.md).
See the [Speedle SDK documentation](https://engine.speedle.io/docs/sdks/).

## Supported backends

- **Self-hosted Speedle ADS** ([teramoby/speedle-plus](https://github.com/teramoby/speedle-plus)):
  the Authorization Decision Service you run yourself, with no client
  authentication or with mutual TLS.
- **Speedle Cloud (coming soon)** ([cloud.speedle.io](https://cloud.speedle.io)):
  the hosted service. Requests authenticate with an account-level API key, sent as a bearer credential.

## Packages

All six SDKs are built together, with the same API, retry behaviour and
conformance suite. The versions below are prepared in this tree and are not
yet released.

| Language | Package | Version in this tree |
| --- | --- | --- |
| Node.js/TypeScript | [npm `@speedle/sdk`](https://www.npmjs.com/package/@speedle/sdk) | 1.9.0 (unreleased) |
| Python | [PyPI `speedle`](https://pypi.org/project/speedle/) | 1.9.0 (unreleased) |
| Java | [Maven Central `io.speedle:speedle-sdk`](https://central.sonatype.com/artifact/io.speedle/speedle-sdk) | 1.9.0 (unreleased) |
| Go | [`github.com/teramoby/speedle-sdk/go`](https://pkg.go.dev/github.com/teramoby/speedle-sdk/go) | 1.9.0 (unreleased) |
| C# | [NuGet `Speedle.Sdk`](https://www.nuget.org/packages/Speedle.Sdk) | 1.9.0 (unreleased) |
| Rust | [crates.io `speedle`](https://crates.io/crates/speedle) | 1.9.0 (unreleased) |

## Compatibility

| SDK version (all six languages) | Self-hosted Speedle Engine | Speedle Cloud API |
| --- | --- | --- |
| 1.x | >= 1.6 | v1 |

SDK major and minor versions are aligned with the Engine; patch numbers are
independent per package. All six SDKs release `X.Y.0` together with Engine
`X.Y.0`, even without an SDK API change, and each SDK releases its own patch
versions within that `X.Y` line. Version alignment is not a compatibility
claim: the decision API used by the SDKs is unchanged through Engine v1.8.0,
and SDK 1.9.0 is validated through that version. See the
[compatibility test evidence](conformance/README.md#engine-compatibility)
for the lower bound and the scope of the tests.

## Quickstart: self-hosted Speedle (local ADS)

Use [Engine v1.8.0](https://github.com/teramoby/speedle-plus/releases/tag/v1.8.0)
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
