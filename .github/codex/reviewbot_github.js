// Adapted from Pavu a7b458e; modified for Speedle+. Apache-2.0; see .github/codex/PAVU-LICENSE.txt.
// Trusted GitHub API stages for the thin ReviewBot workflows.
"use strict";

const fs = require("node:fs");

async function validatePrEvent({ github, context, core }) {
  core.setOutput("should_run", "false");
  const run = context.payload.workflow_run;
  const repository = `${context.repo.owner}/${context.repo.repo}`;
  if (run.event !== "pull_request" || run.conclusion !== "success" ||
      run.path !== ".github/workflows/reviewbot.yml" || run.name !== "ReviewBot PR Event" ||
      run.head_repository?.full_name !== repository ||
      !/^[a-f0-9]{40}$/.test(run.head_sha ?? "")) return;
  const pulls = Array.isArray(run.pull_requests) ? run.pull_requests : [];
  if (pulls.length !== 1 || !Number.isSafeInteger(pulls[0].number) || pulls[0].number < 1) return;
  const { data: pull } = await github.rest.pulls.get({
    ...context.repo, pull_number: pulls[0].number,
  });
  if (pull.state !== "open" || pull.draft || pull.head.repo?.full_name !== repository ||
      pull.head.sha !== run.head_sha || !/^[a-f0-9]{40}$/.test(pull.base.sha)) return;
  core.setOutput("pull_number", String(pull.number));
  core.setOutput("head_sha", pull.head.sha);
  core.setOutput("base_sha", pull.base.sha);
  core.setOutput("should_run", "true");
}

async function reviewGate({ github, context, core }) {
  const enabled = Boolean(process.env.REVIEWBOT_API_KEY);
  core.setOutput("enabled", enabled ? "true" : "false");
  core.setOutput("should_review", "false");
  if (!enabled) {
    core.notice("ReviewBot is disabled until the ARK_API_KEY repository secret is configured.");
    return;
  }
  const commit = process.env.REVIEWED_COMMIT;
  const marker = `<!-- speedle-plus-reviewbot:${commit} -->`;
  const reviews = await github.paginate(github.rest.pulls.listReviews, {
    owner: context.repo.owner,
    repo: context.repo.repo,
    pull_number: Number(process.env.PULL_NUMBER),
    per_page: 100,
  });
  const exists = reviews.some(
    (review) =>
      review.user?.login === "github-actions[bot]" &&
      review.commit_id === commit &&
      review.body?.startsWith(`${marker}\n`),
  );
  core.setOutput("should_review", exists ? "false" : "true");
  if (exists) core.notice(`ReviewBot already reviewed ${commit}; skipping Ark analysis.`);
}

