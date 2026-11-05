# Speedle SDK

Client libraries for the Speedle authorization decision API,
`POST /authz-check/v1/is-allowed`. The goal is to let an application ask
"is this subject allowed to perform this action on this resource?" with a few
lines of code.

**Status: design phase.** There is no implementation and nothing is published
yet. The design is tracked in
[teramoby/cloud.speedle.io#212](https://github.com/teramoby/cloud.speedle.io/issues/212).

## Supported backends

- **Speedle Cloud** ([cloud.speedle.io](https://cloud.speedle.io)): the hosted
  service. Requests authenticate with a service-scoped bearer token.
- **Speedle+ self-hosted ADS** ([teramoby/speedle-plus](https://github.com/teramoby/speedle-plus)):
  the Authorization Decision Service you run yourself, with no client
  authentication or with mutual TLS.

## Planned languages

- Batch 1: Node.js/TypeScript, Python, Java, Go.
- Batch 2 (planned): C#, Rust.

Package names and registries are not decided yet.

## Security

A Speedle Cloud token is a server-side secret. Never ship it to browsers,
mobile apps or any other client you do not control.

## License

[Apache License 2.0](LICENSE).
