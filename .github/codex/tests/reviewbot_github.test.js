// Local tests for ReviewBot authorization and publishing with mocked GitHub APIs.
"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");

const {
  validatePrEvent,
  reviewGate,
  prepareCommand,
  commandAnalyzeGate,
  publishReview,
} = require("../reviewbot_github.js");

const OWNER = "teramoby";
const REPO = "speedle-sdk";
const REPOSITORY = `${OWNER}/${REPO}`;
const API_URL = "https://api.github.com";
const PULL_URL = `${API_URL}/repos/${REPOSITORY}/pulls/18`;
const HEAD_SHA = "a".repeat(40);
const BASE_SHA = "b".repeat(40);
const OTHER_SHA = "c".repeat(40);

function coreDouble() {
  return {
    outputs: {},
    warnings: [],
    notices: [],
    failures: [],
    setOutput(name, value) { this.outputs[name] = value; },
    warning(message) { this.warnings.push(message); },
    notice(message) { this.notices.push(message); },
    setFailed(message) { this.failures.push(message); },
  };
}

function commandFixture(kind = "issue") {
  const issueRouter = kind === "issue";
  return {
    run: {
      event: issueRouter ? "issue_comment" : "pull_request_review_comment",
      name: issueRouter ? "ReviewBot Comment Command" : "ReviewBot Inline Command",
      path: issueRouter
        ? ".github/workflows/reviewbot-comment.yml"
        : ".github/workflows/reviewbot-inline.yml",
      conclusion: "success",
      actor: { type: "User", id: 7, login: "alice" },
      head_repository: { full_name: REPOSITORY },
      display_title: `ReviewBot ${issueRouter ? "comment" : "inline comment"} 77`,
      created_at: "2026-11-05T00:01:00Z",
      head_sha: HEAD_SHA,
      pull_requests: issueRouter ? [] : [{ number: 18, url: PULL_URL }],
    },
    comment: {
      id: 77,
      user: { id: 7, login: "alice", type: "User" },
      created_at: "2026-11-05T00:00:30Z",
      ...(issueRouter
        ? { issue_url: `${API_URL}/repos/${REPOSITORY}/issues/18` }
        : { pull_request_url: PULL_URL }),
      body: "@reviewbot review",
      html_url: `https://github.com/${REPOSITORY}/pull/18#issuecomment-77`,
    },
    issue: { number: 18, pull_request: { url: PULL_URL } },
    pull: {
      number: 18,
      state: "open",
      head: { sha: HEAD_SHA, repo: { full_name: REPOSITORY } },
      base: { sha: BASE_SHA },
    },
    associated: [{ number: 18, url: PULL_URL }],
  };
}

function commandContext(fixture) {
  return {
    eventName: "workflow_run",
    repo: { owner: OWNER, repo: REPO },
    apiUrl: API_URL,
    serverUrl: "https://github.com",
    runId: 900,
    payload: { workflow_run: fixture.run },
  };
}

function githubDouble(fixture, { permission = "write" } = {}) {
  const calls = {
    collaborator: [],
    pullGet: [],
    associated: [],
    createReview: [],
    createComment: [],
    request: [],
  };
  const reviews = [];
  const github = {
    rest: {
      issues: {
        getComment: async () => ({ data: fixture.comment }),
        get: async () => ({ data: fixture.issue }),
        listComments: async () => [],
        createComment: async (parameters) => {
          calls.createComment.push(parameters);
          return { data: { id: 1 } };
        },
      },
      pulls: {
        getReviewComment: async () => ({ data: fixture.comment }),
        get: async (parameters) => {
          calls.pullGet.push(parameters);
          return { data: fixture.pull };
        },
        listReviews: async () => reviews,
        listReviewComments: async () => [],
        createReview: async (parameters) => {
          calls.createReview.push(parameters);
          const review = {
            id: reviews.length + 1,
            user: { login: "github-actions[bot]" },
            commit_id: parameters.commit_id,
            body: parameters.body,
          };
          reviews.push(review);
          return { data: review };
        },
      },
      repos: {
        getCollaboratorPermissionLevel: async (parameters) => {
          calls.collaborator.push(parameters);
          return { data: { permission } };
        },
        listPullRequestsAssociatedWithCommit: async (parameters) => {
          calls.associated.push(parameters);
          return fixture.associated;
        },
      },
    },
    paginate: async (endpoint, parameters) => endpoint(parameters),
    request: async (route, parameters) => {
      calls.request.push({ route, parameters });
      return { data: {} };
    },
  };
  return { github, calls, reviews };
}

function setEnvironment(t, values) {
  const previous = new Map();
  for (const [name, value] of Object.entries(values)) {
    previous.set(name, process.env[name]);
    process.env[name] = value;
  }
  t.after(() => {
    for (const [name, value] of previous) {
      if (value === undefined) delete process.env[name];
      else process.env[name] = value;
    }
  });
}