async function prepareCommand({ github, context, core }) {
  core.setOutput("should_run", "false");

  let comment;
  let pullNumber;
  let sourceKind;
  if (context.eventName !== "workflow_run") {
    core.warning("ReviewBot commands require a comment router run.");
    return;
  }

  // Router runs are untrusted ID pointers only. Resolve identities and comment
  // context through GitHub before authorizing anything.
  const run = context.payload.workflow_run;
  const repository = `${context.repo.owner}/${context.repo.repo}`;
  const issueRouter = run?.event === "issue_comment" &&
    run.name === "ReviewBot Comment Command" &&
    run.path === ".github/workflows/reviewbot-comment.yml";
  const inlineRouter = run?.event === "pull_request_review_comment" &&
    run.name === "ReviewBot Inline Command" &&
    run.path === ".github/workflows/reviewbot-inline.yml";
  if ((!issueRouter && !inlineRouter) ||
      run.conclusion !== "success" ||
      run.actor?.type !== "User" ||
      run.head_repository?.full_name !== repository) {
    core.warning("Ignoring an invalid comment router run.");
    return;
  }
  const pointer = typeof run.display_title === "string"
    ? run.display_title.match(issueRouter
      ? /^ReviewBot comment ([1-9][0-9]*)$/
      : /^ReviewBot inline comment ([1-9][0-9]*)$/)
    : null;
  const routerCommentId = pointer ? Number(pointer[1]) : NaN;
  if (!Number.isSafeInteger(routerCommentId)) {
    core.warning("Ignoring an invalid comment pointer.");
    return;
  }
  if (issueRouter) {
    ({ data: comment } = await github.rest.issues.getComment({
      owner: context.repo.owner,
      repo: context.repo.repo,
      comment_id: routerCommentId,
    }));
  } else {
    ({ data: comment } = await github.rest.pulls.getReviewComment({
      owner: context.repo.owner,
      repo: context.repo.repo,
      comment_id: routerCommentId,
    }));
  }
  const commentTime = Date.parse(comment.created_at);
  const runTime = Date.parse(run.created_at);
  if (comment.id !== routerCommentId ||
      !Number.isSafeInteger(comment.user?.id) ||
      comment.user.id !== run.actor?.id ||
      comment.user.login !== run.actor?.login ||
      !Number.isFinite(commentTime) || !Number.isFinite(runTime) ||
      commentTime > runTime || runTime - commentTime > 120_000) {
    core.warning("Comment identity or creation time does not match the router run.");
    return;
  }

  let targetUrl;
  try {
    targetUrl = new URL(issueRouter ? comment.issue_url : comment.pull_request_url);
  } catch {
    core.warning("Comment does not identify its issue or pull request.");
    return;
  }
  const prefix = `/repos/${repository}/${issueRouter ? "issues" : "pulls"}/`;
  const number = targetUrl.pathname.startsWith(prefix)
    ? targetUrl.pathname.slice(prefix.length) : "";
  if (targetUrl.origin !== new URL(context.apiUrl).origin ||
      targetUrl.search || targetUrl.hash || !/^[1-9][0-9]*$/.test(number) ||
      !Number.isSafeInteger(Number(number))) {
    core.warning("Comment points outside this repository.");
    return;
  }
  pullNumber = Number(number);

  if (issueRouter) {
    const { data: issue } = await github.rest.issues.get({
      owner: context.repo.owner,
      repo: context.repo.repo,
      issue_number: pullNumber,
    });
    if (issue.number !== pullNumber || !issue.pull_request) {
      core.warning("Comment router run does not identify a pull request.");
      return;
    }
    sourceKind = "issue_comment";
  } else {
    const associated = Array.isArray(run.pull_requests) ? run.pull_requests : [];
    let pulls = associated;
    if (pulls.length === 0) {
      if (!/^[a-f0-9]{40}$/.test(run.head_sha ?? "")) {
        core.warning("Inline router run has no verifiable commit association.");
        return;
      }
      pulls = await github.paginate(github.rest.repos.listPullRequestsAssociatedWithCommit, {
        owner: context.repo.owner,
        repo: context.repo.repo,
        commit_sha: run.head_sha,
        per_page: 100,
      });
    }
    if (!pulls.some(pull => pull.number === pullNumber && pull.url === targetUrl.href)) {
      core.warning("Inline router run is not associated with this comment's pull request.");
      return;
    }
    sourceKind = "review_comment";
  }
  const body = comment?.body ?? "";
  const mention = body.match(/(?:^|[^\w@-])@reviewbot(?![\w-])/i);
  if (!mention) {
    core.notice("Comment does not mention ReviewBot.");
    return;
  }

  const mentionStart = mention.index + mention[0].lastIndexOf("@");
  const instruction = `${body.slice(0, mentionStart)}${body.slice(mentionStart + "@reviewbot".length)}`.trim();
  const explicit = instruction.match(/^(review|recheck|check|explain)(?:\s+([\s\S]*))?$/i);
  const command = explicit
    ? (explicit[1].toLowerCase() === "check" ? "recheck" : explicit[1].toLowerCase())
    : (instruction.length === 0 ? "review" : "explain");
  let request = explicit ? (explicit[2] ?? "").trim() : instruction;
  if (command === "explain" && request.length === 0) {
    request = "Explain the relevant code and any security or correctness implications in this comment context.";
  }

  const actor = comment.user?.login;
  if (!actor || comment.user?.type !== "User") {
    core.warning("ReviewBot commands require a human GitHub actor.");
    return;
  }

  const permissionResponse = await github.rest.repos.getCollaboratorPermissionLevel({
    owner: context.repo.owner,
    repo: context.repo.repo,
    username: actor,
  });
  const permission = permissionResponse.data.permission;
  if (!["admin", "maintain", "write"].includes(permission)) {
    core.warning(`Ignoring ReviewBot command from ${actor}: ${permission} permission is insufficient.`);
    return;
  }

  const { data: pull } = await github.rest.pulls.get({
    owner: context.repo.owner,
    repo: context.repo.repo,
    pull_number: pullNumber,
  });

  if (pull.state !== "open") {
    core.warning("ReviewBot comment commands are disabled for closed pull requests.");
    return;
  }
  if (pull.head.repo?.full_name !== repository) {
    core.warning("ReviewBot comment commands are disabled for fork pull requests.");
    return;
  }

  const commentId = String(comment.id);
  const reactionRoute = sourceKind === "review_comment"
    ? "POST /repos/{owner}/{repo}/pulls/comments/{comment_id}/reactions"
    : "POST /repos/{owner}/{repo}/issues/comments/{comment_id}/reactions";
  try {
    await github.request(reactionRoute, {
      owner: context.repo.owner,
      repo: context.repo.repo,
      comment_id: Number(commentId),
      content: "eyes",
    });
  } catch (error) {
    core.warning(`Could not add the eyes reaction: ${error.message}`);
  }

  const marker = `<!-- speedle-plus-reviewbot-command:${command}:${sourceKind}:${commentId} -->`;
  const [reviews, issueComments, reviewComments] = await Promise.all([
    github.paginate(github.rest.pulls.listReviews, {
      owner: context.repo.owner,
      repo: context.repo.repo,
      pull_number: pullNumber,
      per_page: 100,
    }),
    github.paginate(github.rest.issues.listComments, {
      owner: context.repo.owner,
      repo: context.repo.repo,
      issue_number: pullNumber,
      per_page: 100,
    }),
    github.paginate(github.rest.pulls.listReviewComments, {
      owner: context.repo.owner,
      repo: context.repo.repo,
      pull_number: pullNumber,
      per_page: 100,
    }),
  ]);
  const alreadyHandled = [...reviews, ...issueComments, ...reviewComments].some(
    (item) =>
      item.user?.login === "github-actions[bot]" &&
      item.body?.startsWith(`${marker}\n`),
  );
  if (alreadyHandled) {
    core.notice(`ReviewBot command ${commentId} was already handled.`);
    return;
  }

  const sourceContext = sourceKind === "review_comment"
    ? {
        url: comment.html_url,
        path: comment.path,
        line: comment.line,
        startLine: comment.start_line,
        side: comment.side,
        diffHunk: comment.diff_hunk,
      }
    : { url: comment.html_url };
  const encode = (value) => Buffer.from(value, "utf8").toString("base64");

  core.setOutput("command", command);
  core.setOutput("pull_number", String(pullNumber));
  core.setOutput("base_sha", pull.base.sha);
  core.setOutput("head_sha", pull.head.sha);
  core.setOutput("actor", actor);
  core.setOutput("comment_id", commentId);
  core.setOutput("reply_to_comment_id", String(comment.in_reply_to_id ?? comment.id));
  core.setOutput("source_kind", sourceKind);
  core.setOutput("source_url", comment.html_url);
  core.setOutput("request_b64", encode(request));
  core.setOutput("context_b64", encode(JSON.stringify(sourceContext, null, 2)));
  core.setOutput("should_run", "true");
}

