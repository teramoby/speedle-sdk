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

A pushed non-Go tag that matches one of the patterns above starts one run. Go
instead starts from a manual workflow dispatch with a version input; only a
repository admin may dispatch it, and maintainers never push its final `go/v*`
tag.

Releases require no GitHub environment approval (owner decision, 2026-11-05).
Only repository admins may start one: the tag rulesets restrict non-Go
release-tag creation to admins, and the Go dispatch check below enforces the
same boundary. npm is the exception to a fully unattended publish: the
workflow stages each npm version, and a maintainer approves it with 2FA on
npmjs.com afterwards (see npm below).

1. **preflight**: reads the `release` environment with the workflow token's
   `actions: read` permission and fails if it is missing or unreadable. For a
   Go dispatch, it also uses the token's implicit `metadata: read` to require
   `admin` repository permission for `github.triggering_actor`.
2. **verify**: the requested tag is `<language>/vX.Y.Z`, the selected commit is
   an ancestor of `main`, and every version source of that SDK (table below)
   equals `X.Y.Z` (`.github/scripts/release-version.py`).
3. **`<language>`**: builds, runs the unit tests and every conformance vector
   against `conformance/mock`, and builds the package. This job has no
   secrets and no OIDC token, and no caches are used.
4. **`publish-<language>`**: runs in the GitHub environment `release` without
   waiting for approval. For five SDKs it publishes the package built in step 3
   (Java and Rust rebuild from the same commit: Maven signs while it deploys,
   and `cargo publish` packages from source with `--no-verify`). npm uses
   `npm stage publish`, so the version waits in the staged queue until a
   maintainer approves it with 2FA on npmjs.com.
   **`publish-go`** creates the `go/vX.Y.Z` tag at the verified commit only
   after the build and tests pass; `go-proxy` then confirms it on
   `proxy.golang.org`.

Only the publish job of npm, PyPI, NuGet and crates.io has `id-token: write`.
The workflow has no pull request or `workflow_run` trigger, and runs only on
GitHub-hosted `ubuntu-latest` runners. Its only manual trigger is the Go release
request; the other five SDKs remain tag-driven.

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
3. **Start the run**:
   - For Node.js, Python, Java, C# or Rust, tag the merge commit and push only
     that tag:

     ```sh
     git fetch origin
     git tag -a node/v0.2.0 -m "node 0.2.0" <merge-commit-sha>
     git push origin node/v0.2.0
     ```

   - For Go, **do not push `go/vX.Y.Z`**. Dispatch the release workflow on
     `main` with version `X.Y.Z` (Actions → release → Run workflow), or run:

     ```sh
     gh workflow run release.yml --repo teramoby/speedle-sdk --ref main -f version=X.Y.Z
     ```

4. **Watch** the run (Actions → release). The publish job starts automatically
   after the checks pass. Confirm that:
   - the run is for the requested tag or Go version and the expected `main`
     commit;
   - `preflight` passed, `verify` printed `... is on main` and an `ok:` line for
     every version source, and the build and test job is green.
5. **Check** the registry or pkg.go.dev page linked from the `release`
   environment in the run. For npm the version is staged, not published yet:
   approve it with 2FA as described under npm below, then verify the published
   version and provenance (npm, step 3). The publish job summary repeats these
   approval steps.

If a publish job fails before anything was uploaded (for example, a missing
secret), fix the cause and use "Re-run failed jobs". If the npm stage publish
succeeded, approve the staged version instead of re-running. If npm created
`@speedle/sdk` with the `0.0.0-stage` placeholder but the version was not
staged, the package now exists and the stage-token guard blocks a retry with
`NPM_STAGE_TOKEN`: configure the trusted publisher (npm below, step 4),
delete the `NPM_STAGE_TOKEN` secret (step 5), and re-run the failed jobs. If Go
fails before `publish-go`, no module tag exists; fix the cause and dispatch the
same version again. Never move or reuse a tag that was created. Registries do
not allow a version to be replaced: to fix a bad release, release the next
patch version and deprecate the bad one (`npm deprecate`, yank on PyPI and
crates.io, unlist on NuGet, a `retract` directive in `go/go.mod`). Maven
Central releases cannot be removed.

## Owner setup on GitHub

Repository `teramoby/speedle-sdk`, Settings:

1. **Environment** (Environments → New environment), name **`release`**:
   - Do not configure required reviewers. The owner decided on 2026-11-05 that
     releases require no GitHub environment approval. The npm 2FA approval
     happens on npmjs.com, not here.
   - Clear "Allow administrators to bypass configured protection rules".
   - Deployment branches and tags: "Selected branches and tags", with a
     **Branch** rule for `main` (Go dispatches) and five rules of type **Tag**:
     `node/v*`, `python/v*`, `java/v*`, `dotnet/v*`, `rust/v*`.
   - Environment secrets and variables from the table below.