function publishingEnvironment(t, reportOverrides = {}) {
  const temporary = fs.mkdtempSync(path.join(os.tmpdir(), "reviewbot-github-test-"));
  t.after(() => fs.rmSync(temporary, { recursive: true, force: true }));
  setEnvironment(t, {
    RUNNER_TEMP: temporary,
    GITHUB_RUN_ID: "900",
    GITHUB_RUN_ATTEMPT: "2",
    PULL_NUMBER: "18",
    HEAD_SHA,
    COMMAND: "review",
    ANALYZE_RESULT: "success",
    REVIEWBOT_ENABLED: "true",
    SHOULD_REVIEW: "true",
    REVIEWED_COMMIT: HEAD_SHA,
  });
  const directory = path.join(temporary, "reviewbot-handoff");
  fs.mkdirSync(directory);
  const report = {
    version: 1,
    message: "## ReviewBot review\n\nNo findings.",
    model: "kimi-k3.1-pro",
    run_id: "900",
    run_attempt: "2",
    pull_number: "18",
    head_sha: HEAD_SHA,
    command: "review",
    ...reportOverrides,
  };
  fs.writeFileSync(path.join(directory, "report.json"), JSON.stringify(report), { mode: 0o600 });
}

test("prepareCommand accepts an authorized collaborator command", async () => {
  const fixture = commandFixture();
  const { github, calls } = githubDouble(fixture);
  const core = coreDouble();
  await prepareCommand({ github, context: commandContext(fixture), core });
  assert.equal(core.outputs.should_run, "true");
  assert.equal(core.outputs.actor, "alice");
  assert.equal(core.outputs.command, "review");
  assert.equal(core.outputs.head_sha, HEAD_SHA);
  assert.deepEqual(calls.collaborator.map(call => call.username), ["alice"]);
});

test("prepareCommand validates routers, actors, timing and pull request targets", async (t) => {
  const cases = [
    ["an invalid router", (fixture) => { fixture.run.name = "Untrusted Router"; }],
    ["an actor mismatch", (fixture) => { fixture.comment.user.id = 999; }],
    ["a stale comment", (fixture) => { fixture.comment.created_at = "2026-11-04T23:58:00Z"; }],
    ["a cross-repository target", (fixture) => {
      fixture.comment.issue_url = `${API_URL}/repos/another/repository/issues/18`;
    }],
    ["an issue without a pull request", (fixture) => { delete fixture.issue.pull_request; }],
    ["a fork pull request", (fixture) => { fixture.pull.head.repo.full_name = "another/fork"; }],
  ];
  for (const [name, mutate] of cases) {
    await t.test(`rejects ${name}`, async () => {
      const fixture = commandFixture();
      mutate(fixture);
      const { github, calls } = githubDouble(fixture);
      const core = coreDouble();
      await prepareCommand({ github, context: commandContext(fixture), core });
      assert.equal(core.outputs.should_run, "false");
      assert.equal(calls.createReview.length, 0);
      assert.equal(calls.createComment.length, 0);
    });
  }
});

test("prepareCommand rejects insufficient collaborator permission before reading the PR", async () => {
  const fixture = commandFixture();
  const { github, calls } = githubDouble(fixture, { permission: "read" });
  const core = coreDouble();
  await prepareCommand({ github, context: commandContext(fixture), core });
  assert.equal(core.outputs.should_run, "false");
  assert.equal(calls.collaborator.length, 1);
  assert.equal(calls.pullGet.length, 0);
});

test("prepareCommand verifies inline comment pull request association", async (t) => {
  await t.test("rejects a different associated pull request", async () => {
    const fixture = commandFixture("inline");
    fixture.run.pull_requests = [];
    fixture.associated = [{ number: 19, url: `${API_URL}/repos/${REPOSITORY}/pulls/19` }];
    const { github, calls } = githubDouble(fixture);
    const core = coreDouble();
    await prepareCommand({ github, context: commandContext(fixture), core });
    assert.equal(core.outputs.should_run, "false");
    assert.equal(calls.associated.length, 1);
    assert.equal(calls.collaborator.length, 0);
  });

  await t.test("accepts the associated pull request", async () => {
    const fixture = commandFixture("inline");
    const { github } = githubDouble(fixture);
    const core = coreDouble();
    await prepareCommand({ github, context: commandContext(fixture), core });
    assert.equal(core.outputs.should_run, "true");
    assert.equal(core.outputs.source_kind, "review_comment");
  });
});

