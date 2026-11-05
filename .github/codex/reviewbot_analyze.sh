#!/usr/bin/env bash
# Adapted from Pavu a7b458e; modified for Speedle+. Apache-2.0; see .github/codex/PAVU-LICENSE.txt.
# Build a static diff from immutable commits, then call the trusted Ark runner.
set -euo pipefail

mode="${1:-}"
if [[ "${mode}" != review && "${mode}" != command ]]; then
  echo 'ReviewBot reason_code=invalid_analysis_mode' >&2
  exit 2
fi
: "${RUNNER_TEMP:?}" "${TRUSTED_SHA:?}" "${BASE_SHA:?}" "${HEAD_SHA:?}" "${GH_TOKEN:?}"
[[ "${TRUSTED_SHA}" =~ ^[a-f0-9]{40}$ && "${BASE_SHA}" =~ ^[a-f0-9]{40}$ && "${HEAD_SHA}" =~ ^[a-f0-9]{40}$ ]]

# The checkout and this script are from the trusted workflow revision. Fetch the
# reviewed commits as objects only; never check out or execute the PR head.
auth=$(printf 'x-access-token:%s' "${GH_TOKEN}" | base64 -w0)
git -c "http.extraheader=AUTHORIZATION: basic ${auth}" fetch --no-tags origin "${BASE_SHA}" "${HEAD_SHA}"
merge_base=$(git merge-base "${BASE_SHA}" "${HEAD_SHA}")

if [[ "${mode}" == review ]]; then
  prefix="${RUNNER_TEMP}/reviewbot"
else
  prefix="${RUNNER_TEMP}/reviewbot-command"
  : "${COMMAND:?}"
  [[ "${COMMAND}" == review || "${COMMAND}" == recheck || "${COMMAND}" == explain ]]
  printf '%s' "${REQUEST_B64}" | base64 --decode > "${prefix}-request.txt"
  printf '%s' "${CONTEXT_B64}" | base64 --decode > "${prefix}-context.txt"
  if [[ -n "${HISTORY_B64}" ]]; then
    printf '%s' "${HISTORY_B64}" | base64 --decode > "${prefix}-history.txt"
  else
    : > "${prefix}-history.txt"
  fi
fi

build_review_input() {
  local unified="$1" destination="$2"
  {
    if [[ "${mode}" == review ]]; then
      git show "${BASE_SHA}:.github/codex/prompts/reviewbot.md"
      printf '\n\n## Trusted policy from the base revision\n\n'
    else
      git show "${BASE_SHA}:.github/codex/prompts/reviewbot.md" || \
        git show "${TRUSTED_SHA}:.github/codex/prompts/reviewbot.md"
      printf '\n\n'
      git show "${TRUSTED_SHA}:.github/codex/prompts/reviewbot-command.md"
      printf '\n\n## Trusted policy from the base revision\n\n'
    fi
    git show "${BASE_SHA}:AGENTS.md" || printf 'No AGENTS.md exists at the base revision.\n'
    if [[ "${mode}" == review ]]; then
      printf '\n\n## Pull request metadata\n\n'
      # shellcheck disable=SC2016 # Literal Markdown backticks, not command substitution.
      printf 'Base commit: `%s`\nHead commit: `%s`\nMerge base: `%s`\n\n' "${BASE_SHA}" "${HEAD_SHA}" "${merge_base}"
      git diff --no-ext-diff --no-textconv --stat "${merge_base}" "${HEAD_SHA}"
      printf '\n\n## Unified diff\n\n```diff\n'
    else
      printf '\n\n## Command metadata\n\n'
      # shellcheck disable=SC2016 # Literal Markdown backticks, not command substitution.
      printf 'Mode: `%s`\nBase commit: `%s`\nHead commit: `%s`\nMerge base: `%s`\n' "${COMMAND}" "${BASE_SHA}" "${HEAD_SHA}" "${merge_base}"
      printf '\n\n## Untrusted collaborator request\n\n'
      sed 's/^/> /' "${prefix}-request.txt"
      printf '\n\n## Untrusted triggering-comment context\n\n```json\n'
      cat "${prefix}-context.txt"
      printf '\n```\n'
      if [[ -s "${prefix}-history.txt" ]]; then
        printf '\n\n## Untrusted prior ReviewBot output\n\n'
        cat "${prefix}-history.txt"
      fi
      printf '\n\n## Pull request diff stat\n\n'
      git diff --no-ext-diff --no-textconv --stat "${merge_base}" "${HEAD_SHA}"
      printf '\n\n## Untrusted unified diff\n\n```diff\n'
    fi
    git diff --no-ext-diff --no-textconv --find-renames --find-copies --unified="${unified}" "${merge_base}" "${HEAD_SHA}"
    printf '\n```\n'
  } > "${destination}"
}

build_review_input 80 "${prefix}-input.txt"
build_review_input 3 "${prefix}-input-retry.txt"
python3 .github/codex/reviewbot_ark.py \
  "${prefix}-input.txt" \
  "${prefix}-input-retry.txt" \
  "${prefix}-result.json" \
  "${prefix}-request.json" \
  "${prefix}-response.json"
