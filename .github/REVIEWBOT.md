# ReviewBot

ReviewBot posts static, model-written reviews on pull requests. It is the same
ReviewBot as [teramoby/speedle-plus](https://github.com/teramoby/speedle-plus)
(copied from `master` at `46a4f90`), which is adapted from
[Pavu at a7b458e](https://github.com/pavulabs/pavu/tree/a7b458ee3194865b653826a1b0c1119c28634c9d)
under Apache 2.0. It never runs, builds or tests PR code. It reads the diff and
sends it, with trusted prompts, to the configured model provider: Ark by
default, or DeepSeek when selected.

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

ReviewBot does nothing until the selected provider's secret exists. Without it,
each run prints a notice, sends no code and publishes nothing.

| Name | Kind | Required | Default | Purpose |
| --- | --- | --- | --- | --- |
| `REVIEWBOT_PROVIDER` | Actions variable | No | `ark` | Model provider: `ark` or `deepseek`. Switching providers is a variable change only. |
| `ARK_API_KEY` | Actions secret | When `ark` (default) | none | Ark Agent Plan API key. Either grant the existing organization secret to `speedle-sdk`, or create a repository secret with this name. |
| `ARK_REVIEW_MODELS` | Actions variable | No | `kimi-k3.1-pro deepseek-v4.1-flash glm-5.3` | Space-separated Ark model IDs, tried in this order. |
| `ARK_REVIEW_MAX_OUTPUT_TOKENS` | Actions variable | No | `32768` | Maximum output tokens per request. |
| `ARK_REVIEW_REASONING_EFFORT` | Actions variable | No | `medium` | Ark reasoning effort: `low`, `medium` or `high`. |
| `ARK_REVIEW_INPUT_LIMIT` | Actions variable | No | `750000` | Maximum input bytes before truncation. |
| `ARK_REVIEW_MODEL` | Actions variable | No | none | Legacy override. If `ARK_REVIEW_MODELS` is unset, this model is tried before the default chain. |
| `DEEPSEEK_API_KEY` | Actions secret | When `deepseek` | none | DeepSeek API key. |
| `DEEPSEEK_REVIEW_MODEL` | Actions variable | No | `deepseek-flash` | DeepSeek model ID. The API's model list is `deepseek-flash` and `deepseek-v4-pro`. |

All defaults live in `.github/codex/reviewbot_model.py`,
`.github/codex/reviewbot_ark.py` and `.github/codex/reviewbot_deepseek.py`; the
workflow YAML only forwards variable values. Ark uses
`https://ark.cn-beijing.volces.com/api/plan/v3/responses`; DeepSeek uses
`https://api.deepseek.com/chat/completions` (OpenAI-compatible,
non-streaming).

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

## Providers

`REVIEWBOT_PROVIDER` selects the provider. It is unset by default, which means
`ark`, so the existing Ark path is unchanged. Setting it to `deepseek` switches
to DeepSeek without a workflow or script edit.

- **Ark.** The trusted input is sent as one `input` string. Models from
  `ARK_REVIEW_MODELS` are tried in order; `max_output_tokens`,
  `reasoning.effort` and the input cap come from the `ARK_REVIEW_*` variables.
- **DeepSeek.** The trusted prompt is the system message; the trusted policy
  from the base revision, commit metadata and diff form the user message
  (commands also include the untrusted request, context and prior output). The
  model is `DEEPSEEK_REVIEW_MODEL` (default `deepseek-flash`), requested with
  `max_tokens: 65536` and `stream: false`. `deepseek-flash` has a 1M-token
  context and a 384K-token maximum output; the system-plus-user input is capped
  at 750,000 bytes, so the request stays inside both. The review text is
  `choices[0].message.content`; any `reasoning_content` is ignored.
  `finish_reason: "length"` is the Ark output-token-limit case and retries once
  with unified=3 diff context.

Both providers use the same transport, HTTP-error, timeout and unusable-output
handling and the same reason codes. Response parsing, the comment format,
verdict markers and the footer are shared; the footer names the model that
produced the review.

## Stable workflows, editable scripts

GitHub can hold a new or changed privileged workflow for manual approval. Never
change `reviewbot-review.yml` or `reviewbot-command.yml` casually: **each change
to either file needs one owner approval for that file**. These files are stable
shells containing only triggers, permissions, concurrency, runner settings,
trusted default-branch checkouts, calls into trusted stage scripts, and the
validated same-run handoff. The pointer-only routers stay unprivileged and
contain no analysis or publishing logic.

Request building, provider calls, failover, response checks, parsing,
authorization and publishing live under `.github/codex/`. Provider selection
and other tunables live in repository variables, with defaults in
`reviewbot_model.py`, `reviewbot_ark.py` and `reviewbot_deepseek.py`. Prompts
are under `.github/codex/prompts/`. After the trusted change merges to the
default branch, a diff that changes only these scripts or prompts, or a change
only to the repository variables above, needs **no privileged-workflow
approval**.

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
- **Report handoff.** The analysis job has a read-only token and the selected
  provider's key; the publishing job has write permissions and no model key.
  The report moves
  between them as a same-run artifact (`report.json`, 256 KiB at most, one-day
  retention), not a job output. Before upload, `.github/codex/reviewbot_handoff.py`
  rejects the report if it contains any repository secret value or the job
  token, logging the secret's name only. The publisher validates the schema and
  the run, attempt, PR, head and command binding before writing anything.
- **Bounded requests and failover.** Ark input is capped by
  `ARK_REVIEW_INPUT_LIMIT` (750,000 bytes by default) and output by
  `ARK_REVIEW_MAX_OUTPUT_TOKENS` (32768 by default); DeepSeek caps the
  system-plus-user input at 750,000 bytes and requests `max_tokens: 65536`.
  Ark models are tried in `ARK_REVIEW_MODELS` order on HTTP errors, request
  timeouts, incomplete responses or reasoning-only answers; DeepSeek uses one
  model. A response cut off by the output-token limit (DeepSeek
  `finish_reason: "length"`) is retried once on the same model with 3 lines of
  diff context instead of 80; both attempts share that model's 360-second HTTP
  budget. The first usable review is published with its model ID. If every
  model fails, nothing is published and analysis fails with
  `all_models_failed`.
- Actions are pinned to full commit SHAs and every job declares its permissions.
- **Runners.** Self-hosted jobs select the labels `self-hosted`, `aliyun-ecs`
  and `reviewbot`, served by the dedicated runner `ecs-light-teramoby` in the
  `aliyun-reviewbot` runner group (see Owner setup for the allowed workflows).
  PR event routing, PR event validation, command authorization and inline
  routing run on GitHub-hosted `ubuntu-24.04`, so public PRs and comments reach
  the self-hosted runner only after validation.

Data sent to the selected provider: the trusted prompts, `AGENTS.md` from the
base revision if it exists, commit metadata and the static diff. Under `ark`
the provider is Ark; under `deepseek` it is DeepSeek. Commands also send the
comment text, its code context and, for `recheck`, earlier bot output. Fork PRs
are not reviewed. To stop model calls, remove the active provider's secret or
disable the workflows.

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
- The review prompt describes this repository instead of the self-hosted Speedle server.

## Files

- `.github/workflows/reviewbot.yml`, `reviewbot-review.yml`, `reviewbot-comment.yml`,
  `reviewbot-command.yml`, `reviewbot-inline.yml`
- `.github/actionlint.yaml`: the `aliyun-ecs` and `reviewbot` self-hosted labels
- `.github/codex/reviewbot_github.js`: trusted validation, authorization and publishing stages
- `.github/codex/reviewbot_analyze.sh`: immutable-commit input construction
- `.github/codex/reviewbot_model.py`: provider selection, shared tunables, transport and ordered model failover
- `.github/codex/reviewbot_ark.py`: Ark endpoint, defaults, request payload and response extraction
- `.github/codex/reviewbot_deepseek.py`: DeepSeek endpoint, defaults, request payload and response extraction
- `.github/codex/reviewbot_response.py`, `.github/codex/reviewbot_handoff.py`
- `.github/codex/tests/test_reviewbot_model.py`: local provider, response and failover tests
- `.github/codex/prompts/reviewbot.md`, `.github/codex/prompts/reviewbot-command.md`
- `.github/codex/PAVU-LICENSE.txt`, `.github/codex/PAVU-NOTICE.txt`: the Apache 2.0
  license and notice for the reused ReviewBot source. The rest of the repository
  stays under UPL 1.0.

Check workflow and script changes with:

```sh
actionlint .github/workflows/reviewbot*.yml
zizmor --offline .github/workflows/reviewbot*.yml
node --check .github/codex/reviewbot_github.js
node --test .github/codex/tests/reviewbot_github.test.js
shellcheck .github/codex/reviewbot_analyze.sh
python3 -I -m unittest discover -s .github/codex/tests -v
```
