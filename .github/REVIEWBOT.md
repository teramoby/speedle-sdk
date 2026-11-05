# ReviewBot

ReviewBot posts static, model-written reviews on pull requests. It is the same
ReviewBot as [teramoby/speedle-plus](https://github.com/teramoby/speedle-plus)
(copied from `master` at `46a4f90`), which is adapted from
[Pavu at a7b458e](https://github.com/pavulabs/pavu/tree/a7b458ee3194865b653826a1b0c1119c28634c9d)
under Apache 2.0. It never runs, builds or tests PR code. It reads the diff and
sends it, with trusted prompts, to an Ark model.

## Flow

No workflow uses the privileged PR-target trigger. GitHub restricts it on public
repositories from December 2, 2026, and flagged its runs for manual approval.
ReviewBot follows the split from
[cloud.speedle.io](https://github.com/teramoby/cloud.speedle.io) (`125f914`):

1. **PR event (untrusted).** `reviewbot.yml` ("ReviewBot PR Event") runs on
   `pull_request` on `ubuntu-24.04` with `permissions: {}`, no checkout and no
   secrets. Its successful run is only a pointer to the PR and its head SHA.
2. **Review (trusted).** `reviewbot-review.yml` ("ReviewBot") runs on
   `workflow_run` from `main`. Its `prepare` job (`ubuntu-24.04`) checks that the
   source run is a successful `pull_request` run of `reviewbot.yml` from this
   repository, and that the PR is open, not a draft, from this repository and
   still at that head SHA. Only then do `review` and `publish` run on the
   self-hosted runner.
3. **Commands.** `reviewbot-comment.yml` ("ReviewBot Comment Command") runs on
   `issue_comment` on `ubuntu-24.04` with `permissions: {}`, no checkout and no
   secrets. Like `reviewbot-inline.yml` for code-line comments, it only records
   the comment ID. `reviewbot-command.yml` runs on `workflow_run` from `main`
   for both routers, re-reads the comment and authorizes it before analysis.
   Comments that do not mention `@reviewbot` or are not from a user skip the
   router job.

## What it does

- **Automatic review.** Opening, updating, reopening or marking a PR ready for
  review starts an analysis. Draft PRs and fork PRs are skipped. Each new head
  commit gets one `COMMENT` review that names the commit and the model ID. A bot review that
  already exists for that commit suppresses a second model call.
- **Comment commands** (below) start a review, a recheck or an explanation.
- Reviews never approve, request changes in GitHub's sense, or merge anything.

The review prompt comes from the PR's base revision (`.github/codex/prompts/`)
and asks the model to focus on fail-open decisions, credential handling,
divergence from `docs/design.md` and between the six SDKs, and conformance
coverage.

## Commands

Collaborators with GitHub `write`, `maintain` or `admin` permission can comment:

```text
@reviewbot review
@reviewbot review focus on the retry loop
@reviewbot recheck
@reviewbot check
@reviewbot explain why can this deadline be exceeded?
Is this attribute encoding right? @reviewbot
```

A bare mention means `review`; `check` means `recheck`; any other text becomes
an `explain` question. Mentions are case-insensitive. Commands work in ordinary
PR comments and in code-line review threads, including on draft PRs. The
commenter must be a human user with write permission, the PR must be open, and
its head branch must be in this repository; commands on fork PRs are ignored.

`review` and `recheck` publish a `COMMENT` review at the current head. `recheck`
re-evaluates up to five earlier ReviewBot outputs (80,000 characters at most).
`explain` replies in the same comment thread. An eyes reaction acknowledges an
authorized command and a thumbs-up marks a published reply. Each comment is
handled once. A missing key or a failed analysis produces a short status reply
without model or error text.

## Owner setup

ReviewBot does nothing until the secret exists. Without it, each run prints a
notice, sends no code and publishes nothing.

| Name | Kind | Required | Default | Purpose |
| --- | --- | --- | --- | --- |
| `ARK_API_KEY` | Actions secret | Yes | none | Ark Agent Plan API key. Either grant the existing organization secret to `speedle-sdk`, or create a repository secret with this name. |
| `ARK_REVIEW_MODEL` | Actions variable | No | `kimi-k3` | Ark model ID used for reviews and commands. |

The endpoint is `https://ark.cn-beijing.volces.com/api/plan/v3/responses`.
GitHub reads and writes use the job's `GITHUB_TOKEN`; no PAT or GitHub App is
needed. The trusted workflows become active once they are on `main`, because
`issue_comment` and `workflow_run` always run the default-branch version.

The `aliyun-reviewbot` runner group must allow exactly these workflows on
`main` of `teramoby/speedle-sdk`, the trusted ones whose jobs use the
self-hosted runner:

- `.github/workflows/reviewbot-review.yml`
- `.github/workflows/reviewbot-command.yml`

`reviewbot.yml`, `reviewbot-comment.yml` and `reviewbot-inline.yml` route
untrusted events on `ubuntu-24.04` and must not be in the group.

## Security model

- **No untrusted event with secrets.** The event routers (`reviewbot.yml`,
  `reviewbot-comment.yml`, `reviewbot-inline.yml`) have no token scopes,
  checkout or secrets. The trusted workflows check out the trusted workflow
  commit, fetch the base and head commits by SHA as objects only, and compute a
  merge-base `git diff` with `--no-ext-diff --no-textconv`. They run only helper
  scripts from the trusted commit, so PR `.gitattributes` and helpers have no
  effect.
- **Write-permission commands only.** `reviewbot-command.yml` checks the
  commenter's repository permission through the API before doing anything else.
- **Comment bridges.** `issue_comment` and `pull_request_review_comment` run
  pointer-only routers with `permissions: {}`, no checkout and no secrets; they
  only record the comment ID in the run name. The default-branch `workflow_run`
  handler in `reviewbot-command.yml` re-reads either kind of comment through
  the API and checks the router run, actor and timing. It also verifies the
  issue is a PR or the inline run's PR association before the usual permission
  check.
- **Report handoff.** The analysis job has a read-only token and the Ark key;
  the publishing job has write permissions and no model key. The report moves
  between them as a same-run artifact (`report.json`, 256 KiB at most, one-day
  retention), not a job output. Before upload, `.github/codex/reviewbot_handoff.py`
  rejects the report if it contains any repository secret value or the job
  token, logging the secret's name only. The publisher validates the schema and
  the run, attempt, PR, head and command binding before writing anything.
- **Bounded requests.** Input is capped at 750,000 bytes and output at 32768
  tokens. A response cut off by the output-token limit is retried once with
  3 lines of diff context instead of 80, within one 600-second HTTP budget.
  Other bad responses fail without publishing partial text.
- Actions are pinned to full commit SHAs and every job declares its permissions.
- **Runners.** Self-hosted jobs select the labels `self-hosted`, `aliyun-ecs`
  and `reviewbot`, served by the dedicated runner `ecs-light-teramoby` in the
  `aliyun-reviewbot` runner group (see Owner setup for the allowed workflows).
  PR event routing, PR event validation, command authorization and inline
  routing run on GitHub-hosted `ubuntu-24.04`, so public PRs and comments reach
  the self-hosted runner only after validation.

Data sent to Ark: the trusted prompts, `AGENTS.md` from the base revision if it
exists, commit metadata and the static diff. Commands also send the comment
text, its code context and, for `recheck`, earlier bot output. Fork PRs are not
reviewed. To stop model calls, remove the secret or disable the workflows.

## Differences from speedle-plus

- Trigger: the `pull_request` + `workflow_run` split from cloud.speedle.io
  replaces speedle-plus's PR-target review. Fork PRs are no longer
  reviewed automatically.
- Runners: the dedicated `aliyun-reviewbot` group (labels `aliyun-ecs`,
  `reviewbot`) instead of the shared `speedle-runner`.
- The report handoff and the lowercase `github_token` key fix come from
  [cloud.speedle.io#118](https://github.com/teramoby/cloud.speedle.io/pull/118)
  and [#130](https://github.com/teramoby/cloud.speedle.io/pull/130). They
  replace the `final_message` job output, which GitHub drops when it matches
  a secret mask.
- Not copied: the speedle-plus Dependabot auto-merge job and its merge
  eligibility marker, and the `workflow_dispatch` regression job. Both depend
  on speedle-plus CI, runners and test scripts.
- The review prompt describes this repository instead of the Speedle+ server.

## Files

- `.github/workflows/reviewbot.yml`, `reviewbot-review.yml`, `reviewbot-comment.yml`,
  `reviewbot-command.yml`, `reviewbot-inline.yml`
- `.github/actionlint.yaml`: the `aliyun-ecs` and `reviewbot` self-hosted labels
- `.github/codex/reviewbot_response.py`, `.github/codex/reviewbot_handoff.py`
- `.github/codex/prompts/reviewbot.md`, `.github/codex/prompts/reviewbot-command.md`
- `.github/codex/PAVU-LICENSE.txt`, `.github/codex/PAVU-NOTICE.txt`: the Apache 2.0
  license and notice for the reused ReviewBot source. The rest of the repository
  stays under UPL 1.0.

Treat these files as privileged CI configuration. Check changes with:

```sh
actionlint .github/workflows/reviewbot*.yml
zizmor --offline .github/workflows/reviewbot*.yml
```