async function commandAnalyzeGate({ github, context, core }) {
  const enabled = Boolean(process.env.REVIEWBOT_API_KEY);
  core.setOutput("enabled", enabled ? "true" : "false");
  core.setOutput("history_b64", "");
  if (!enabled) {
    core.notice("ReviewBot is disabled until the ARK_API_KEY repository secret is configured.");
    return;
  }
  if (process.env.COMMAND !== "recheck") return;
  const pullNumber = Number(process.env.PULL_NUMBER);
  const [reviews, comments] = await Promise.all([
    github.paginate(github.rest.pulls.listReviews, {
      owner: context.repo.owner,
      repo: context.repo.repo,
      pull_number: pullNumber,
      per_page: 100,
    }),
    github.paginate(github.rest.issues.listComments, {
      owner: context.repo.owner,
      repo: context.repo.repo,
      issue_number: pullNumber,
      per_page: 100,
    }),
  ]);
  const history = [...reviews, ...comments]
    .filter(
      (item) =>
        item.user?.login === "github-actions[bot]" &&
        (
          item.body?.includes("<!-- speedle-plus-reviewbot:") ||
          item.body?.includes("<!-- speedle-plus-reviewbot -->") ||
          item.body?.includes("<!-- speedle-plus-reviewbot-command:review:") ||
          item.body?.includes("<!-- speedle-plus-reviewbot-command:recheck:")
        ),
    )
    .sort((left, right) => {
      const leftTime = left.submitted_at ?? left.updated_at ?? left.created_at ?? "";
      const rightTime = right.submitted_at ?? right.updated_at ?? right.created_at ?? "";
      return leftTime.localeCompare(rightTime);
    })
    .slice(-5)
    .map((item) => item.body)
    .join("\n\n---\n\n")
    .slice(-80_000);
  core.setOutput("history_b64", Buffer.from(history, "utf8").toString("base64"));
}

