# Contributing to Speedle SDK

We welcome your contributions! There are multiple ways to contribute.

## Issues

If you hit a bug or have an enhancement request then file a
[GitHub issue](https://github.com/teramoby/speedle-sdk/issues).

If you report a bug, please include:

1. The SDK language and version (for example `@speedle/sdk` 0.1.0)
2. Issue description
3. Reproducible environment and steps
4. Expected behavior

## Security

Do not report a security issue in a public issue. See [SECURITY.md](SECURITY.md)
for the private reporting channels.

## Code

We welcome code contributions.

Contributions to Speedle are made under the Universal Permissive License 1.0
(UPL-1.0). Please sign off your commits to certify that you have the right to
submit your contribution under the Developer Certificate of Origin (DCO) 1.1.
Retain existing copyright and license notices and identify any third-party
material in your pull request.

Sign off each commit with `git commit -s` (see [DCO](DCO) and
[UPL-1.0 LICENSE](LICENSE)), which adds a
`Signed-off-by: Your Name <you@example.com>` trailer using your Git `user.name`
and `user.email`. To add a sign-off to existing commits, run
`git commit --amend -s` for the most recent commit or
`git rebase --signoff origin/main` for a branch of commits.

The process:

1. Sign off each commit with `git commit -s` (see [DCO](DCO) and
   [UPL-1.0 LICENSE](LICENSE))
2. Fork the repo
3. Fix an issue or create an issue and fix it
4. Create a Pull Request that fixes the issue
5. Watch your PR for pipeline results. If there are failures then fix them.
6. We will review your PR and merge as appropriate.

Read [AGENTS.md](AGENTS.md) before changing an SDK. It records the layout, the
one-PR-per-language rule, and the parts of [docs/design.md](docs/design.md) that
bind every language.

Every SDK runs the shared vectors in `conformance/` against the mock server in
`conformance/mock`, so a behaviour change needs a vector there as well as the
per-language tests.

Run the checks for the language you changed from its directory:

| Language | Commands |
| --- | --- |
| Node.js/TypeScript | `npm ci`, `npm run lint`, `npm test`, `npm run test:package` |
| Python | `uv sync --locked`, `uv run ruff check`, `uv run ruff format --check`, `uv run mypy`, `uv run pytest tests --ignore=tests/conformance` |
| Java | `mvn -B -ntp spotless:check compile javadoc:javadoc`, `mvn -B -ntp test` |
| Go | `gofmt -l .`, `go vet ./...`, `go test -short -race -count=1 ./...` |
| C# | `dotnet build Speedle.Sdk.sln -c Release`, `dotnet test Speedle.Sdk.sln -c Release --no-build --filter "Category!=Conformance"` |
| Rust | `cargo fmt --check`, `cargo clippy --locked --all-features --all-targets -- -D warnings`, `cargo test --locked --all-features` |

Each language's conformance job in `.github/workflows/` shows the exact command
that runs the shared vectors. The Node.js, Python, Go and Rust tests build and
start `conformance/mock` themselves; the Java and C# runs need the mock binary
built first and passed to the test run.

## Code of Conduct

Follow the [Golden Rule](https://en.wikipedia.org/wiki/Golden_Rule). If you'd
like more specific guidelines see the
[Contributor Covenant Code of Conduct](https://www.contributor-covenant.org/version/1/4/code-of-conduct.html).