2. **Tag rulesets** (Rules → Rulesets → New tag ruleset), enforcement Active,
   bypass list Repository admin:
   - Name `release tags`: target `node/v*`, `python/v*`, `java/v*`, `dotnet/v*`
     and `rust/v*`; enable Restrict creations, Restrict updates and Restrict
     deletions.
   - Name `Go release tags`: target `go/v*`; enable Restrict updates and
     Restrict deletions, but not Restrict creations. The protected
     `publish-go` job's `GITHUB_TOKEN` must be able to create the tag after
     preflight, verification and tests. Direct `go/v*` pushes are not a release
     path, do not trigger this workflow, and must not be used.

The workflow's `preflight` job queries
`GET /repos/teramoby/speedle-sdk/environments/release` with `actions: read` and
fails before verification if the environment is absent or unreadable. For Go,
it also queries
`GET /repos/teramoby/speedle-sdk/collaborators/{user}/permission` using the
token's implicit `metadata: read`, and rejects the dispatch unless the
triggering user has `admin` permission. It does not prove the deployment-ref or
tag-ruleset settings, so the owner must complete the setup above before the
first release.

Repository admins are the gate that matters. For non-Go SDKs, GitHub runs the
workflow file of the tagged commit, and the tag ruleset limits who can push such
a tag. For Go, dispatch selects `main`, preflight requires an admin dispatcher,
and the tag does not exist until the same run has passed `preflight`, `verify`,
and the Go build and tests. No reviewer approval follows in either case.

### `release` environment secrets and variables

| Name | Kind | Needed for | Value |
| --- | --- | --- | --- |
| `MAVEN_CENTRAL_USERNAME` | secret | Java | Central Portal user token, username part |
| `MAVEN_CENTRAL_PASSWORD` | secret | Java | Central Portal user token, password part |
| `MAVEN_GPG_KEY` | secret | Java | ASCII-armored secret signing key (see below) |
| `MAVEN_GPG_PASSPHRASE` | secret | Java | Passphrase of that key |
| `NUGET_USER` | variable | C# | nuget.org user name (profile name, not the email address) of the account that owns the trusted publishing policy |
| `NPM_STAGE_TOKEN` | secret, first npm release only | Node.js | Short-lived npm stage-only granular token (see npm below); delete after the first release is approved |
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
§12.2). npm adds a trusted publisher only to a package that exists, so the
first release uses a stage-only granular token. `npm stage publish` may create
a brand-new package: the registry publishes a public placeholder version
`0.0.0-stage`, and the real version waits in the staged queue until a
maintainer approves it with 2FA. Staging itself does not need 2FA, so CI can
run it with a stage-only token that has no Bypass 2FA.

1. **Create the stage-only token**, once, before the first Node.js release.
   On npmjs.com: Access Tokens → Generate New Token → Granular Access Token:
   - Packages: the **`@speedle` scope**. It is the least privilege that covers
     `@speedle/sdk`, and it cannot stage packages outside the scope.
   - Permission: **Read and write (stage only)**.
   - Leave **Bypass two-factor authentication** unchecked.
   - Shortest expiry.

   Create a new token for this purpose; do not reuse or rename an older token,
   in particular not the removed `NPM_BOOTSTRAP_TOKEN` (the workflow fails if
   that secret is still set, see step 5).

   Use **All Packages** only as a fallback, and only if npm will not let the
   scope-scoped token create a brand-new package: `@speedle/sdk` does not
   exist yet, and npm's package selector matches existing packages, so on some
   accounts only All Packages can stage the version that creates the package.
   That token can stage any package the account can reach, so it is a
   fallback rather than the default; keep it equally short-lived and delete it
   right after the first approval (step 5). If it is needed, audit before
   approving: list the staged versions of every package the account can reach
   (`npm stage list <package>`), so nothing outside `@speedle/sdk` was staged.
   The 2FA approval stays the barrier either way, because a stage-only token
   can never approve.

   Store it as the `release` environment secret `NPM_STAGE_TOKEN`:

   ```sh
   gh secret set NPM_STAGE_TOKEN --env release --repo teramoby/speedle-sdk
   ```

   Then cut `node/v0.1.0`. The workflow runs
   `npm stage publish --provenance --access public`.
