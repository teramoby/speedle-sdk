# ReviewBot

ReviewBot posts static, model-written reviews on pull requests. It is the same
ReviewBot as [teramoby/speedle-plus](https://github.com/teramoby/speedle-plus)
(copied from `master` at `46a4f90`), which is adapted from
[Pavu at a7b458e](https://github.com/pavulabs/pavu/tree/a7b458ee3194865b653826a1b0c1119c28634c9d)
under Apache 2.0. It never runs, builds or tests PR code. It reads the diff and
sends it, with trusted prompts, to an Ark model.

## What it does

- **Automatic review.** Opening, updating, reopening or marking a PR ready for
  review starts an analysis. Draft PRs are skipped. Each new head commit gets one
  `COMMENT` review that names the commit and the model ID. A bot review that
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
needed. The workflows become active once they are on `main`, because
`pull_request_target`, `issue_comment` and `workflow_run` always run the
default-branch version.

## Security model

- **No PR code with secrets.** `reviewbot.yml` uses `pull_request_target` and
  checks out the PR head only to compute a merge-base `git diff`. It deletes the
  PR's `.gitattributes` files so they cannot hide changes, and runs only helper
  scripts read with `git show` from the trusted workflow commit. The command
  workflow checks out the trusted workflow commit, not the PR head.
- **Write-permission commands only.** `reviewbot-command.yml` checks the
  commenter's repository permission through the API before doing anything else.
- **Inline-comment bridge.** `pull_request_review_comment` runs at the PR merge
  revision, so `reviewbot-inline.yml` has `permissions: {}`, no checkout and no
  secrets; it only records the comment ID in its run name. The default-branch
  `workflow_run` handler in `reviewbot-command.yml` re-reads the comment through
  the API and checks the run, actor, PR association and timing before the usual
  permission check.
- **Report handoff.** The analysis job has a read-only token and the Ark key;
  the publishing job has write permissions and no model key. The report moves
  between them as a same-run artifact (`report.json`, 256 KiB at most, one-day
  retention), not a job output. Before upload, `.github/codex/reviewbot_handoff.py`
  rejects the report if it contains any repository secret value or the job
  token, logging the secret's name only. The publisher validates the schema and
  the run, attempt, PR, head and command binding before writing anything.
- **Bounded requests.** Input is capped at 750,000 bytes and output at 8192
  tokens. A response cut off by the output-token limit is retried once with
  3 lines of diff context instead of 80, within one 600-second HTTP budget.
  Other bad responses fail without publishing partial text.
- Actions are pinned to full commit SHAs, every job declares its permissions,
  and every job runs on GitHub-hosted `ubuntu-latest`.

Data sent to Ark: the trusted prompts, `AGENTS.md` from the base revision if it
exists, commit metadata and the static diff. Commands also send the comment
text, its code context and, for `recheck`, earlier bot output. Automatic reviews
also run for fork PRs (the diff is still only read). To stop model calls, remove
the secret or disable the workflows.

## Differences from speedle-plus

- Runners: every job uses GitHub-hosted `ubuntu-latest` instead of the
  speedle-plus self-hosted runner group.
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

- `.github/workflows/reviewbot.yml`, `reviewbot-command.yml`, `reviewbot-inline.yml`
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
