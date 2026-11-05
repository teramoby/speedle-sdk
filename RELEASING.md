# Releasing

Every SDK is released only by `.github/workflows/release.yml`, from a tag on a
commit of `main` (design §12.3). Never publish a package by hand.

| SDK | Tag | Registry and package |
| --- | --- | --- |
| Node.js/TypeScript | `node/vX.Y.Z` | npm `@speedle/sdk` |
| Python | `python/vX.Y.Z` | PyPI `speedle` |
| Java | `java/vX.Y.Z` | Maven Central `io.speedle:speedle-sdk` |
| Go | `go/vX.Y.Z` | `github.com/teramoby/speedle-sdk/go` (no registry) |
| C# | `dotnet/vX.Y.Z` | NuGet `Speedle.Sdk` |
| Rust | `rust/vX.Y.Z` | crates.io `speedle` |

Versions are plain `X.Y.Z` (no pre-release suffix). Each SDK has its own
version, starting at `0.1.0`.

## What the workflow does

A pushed tag that matches one of the patterns above starts one run:

1. **verify**: the tag is `<language>/vX.Y.Z`, the tagged commit is an
   ancestor of `main`, and every version source of that SDK (table below)
   equals `X.Y.Z` (`.github/scripts/release-version.py`).
2. **`<language>`**: builds, runs the unit tests and every conformance vector
   against `conformance/mock`, and builds the package. This job has no
   secrets and no OIDC token, and no caches are used.
3. **`publish-<language>`**: runs in the GitHub environment `release`, so it
   waits for a required reviewer. Go has no publish job and no approval: the
   tag push is the Go release (see Go below), and `go-proxy` only confirms it. It publishes the package built in step 2
   (Java and Rust rebuild from the same commit: Maven signs while it deploys,
   and `cargo publish` packages from source with `--no-verify`).

Only the publish job of npm, PyPI, NuGet and crates.io has `id-token: write`.
The workflow has no pull request, `workflow_run` or manual trigger, and runs
only on GitHub-hosted `ubuntu-latest` runners.

## Cutting a release

1. **Release PR** for one SDK, titled for example `chore(node): release 0.2.0`.
   Set the version in every source of that SDK and change the `CHANGELOG.md`
   heading from `## X.Y.Z (unreleased)` to `## X.Y.Z (YYYY-MM-DD)`.

   | SDK | Version sources (all must match the tag) | Helper |
   | --- | --- | --- |
   | node | `node/package.json` `version`; `node/package-lock.json` `version` and `packages[""].version`; `node/src/version.ts` `VERSION` | `npm version X.Y.Z --no-git-tag-version` in `node/` updates the first two |
   | python | `python/pyproject.toml` `project.version`; `python/src/speedle/_version.py` `__version__`; `python/uv.lock` entry `speedle` | `uv lock` in `python/` after editing the first two |
   | java | `java/pom.xml` `<version>` (`version.properties` is filtered from it) | `mvn versions:set -DnewVersion=X.Y.Z -DgenerateBackupPoms=false` |
   | go | `go/speedle.go` `const Version` | |
   | dotnet | `dotnet/src/Speedle.Sdk/Speedle.Sdk.csproj` `<Version>` | |
   | rust | `rust/Cargo.toml` `version`; `rust/Cargo.lock` entry `speedle` | `cargo update -p speedle` in `rust/` |

   To check locally: `python3 .github/scripts/release-version.py node/vX.Y.Z`.
2. **Merge** the PR and wait until that SDK's CI workflow is green on `main`.
3. **Tag** the merge commit and push only that tag:

   ```sh
   git fetch origin
   git tag -a node/v0.2.0 -m "node 0.2.0" <merge-commit-sha>
   git push origin node/v0.2.0
   ```

   Pushing a Go tag publishes the Go module at once and no approval follows
   (see Go below). Before you push a `go/v*` tag, do the Go pre-tag check.
4. **Approve** (not Go) the `publish-<language>` job in the run (Actions → release →
   Review deployments) after checking that:
   - the run is for the tag you pushed and its commit is the merge commit;
   - `verify` printed `... is on main` and an `ok:` line for every version source;
   - the build and test job is green.
5. **Check** the registry page linked from the `release` environment in the run.

If a publish job fails before anything was uploaded (for example, a missing
secret), fix the cause and use "Re-run failed jobs". Never move or reuse a tag.
Registries do not allow a version to be replaced: to fix a bad release, release
the next patch version and deprecate the bad one (`npm deprecate`, yank on
PyPI and crates.io, unlist on NuGet, a `retract` directive in `go/go.mod`).
Maven Central releases cannot be removed.

## Owner setup on GitHub

Repository `teramoby/speedle-sdk`, Settings:

