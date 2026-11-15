# Repository guidance

speedle-sdk holds the official client SDKs for Speedle Cloud and self-hosted
Speedle.

## Spec

- `docs/design.md` is the spec. Behaviour that differs from it is a bug in the
  code or needs a design change first.
- The retry and failure rules (§6), typed attributes (§3.2.1) and the error
  model (§3.4, §7) are binding for every language.

## Layout and PRs

- One directory per language (`node/`, `python/`, `java/`, `go/`, `dotnet/`,
  `rust/`), and one PR per language. Do not mix languages in a PR. A
  coordinated release bump across SDKs may be one PR when it changes only
  version sources, lockfiles, changelogs and READMEs as follows: version-only
  substitutions of the SDK's own version in version sources, install snippets
  and examples; each SDK's own version entries in lockfiles, never dependency
  entries; changelog or README text about versions, release dates or status
  notes; and README links to the compatibility documentation. Reviewers reject
  any other change in such a PR, including any change to API documentation,
  dependency declarations or versions, integrity data, scripts, plugins, build
  configuration, license or package metadata, package names, registry URLs,
  install commands, credential setup or other setup instructions, any change
  to compatibility or security claims, even where design §12.3 or §10 states
  the current text (those need a design change with conformance evidence), and
  any weakening or removal of security guidance, such as the design §10
  API-key warning. A repository maintainer approves such a PR only after
  checking every change against this list.
- Every SDK must pass the shared vectors in `conformance/` against the mock
  server in `conformance/mock`, in its CI workflow.

## License

- UPL-1.0, copyright line `Copyright (c) 2026 The Speedle Authors`. Every
  package declares SPDX `UPL-1.0` and ships the license file.
- `.github/codex/` keeps its Apache 2.0 license and notice for reused ReviewBot
  code.

## Writing

- English for code, comments, docs, commits, PRs and reviews.
- Conventional Commits for commit messages and PR titles, for example
  `feat(python): add async client`.

## Releases

- Never publish packages by hand. Releases go only through the release
  workflow (design §12.3).