2. **Approve the staged version with 2FA.** The publish job summary links to
   this. A maintainer opens `@speedle/sdk` on npmjs.com → **Staged Packages**
   → approve the version, or runs `npm stage approve <stage-id>`
   (`npm stage list @speedle/sdk` shows the id) and completes 2FA. Until then
   the package page shows the placeholder `0.0.0-stage`, `npm install
   @speedle/sdk` may fail with `ETARGET`, and the package must not be
   announced.
3. **Verify the published version.** The approval publishes the version the
   workflow staged; a green workflow run alone does not. Confirm it on the
   registry:

   ```sh
   npm view @speedle/sdk@X.Y.Z version dist.integrity dist.attestations
   ```

   Check that the version is `X.Y.Z`, the package page shows the provenance
   badge, and the attestation points at the release workflow run for the tag.
   Only after this is the release complete. If the version or provenance does
   not match, deprecate it and release the next patch; a published npm version
   cannot be replaced.
4. **Configure trusted publishing right after that first approval**, within
   48 hours of the first successful publish. Open `@speedle/sdk` → Settings →
   Trusted publishing → GitHub Actions and enter:
   - Organization or user: `teramoby`
   - Repository: `speedle-sdk`
   - Workflow filename: `release.yml`
   - Environment name: `release`
   - Allowed actions: allow **`npm stage publish`** (the workflow stages every
     release; direct `npm publish` stays disabled).

   Or run:

   ```sh
   npm trust github @speedle/sdk --file release.yml \
     --repo teramoby/speedle-sdk --env release --allow-stage-publish
   ```

   A new trusted-publisher configuration expires if no publish succeeds within
   48 hours of its creation. If the next Node.js release is further away than
   that, create the configuration again just before it.
5. **Delete the stage-only token.** Delete the `NPM_STAGE_TOKEN` secret and
   revoke the token on npmjs.com. If the `release` environment still has the
   old `NPM_BOOTSTRAP_TOKEN` secret (the bypass-2FA path this design replaces),
   delete that secret and revoke its token as well: the workflow no longer
   reads it, so a leftover would otherwise stay valid without being used. The
   workflow enforces the stage-token cleanup: when `NPM_STAGE_TOKEN` is set,
   the publish job first checks `https://registry.npmjs.org/@speedle%2fsdk`
   (read-only, no token) and fails with "delete the ... secret" if the package
   already exists, or if the check gets any answer other than 404. A lingering
   stage token therefore blocks the release instead of silently replacing
   trusted publishing.
6. **Later releases** use trusted publishing (OIDC) and also stage the
   release, so a maintainer approves every version with 2FA as in steps 2
   and 3. After such a release succeeds: Settings → Publishing access →
   "Require two-factor authentication and disallow tokens".

With trusted publishing, npm authenticates through OIDC and adds provenance.

**Switching to direct `npm publish`.** If the maintainers later decide that no
human approval is needed for each npm version, allow **`npm publish`** on the
trusted publisher (the `--allow-publish` flag of `npm trust`; it can be enabled
together with stage publishing) and change both `npm stage publish` steps in
`.github/workflows/release.yml` to `npm publish`. The `NPM_STAGE_TOKEN` path
only exists for the first release, so nothing else changes.

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
   revoke the token on crates.io. The workflow enforces this: when
   `CARGO_BOOTSTRAP_TOKEN` is set, the publish job first checks
   `https://crates.io/api/v1/crates/speedle` (read-only, no token) and fails
   with "delete the bootstrap secret" if the crate already exists, or if the
   check gets any answer other than 404. A lingering token therefore blocks the
   release instead of silently replacing trusted publishing.
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
release: once it exists anyone can fetch the version, and `proxy.golang.org`
and `sum.golang.org` keep it even if the tag is deleted. Therefore the
workflow, not the maintainer, creates the tag:

1. Merge the Go release PR and dispatch `.github/workflows/release.yml` on
   `main` with `version: X.Y.Z`. Never push `go/vX.Y.Z` directly.
2. `preflight` checks the protected environment; `verify` checks that the
   selected commit is on `main`, `const Version` in `go/speedle.go` equals
   `X.Y.Z`, and the request is well formed. The `go` job then builds and runs
   all unit and conformance tests without credentials.
3. After those checks pass, `publish-go` runs without further approval in the
   `release` environment and uses `contents: write` to create the lightweight
   `go/vX.Y.Z` tag at the exact verified `GITHUB_SHA`. `go-proxy` subsequently
   runs `go mod download` through `proxy.golang.org`, which checks the version
   against `sum.golang.org` and makes it appear on pkg.go.dev.

A failure before `publish-go` publishes nothing and the same version can be
dispatched again after fixing the cause. Once the tag exists, a later failure
cannot undo the release: retract the version in `go/go.mod` and release the
next patch.