test("validatePrEvent rejects a stale pull request head", async () => {
  const fixture = commandFixture();
  fixture.pull.head.sha = OTHER_SHA;
  const context = {
    repo: { owner: OWNER, repo: REPO },
    payload: {
      workflow_run: {
        event: "pull_request",
        conclusion: "success",
        path: ".github/workflows/reviewbot.yml",
        name: "ReviewBot PR Event",
        head_repository: { full_name: REPOSITORY },
        head_sha: HEAD_SHA,
        pull_requests: [{ number: 18 }],
      },
    },
  };
  const github = { rest: { pulls: { get: async () => ({ data: fixture.pull }) } } };
  const core = coreDouble();
  await validatePrEvent({ github, context, core });
  assert.equal(core.outputs.should_run, "false");
  assert.equal(core.outputs.head_sha, undefined);
});

test("publishReview creates only one review for a head", async (t) => {
  publishingEnvironment(t);
  const fixture = commandFixture();
  const { github, calls } = githubDouble(fixture);
  const core = coreDouble();
  const context = commandContext(fixture);
  await publishReview({ github, context, core });
  await publishReview({ github, context, core });
  assert.equal(calls.createReview.length, 1);
  assert.equal(calls.createReview[0].commit_id, HEAD_SHA);
  assert.match(calls.createReview[0].body, /^<!-- speedle-plus-reviewbot:/);
  assert.equal(core.notices.length, 1);
});

test("publishReview does not publish after failed analysis", async (t) => {
  publishingEnvironment(t);
  process.env.ANALYZE_RESULT = "failure";
  const fixture = commandFixture();
  const { github, calls } = githubDouble(fixture);
  const core = coreDouble();
  await publishReview({ github, context: commandContext(fixture), core });
  assert.equal(core.failures.length, 1);
  assert.equal(calls.createReview.length, 0);
  assert.equal(calls.createComment.length, 0);
});

test("publishReview rejects a handoff bound to a stale head", async (t) => {
  publishingEnvironment(t, { head_sha: OTHER_SHA });
  const fixture = commandFixture();
  const { github, calls } = githubDouble(fixture);
  const core = coreDouble();
  await assert.rejects(
    publishReview({ github, context: commandContext(fixture), core }),
    /bound to a different review/,
  );
  assert.equal(calls.createReview.length, 0);
  assert.equal(calls.createComment.length, 0);
});

function providerEnvironment(t, provider, arkKey, deepseekKey) {
  setEnvironment(t, {
    REVIEWBOT_PROVIDER: provider,
    REVIEWBOT_API_KEY: arkKey,
    REVIEWBOT_DEEPSEEK_API_KEY: deepseekKey,
    REVIEWED_COMMIT: HEAD_SHA,
    PULL_NUMBER: "18",
    COMMAND: "review",
  });
}

test("reviewGate defaults to Ark and uses the Ark secret", async (t) => {
  providerEnvironment(t, "", "ark-test", "deepseek-test");
  const fixture = commandFixture();
  const { github } = githubDouble(fixture);
  const core = coreDouble();
  await reviewGate({ github, context: commandContext(fixture), core });
  assert.equal(core.outputs.enabled, "true");
  assert.equal(core.outputs.should_review, "true");
});

test("reviewGate uses only the selected provider secret", async (t) => {
  providerEnvironment(t, "deepseek", "", "deepseek-test");
  const fixture = commandFixture();
  const { github } = githubDouble(fixture);
  const core = coreDouble();
  await reviewGate({ github, context: commandContext(fixture), core });
  assert.equal(core.outputs.enabled, "true");
  assert.equal(core.outputs.should_review, "true");
});

test("reviewGate disables DeepSeek without the DeepSeek secret", async (t) => {
  providerEnvironment(t, "deepseek", "ark-test", "");
  const fixture = commandFixture();
  const { github } = githubDouble(fixture);
  const core = coreDouble();
  await reviewGate({ github, context: commandContext(fixture), core });
  assert.equal(core.outputs.enabled, "false");
  assert.equal(core.outputs.should_review, "false");
  assert.match(core.notices[0], /DEEPSEEK_API_KEY/);
});

test("reviewGate rejects an unknown provider", async (t) => {
  providerEnvironment(t, "other", "ark-test", "deepseek-test");
  const fixture = commandFixture();
  const { github } = githubDouble(fixture);
  const core = coreDouble();
  await reviewGate({ github, context: commandContext(fixture), core });
  assert.equal(core.outputs.enabled, "false");
  assert.equal(core.outputs.should_review, "false");
  assert.match(core.notices[0], /REVIEWBOT_PROVIDER/);
});

test("commandAnalyzeGate honors the selected provider", async (t) => {
  providerEnvironment(t, "deepseek", "", "deepseek-test");
  const fixture = commandFixture();
  const { github } = githubDouble(fixture);
  const core = coreDouble();
  await commandAnalyzeGate({ github, context: commandContext(fixture), core });
  assert.equal(core.outputs.enabled, "true");
  assert.equal(core.outputs.history_b64, "");
});
