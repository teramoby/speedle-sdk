# SDK end-to-end tests

These tests run every SDK against a real authorization decision service with
a known policy set, and check each decision. They complement the conformance
suite in `conformance/`, which tests each SDK against a scripted mock.

Speedle Cloud must answer exactly like Speedle+ (owner rule,
[cloud.speedle.io#213](https://github.com/teramoby/cloud.speedle.io/issues/213)).
So the expected decisions are recorded from a local Speedle+ REST ADS loaded
with the same policies, and the gamma run checks that Speedle Cloud gives the
same answers through each SDK.

Tracking issue: [teramoby/cloud.speedle.io#212](https://github.com/teramoby/cloud.speedle.io/issues/212).

## Files

| Path | What it is |
| --- | --- |
| `policies.json` | The policy set, in Speedle Cloud's JSON export format, which is also the console's JSON import file ([cloud.speedle.io#191](https://github.com/teramoby/cloud.speedle.io/pull/191)). |
| `vectors.json` | The requests and the decision Speedle+ returned for each. |
| `speedle-plus/` | The local proof: `run-local.sh` and a small Go tool (standard library only) that loads the policies with `spctl`, checks the parsed forms and checks or records the vectors with raw HTTP. |
| `java/`, `node/`, `python/`, `go/`, `dotnet/`, `rust/` | One thin runner per SDK, each with `build.sh` and `run.sh`. Each builds the SDK from its directory in this repository. |
| `../.github/workflows/e2e-local.yml` | The local proof for every SDK, on pull requests and pushes that touch `e2e/` or an SDK. No secrets. |
| `../.github/workflows/e2e-gamma.yml` | The gamma workflow. |

## Policy set

Service `sdk-e2e`, 14 authorization policies and 5 role policies:

| Policy | Kind | What it covers |
| --- | --- | --- |
| `writers-are-editors`, `bob-is-editor` | role | Role from a group and from a user. |
| `ivan-is-never-editor` | role | A deny role policy overrides a role grant. |
| `carol-owns-apollo` | role | A role scoped to one resource. |
| `olga-audits-on-duty` | role | A role policy with a `bool` condition. |
| `public-docs-read` | authz | A plain user grant with two actions. |
| `editors-edit-drafts`, `auditors-read-logs` | authz | Grants to a role. |
| `locked-drafts-are-read-only` | authz | A deny with a `bool` condition overrides a role grant. |
| `owners-delete-projects` | authz | A resource expression (`expr:`). |
| `finance-staff-read-reports` | authz | A group grant with a `string` condition. |
| `henry-never-reads-reports` | authz | A user deny overrides a group grant. |
| `vault-needs-clearance` | authz | A `numeric` condition. |
| `archive-tickets-2026` | authz | A `datetime` range. |
| `restore-from-november-snapshot` | authz | A `datetime` array (`in`). |
| `primary-colour-labels` | authz | A `string` array (`IsSubSet`). |
| `top-score-ranks` | authz | A `numeric` array (`in`). |
| `unanimous-reviews` | authz | A `bool` array (`!(false in approvals)`). |
| `small-expenses` | authz | `numeric`, `bool` and `string` attributes in one condition. |

The policies use only built-in functions, no custom functions (Speedle Cloud
rejects those) and no time-dependent built-in attributes, so the decisions do
not change over time. The file passes Cloud's own `ServiceImportFileSchema`
(`packages/shared/src/policy-import.ts` at cloud.speedle.io `125f914b`).

## Vectors

`vectors.json` has 34 vectors: allowed, denied with no applicable policy,
denied by a deny policy, roles from users, groups and resource scopes, every
attribute type in single and array form, a fractional-second `datetime`, a
missing attribute in a grant and in a deny condition, and a wrong attribute
type.

Each vector is:

```json
{
  "name": "numeric-condition-boundary",
  "description": "clearance 3 satisfies clearance >= 3.",
  "request": {
    "subject": {"principals": [{"type": "user", "name": "dave"}]},
    "resource": "/vault",
    "action": "read",
    "attributes": [{"name": "clearance", "type": "numeric", "value": 3}]
  },
  "expect": {"allowed": true, "reason": 0}
}
```

`request` is a Speedle+ REST `JsonContext` without `serviceName`; the runner
adds the service name it is given. Attribute types are `string`, `numeric`,
`bool` and `datetime`; a JSON array value is the array form. `datetime`
values are RFC 3339 in UTC with at most millisecond precision, the form the
SDKs write. `expect` is the `is-allowed` body: `allowed`, `reason` and, when
Speedle+ sets it, `errorMessage`. The top-level `speedlePlus` field is the
Speedle+ commit the expectations were recorded with.

## Runner contract

Every `e2e/<language>/` directory has two scripts:

- `build.sh` builds the SDK from this repository and the runner. It takes no
  secrets.
- `run.sh` runs every vector with the SDK's detailed `check` call and compares
  `allowed`, `reason` and `errorMessage` with `expect`.

`run.sh` reads its configuration from the environment:

| Variable | Meaning |
| --- | --- |
| `SPEEDLE_E2E_ENDPOINT` | Required. Base URL, for example `https://g.speedle.io`. |
| `SPEEDLE_E2E_SERVICE` | Required. The `serviceName` to send; on Speedle Cloud, the token's service. |
| `SPEEDLE_E2E_TOKEN` | The bearer token. Required for `speedle-cloud`; for `speedle-plus` the runner uses no authentication when it is unset. |
| `SPEEDLE_E2E_BACKEND` | `speedle-cloud` (default) or `speedle-plus`. |
| `SPEEDLE_E2E_VECTORS` | Vector file, default `e2e/vectors.json`. |

The runners use the SDK defaults (3 attempts, 5 s deadline) and add
`speedle-sdk-e2e` to the `User-Agent`. They print `PASS <name>` or
`FAIL <name>: want ..., got ...` for each vector, then a summary line. An
error while running one vector, from the SDK or from a malformed vector, fails
that vector and the run goes on. An expectation without `allowed` or `reason`
never passes. The exit status is 0 when every vector passes, 1 when any fails,
and 2 for a configuration error or a vector file that is unreadable or has no
vectors. They never print the token.

## Local proof

Needs Go, git, curl, and the toolchains of the SDKs you name. Speedle+ is
built from the git checkout you point at; nothing is written into it.

```bash
SPEEDLE_PLUS_DIR=/path/to/speedle-plus e2e/speedle-plus/run-local.sh all
```

The script builds `speedle-pms`, `speedle-ads` and `spctl`, starts PMS on
`127.0.0.1:16733` with a temporary file store, creates the service and every
policy from the SPDL in `policies.json` with `spctl`, and checks that the
`parsed` form in the file is what PMS stored. It then starts a REST ADS on
`127.0.0.1:16734` with the same store, checks every vector with raw HTTP, and
runs the named SDK runners against it with `SPEEDLE_E2E_BACKEND=speedle-plus`
and no token (a `SPEEDLE_E2E_TOKEN` in your shell is not passed on).
Name one or more of `java`, `node`, `python`, `go`, `dotnet` and `rust`, or
`all`, or nothing to check only Speedle+.

The `e2e-local` workflow runs the same script for each SDK on GitHub-hosted
runners, with Speedle+ checked out at the `speedlePlus` commit in
`vectors.json`.

After changing the policies or the vectors, record the parsed forms and the
expectations again, then review the diff. Recording refuses a Speedle+
checkout with uncommitted changes, because the recorded commit would not
describe the build:

```bash
SPEEDLE_PLUS_DIR=/path/to/speedle-plus e2e/speedle-plus/run-local.sh --record
```

New entries in `policies.json` need a UUIDv7 `id` and millisecond
`createdAt`/`updatedAt` values; the import ignores them, but the file schema
requires them.

## Gamma

The workflow `e2e-gamma` runs the runners against `https://g.speedle.io`:

- on demand (**Actions → e2e-gamma → Run workflow**), with the input
  `language`: one SDK (default `java`) or `all`;
- weekly on Mondays at 04:23 UTC, for every SDK.

It runs on GitHub-hosted runners with `contents: read`, never on pull
requests, only from `main`, and runs the SDKs one after another. The build step has no secrets;
only the run step gets the token. Until both secrets exist, a scheduled run
writes a warning and stops, and a manual run fails.

### Prerequisites on gamma

- Scoped ADS bearer tokens:
  [cloud.speedle.io#136](https://github.com/teramoby/cloud.speedle.io/pull/136),
  after the owner's #79 acceptance. Until then gamma answers 401 to
  `is-allowed` without a valid token.
- The Speedle+ request format:
  [cloud.speedle.io#215](https://github.com/teramoby/cloud.speedle.io/pull/215).
  The SDKs send only that format (typed attribute arrays), so the runs are
  expected to fail on gamma until #215 is deployed there.

### One-time setup (owner)

1. In **Settings → Environments**, create the environment `gamma-e2e`.
   Restrict its deployment branches to `main`, so that a workflow run from
   another branch cannot read the token. Required reviewers are optional; with
   them, every run, including the weekly one, waits for approval.
2. Sign in to the console at <https://g.speedle.io> and create the service,
   for example `sdk-e2e`. Speedle Cloud allows one service per account, so use
   an account whose service is only for these tests; the name itself is free,
   because the runners take it from the secret.
3. Open the service's export page (`/service/export`), use **Import**, select
   `e2e/policies.json` from this repository, check the preview (19 policies:
   14 authorization, 5 role) and confirm. The import takes only the kind, name
   and SPDL of each policy; the service metadata and IDs in the file are
   ignored.
4. Once #136 is on gamma, create an ADS token for that service in the console.
5. Store two secrets in the `gamma-e2e` environment:
   - `SPEEDLE_E2E_TOKEN`: the ADS token;
   - `SPEEDLE_E2E_SERVICE`: the service name, for example `sdk-e2e`.
6. Run the workflow with `language: java`. When it passes, run it with
   `language: all`.

The import is append-only and rejects names that already exist. To update the
policies on gamma after a change to `policies.json`, delete the service's
policies (or the service) and import the file again.

### Quota

Each SDK run makes one call per vector: 34 calls, or 204 for all six SDKs.
Speedle Cloud charges each call, denials included, against the daily quota
and limits a token to 600 calls per minute.
A retried 503 `snapshot_unavailable` costs one more call per attempt, at most
three per vector.