function readHandoff() {
  const file = `${process.env.RUNNER_TEMP}/reviewbot-handoff/report.json`;
  let report;
  try {
    const stat = fs.lstatSync(file);
    if (!stat.isFile() || stat.isSymbolicLink() || stat.size > 262144) throw new Error();
    const bytes = fs.readFileSync(file);
    const text = bytes.toString("utf8");
    if (!Buffer.from(text, "utf8").equals(bytes)) throw new Error();
    report = JSON.parse(text);
    const keys = ["version", "message", "model", "run_id", "run_attempt", "pull_number", "head_sha", "command"];
    if (!report || Array.isArray(report) || Object.keys(report).length !== keys.length ||
        !keys.every((key) => Object.hasOwn(report, key)) || report.version !== 1 ||
        typeof report.message !== "string" || !report.message.trim() ||
        Buffer.byteLength(report.message, "utf8") > 241000 ||
        typeof report.model !== "string" || !/^[A-Za-z0-9][A-Za-z0-9._:/-]{0,199}$/.test(report.model) ||
        report.run_id !== process.env.GITHUB_RUN_ID ||
        report.run_attempt !== process.env.GITHUB_RUN_ATTEMPT ||
        report.pull_number !== process.env.PULL_NUMBER ||
        report.head_sha !== process.env.HEAD_SHA || report.command !== process.env.COMMAND) throw new Error();
  } catch {
    throw new Error("ReviewBot handoff is missing, oversized, malformed or bound to a different review; no report published.");
  }
  return report;
}

async function publishReview({ github, context, core }) {
  const analyzeResult = process.env.ANALYZE_RESULT;
  const enabled = process.env.REVIEWBOT_ENABLED;
  const shouldReview = process.env.SHOULD_REVIEW;
  if (analyzeResult !== "success" ||
      !["true", "false"].includes(enabled) ||
      (enabled === "true" && !["true", "false"].includes(shouldReview))) {
    core.setFailed("ReviewBot analysis did not produce a publishable outcome. See the analyze job; no model text will be published.");
    return;
  }
  if (enabled !== "true" || shouldReview !== "true") return;
  const report = readHandoff();
  const message = report.message.trim();
  const modelId = report.model;
  const commit = process.env.REVIEWED_COMMIT;
  const marker = `<!-- speedle-plus-reviewbot:${commit} -->`;
  const modelFooter = modelId
    ? `\n\n_Model ID: \`${modelId.replaceAll("`", "\\`").replace(/[\r\n]/g, " ")}\`_`
    : "";
  const suffix = `\n\n_Reviewed commit: \`${commit}\`_${modelFooter}`;
  const maximumLength = 65_000;
  const prefix = `${marker}\n`;
  let body = `${prefix}${message}${suffix}`;

  if (body.length > maximumLength) {
    const notice = "\n\n_ReviewBot output was truncated to fit GitHub's comment limit._";
    const budget = Math.max(0, maximumLength - prefix.length - notice.length - suffix.length);
    body = `${prefix}${message.slice(0, budget)}${notice}${suffix}`;
  }

  const reviews = await github.paginate(github.rest.pulls.listReviews, {
    owner: context.repo.owner,
    repo: context.repo.repo,
    pull_number: Number(process.env.PULL_NUMBER),
    per_page: 100,
  });
  const existing = reviews.find(
    (review) =>
      review.user?.login === "github-actions[bot]" &&
      review.commit_id === commit &&
      review.body?.startsWith(`${marker}\n`),
  );
  if (existing) {
    core.notice(`ReviewBot already published review ${existing.id} for ${commit}.`);
    return;
  }
  await github.rest.pulls.createReview({
    owner: context.repo.owner,
    repo: context.repo.repo,
    pull_number: Number(process.env.PULL_NUMBER),
    commit_id: commit,
    body,
    event: "COMMENT",
  });
}