1. **Environment** (Environments → New environment), name **`release`**:
   - Required reviewers: the owner (and any other release approver). With
     more than one approver, enable "Prevent self-review".
   - Clear "Allow administrators to bypass configured protection rules".
   - Deployment branches and tags: "Selected branches and tags", with five
     rules of type **Tag**: `node/v*`, `python/v*`, `java/v*`, `dotnet/v*`,
     `rust/v*` (no Go job uses this environment).
   - Environment secrets and variables from the table below.
2. **Tag ruleset** (Rules → Rulesets → New tag ruleset), name `release tags`,
   enforcement Active, bypass list: Repository admin. Target tags: include
   `node/v*`, `python/v*`, `java/v*`, `go/v*`, `dotnet/v*`, `rust/v*`.
   Rules: Restrict creations, Restrict updates, Restrict deletions. Only
   admins can then create release tags, and nobody can move or delete one.

The approval is the gate that matters: GitHub runs the workflow file of the
tagged commit, so a tag on a commit outside `main` would run that commit's
copy of `release.yml`. The tag ruleset limits who can push such a tag, and the
reviewer checks the `verify` job before approving.

### `release` environment secrets and variables

| Name | Kind | Needed for | Value |
| --- | --- | --- | --- |
| `MAVEN_CENTRAL_USERNAME` | secret | Java | Central Portal user token, username part |
| `MAVEN_CENTRAL_PASSWORD` | secret | Java | Central Portal user token, password part |
| `MAVEN_GPG_KEY` | secret | Java | ASCII-armored secret signing key (see below) |
| `MAVEN_GPG_PASSPHRASE` | secret | Java | Passphrase of that key |
| `NUGET_USER` | variable | C# | nuget.org user name (profile name, not the email address) of the account that owns the trusted publishing policy |
| `NPM_BOOTSTRAP_TOKEN` | secret, first npm release only | Node.js | Short-lived npm granular token (see npm below); delete after the first release |
| `CARGO_BOOTSTRAP_TOKEN` | secret, first crates.io release only | Rust | Short-lived crates.io token (see crates.io below); delete after the first release |

Set them in the environment, not as repository or organization secrets, for
example `gh secret set MAVEN_GPG_PASSPHRASE --env release --repo teramoby/speedle-sdk`
(it prompts for the value) and
`gh variable set NUGET_USER --env release --repo teramoby/speedle-sdk --body <user>`.

## Owner setup per registry

Every trusted publisher uses the same identity:

| Field | Value |
| --- | --- |
| Repository owner | `teramoby` |
| Repository | `speedle-sdk` |
| Workflow file | `release.yml` (file name only, no path) |
| Environment | `release` |

### npm: `@speedle/sdk`

Prerequisite: the npm organization `speedle` on an account with 2FA (design
§12.2). npm can only add a trusted publisher to a package that exists, so the
first release uses a bootstrap token:

1. First release only: on npmjs.com, Access Tokens → Generate New Token →
   Granular Access Token, with permission **Read and write** for packages and
   scopes, limited to the scope `@speedle` (organization `speedle`), and the
   shortest expiry. Store it as the `release` environment secret
   `NPM_BOOTSTRAP_TOKEN`, then cut `node/v0.1.0`. The workflow still publishes
   with `--provenance`.
2. Right after that release: delete the `NPM_BOOTSTRAP_TOKEN` secret and
   revoke the token on npmjs.com.
3. On the day of the next Node.js release (a new configuration expires if it
   has no successful publish within 2 days), open `@speedle/sdk` → Settings →
   Trusted publishing → GitHub Actions and enter:
   - Organization or user: `teramoby`
   - Repository: `speedle-sdk`
   - Workflow filename: `release.yml`
   - Environment name: `release`
   - Allowed actions: allow **`npm publish`** (the workflow does not use
     `npm stage publish`).
4. After that release succeeds: Settings → Publishing access → "Require
   two-factor authentication and disallow tokens".

With trusted publishing, npm authenticates through OIDC and adds provenance.

### PyPI: `speedle`

On pypi.org (account with 2FA): Your account → Publishing → Add a new pending
publisher → GitHub:

- PyPI Project Name: `speedle`
- Owner: `teramoby`
- Repository name: `speedle-sdk`
- Workflow name: `release.yml`
- Environment name: `release`

The first `python/v*` release creates the project and turns the pending
publisher into the project's trusted publisher. A pending publisher does not
reserve the name, so release soon after adding it. The publish step also
uploads PEP 740 attestations.

### Maven Central: `io.speedle:speedle-sdk`

The namespace `io.speedle` is already verified. Maven Central has no trusted
publishing, so the publish job uses four `release` environment secrets.

1. **User token**: on central.sonatype.com, View Account → Generate User Token.
   Store the username as `MAVEN_CENTRAL_USERNAME` and the password as
   `MAVEN_CENTRAL_PASSWORD`.
2. **Signing key**: create it on a trusted machine (GnuPG 2.2 or later):

   ```sh
   # RSA 4096, signing only, expires in 3 years; choose a strong passphrase.
   gpg --quick-generate-key "Speedle Release Signing <ADDRESS>" rsa4096 sign 3y
   gpg --list-secret-keys --with-colons | awk -F: '/^fpr/ {print $10; exit}'
   FPR=<the 40-character fingerprint printed above>

   # Publish the public key; Central looks it up on these key servers.
   gpg --keyserver keyserver.ubuntu.com --send-keys "$FPR"
   gpg --export "$FPR" | curl -T - https://keys.openpgp.org   # then follow the e-mail link
   curl -fsS "https://keyserver.ubuntu.com/pks/lookup?op=get&search=0x$FPR" | head -1

   # Store the secret key and passphrase in the release environment
   # without writing the key to a file.
   gpg --armor --export-secret-keys "$FPR" \
     | gh secret set MAVEN_GPG_KEY --env release --repo teramoby/speedle-sdk
   gh secret set MAVEN_GPG_PASSPHRASE --env release --repo teramoby/speedle-sdk
   ```

   Replace `ADDRESS` with a mailbox the project controls. Keep an offline
   backup of the secret key and of the revocation certificate in
   `~/.gnupg/openpgp-revocs.d/$FPR.rev`. Export only this one key into
   `MAVEN_GPG_KEY`; the signer uses the first key it finds.

The `release` Maven profile in `java/pom.xml` adds the sources and Javadoc
jars, signs every artifact with the Bouncy Castle signer of
`maven-gpg-plugin` (it reads `MAVEN_GPG_KEY` and `MAVEN_GPG_PASSPHRASE`, no
`gpg` binary), and uploads with `central-publishing-maven-plugin` using the
Maven server id `central-portal`. It publishes automatically once Central
validates the deployment and waits until the release is published (up to
30 minutes); the deployment is also visible under Deployments in the Portal.

### NuGet: `Speedle.Sdk`

Prerequisites from design §12.2: a nuget.org account, the organization
`speedle` and, optionally, the reserved ID prefix `Speedle`. On nuget.org:
user name → Trusted Publishing → add a policy for GitHub Actions:

- Policy name: `speedle-sdk release`
- Package owner: the organization `speedle` (the policy applies to all
  packages of that owner)
- Repository Owner: `teramoby`
- Repository: `speedle-sdk`
- Workflow File: `release.yml`
- Environment: `release`

Then set the `release` environment variable `NUGET_USER` to the nuget.org
user name (profile name) of the account that created the policy. NuGet trusted
publishing also works for the first version of a new package, so no API key is
needed. `NuGet/login` exchanges the OIDC token for an API key that is valid
for one hour.

### crates.io: `speedle`

crates.io adds a trusted publisher only to a crate that exists, so the first
release uses a bootstrap token:

1. First release only: crates.io (GitHub login, verified e-mail) → Account
   Settings → API Tokens → New Token, name `speedle-sdk bootstrap`, the
   shortest expiry, scope **publish-new**, crate pattern `speedle`. Store it as
   the `release` environment secret `CARGO_BOOTSTRAP_TOKEN`, then cut
   `rust/v0.1.0`.
2. Right after that release: delete the `CARGO_BOOTSTRAP_TOKEN` secret and
   revoke the token on crates.io. While the secret exists the workflow uses
   it instead of trusted publishing.
3. On crates.io → `speedle` → Settings → Trusted Publishing → Add → GitHub:
   - Repository owner: `teramoby`
   - Repository name: `speedle-sdk`
   - Workflow filename: `release.yml`
   - Environment: `release`

From then on `rust-lang/crates-io-auth-action` exchanges the OIDC token for a
short-lived publish token, which it revokes at the end of the job.

### Go: `github.com/teramoby/speedle-sdk/go`

Nothing to register. Decide the module path before the first `go/v0.1.0` tag
(design §12.1; the repository path is the default). The tag itself is the
release: once it is pushed anyone can fetch the version, and
`proxy.golang.org` and `sum.golang.org` keep it even if the tag is deleted.
No gate can apply to Go: there is no registry upload to hold back, and the
`release` environment can only pause jobs that start after the push, while the
proxy can fetch the tag as soon as it exists. So the workflow has no
`publish-go` job and no approval for Go. The tag push is the publish act, and
the deliberate step is the maintainer's pre-tag check:

1. The `go` CI workflow is green on the exact `main` commit you will tag.
2. `const Version` in `go/speedle.go` on that commit equals `X.Y.Z`
   (`python3 .github/scripts/release-version.py go/vX.Y.Z` prints `ok:`).
3. The module path in `go/go.mod` is final and the commit is on `main`
   (`git merge-base --is-ancestor <sha> origin/main`).

Only then push the tag (only admins can, by the tag ruleset). The run then
repeats `verify` and the `go` build and test job, and `go-proxy` runs
`go mod download` for the version through `proxy.golang.org`, which checks it
against `sum.golang.org` and makes it appear on pkg.go.dev. A red run after
the push cannot undo the release: retract the version in `go/go.mod` and
release the next patch.