async function publishCommand({ github, context, core }) {
  let modelMessage = "", modelId = "";
  const analyzeResult = process.env.ANALYZE_RESULT;
  const reviewBotEnabled = process.env.REVIEWBOT_ENABLED;
  const validOutcome = analyzeResult === "success" && ["true", "false"].includes(reviewBotEnabled);
  if (!validOutcome) {
    core.setFailed("ReviewBot analysis did not produce a publishable outcome. See the analyze job; no model text will be published.");
  }
  if (analyzeResult === "success" && reviewBotEnabled === "true") {
    const report = readHandoff();
    modelMessage = report.message.trim();
    modelId = report.model;
  }
  const command = process.env.COMMAND;
  const pullNumber = Number(process.env.PULL_NUMBER);
  const headSha = process.env.HEAD_SHA;
  const actor = process.env.ACTOR;
  const commentId = process.env.COMMENT_ID;
  const replyToCommentId = process.env.REPLY_TO_COMMENT_ID;
  const sourceKind = process.env.SOURCE_KIND;
  const sourceUrl = process.env.SOURCE_URL;
  const marker = `<!-- speedle-plus-reviewbot-command:${command}:${sourceKind}:${commentId} -->`;
  const reactionRoute = sourceKind === "review_comment"
    ? "POST /repos/{owner}/{repo}/pulls/comments/{comment_id}/reactions"
    : "POST /repos/{owner}/{repo}/issues/comments/{comment_id}/reactions";
  const addReaction = async (content) => {
    try {
      await github.request(reactionRoute, {
        owner: context.repo.owner,
        repo: context.repo.repo,
        comment_id: Number(commentId),
        content,
      });
    } catch (error) {
      core.warning(`Could not add the ${content} reaction: ${error.message}`);
    }
  };
  const successful = analyzeResult === "success" &&
    reviewBotEnabled === "true" &&
    modelMessage.length > 0;
  let message;
  if (successful) {
    message = modelMessage;
  } else if (reviewBotEnabled !== "true") {
    message = "## ReviewBot unavailable\n\nReviewBot is not configured for this repository. A repository administrator must configure the Ark credential before commands can run.";
  } else {
    message = `## ReviewBot command failed\n\nReviewBot could not complete this command. See the [workflow run](${context.serverUrl}/${context.repo.owner}/${context.repo.repo}/actions/runs/${context.runId}) for status, then post a new command to retry.`;
  }
  const modelFooter = successful && modelId
    ? `\n\n_Model ID: \`${modelId.replaceAll("`", "\\`").replace(/[\r\n]/g, " ")}\`_`
    : "";
  const suffix = `\n\n_Triggered by [@${actor}'s comment](${sourceUrl}) · Commit: \`${headSha}\`_${modelFooter}`;
  const maximumLength = 65_000;
  let body = `${marker}\n${message}${suffix}`;

  if (body.length > maximumLength) {
    const note = "\n\n_ReviewBot output was truncated to fit GitHub's comment limit._";
    body = `${body.slice(0, maximumLength - note.length - suffix.length)}${note}${suffix}`;
  }

  const [reviews, issueComments, reviewComments] = await Promise.all([
    github.paginate(github.rest.pulls.listReviews, {
      owner: context.repo.owner,
      repo: context.repo.repo,
      pull_number: pullNumber,
      per_page: 100,
    }),
    github.paginate(github.rest.issues.listComments, {
      owner: context.repo.owner,
      repo: context.repo.repo,
      issue_number: pullNumber,
      per_page: 100,
    }),
    github.paginate(github.rest.pulls.listReviewComments, {
      owner: context.repo.owner,
      repo: context.repo.repo,
      pull_number: pullNumber,
      per_page: 100,
    }),
  ]);
  const alreadyHandled = [...reviews, ...issueComments, ...reviewComments].some(
    (item) =>
      item.user?.login === "github-actions[bot]" &&
      item.body?.startsWith(`${marker}\n`),
  );
  if (alreadyHandled) {
    core.notice(`ReviewBot command ${commentId} was already published.`);
    if (successful) await addReaction("+1");
    return;
  }

  if (successful && (command === "review" || command === "recheck")) {
    await github.rest.pulls.createReview({
      owner: context.repo.owner,
      repo: context.repo.repo,
      pull_number: pullNumber,
      commit_id: headSha,
      body,
      event: "COMMENT",
    });
  } else if (sourceKind === "review_comment") {
    await github.request(
      "POST /repos/{owner}/{repo}/pulls/{pull_number}/comments/{comment_id}/replies",
      {
        owner: context.repo.owner,
        repo: context.repo.repo,
        pull_number: pullNumber,
        comment_id: Number(replyToCommentId),
        body,
      },
    );
  } else {
    await github.rest.issues.createComment({
      owner: context.repo.owner,
      repo: context.repo.repo,
      issue_number: pullNumber,
      body,
    });
  }
  if (successful) await addReaction("+1");
}

module.exports = {
  validatePrEvent,
  reviewGate,
  prepareCommand,
  commandAnalyzeGate,
  publishReview,
  publishCommand,
};
