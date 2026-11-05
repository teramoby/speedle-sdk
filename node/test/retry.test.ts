import assert from "node:assert/strict";
import { after, test } from "node:test";
import * as E from "../src/errors.js";
import { build } from "./fixtures.js";
import { type Reply, startServer } from "./helpers.js";

const allow: Reply = { status: 200, body: { allowed: true, reason: 0 } };
const r503: Reply = { status: 503, body: { error: "snapshot_unavailable" } };

async function rejection(p: Promise<unknown>): Promise<unknown> {
  return p.then(
    () => assert.fail("expected a rejection"),
    (e: unknown) => e,
  );
}

test("503, 502, 504 are retried with exponential full-jitter backoff", async () => {
  const server = await startServer([r503, { status: 502 }, { status: 504 }, allow]);
  after(() => server.close());
  const { client, clock, events } = build(server.url, { retry: { maxAttempts: 4 } }, 0.5);
  assert.equal(await client.isAllowed("alice", "get", "/r"), true);
  assert.deepEqual(clock.sleeps, [50, 100, 200]);
  assert.deepEqual(
    events.filter((e) => e.type === "retry").map((e) => [e.attempt, e.delayMs, e.errorType]),
    [
      [2, 50, "ServiceUnavailableError"],
      [3, 100, "ServerError"],
      [4, 200, "ServerError"],
    ],
  );
  await client.close();
});

test("backoff is capped by maxDelayMs and attempts are exhausted", async () => {
  const server = await startServer([r503, r503, r503]);
  after(() => server.close());
  const { client, clock, events } = build(server.url, { retry: { baseDelayMs: 300, maxDelayMs: 400 } });
  const err = await rejection(client.check("alice", "get", "/r"));
  assert.ok(err instanceof E.ServiceUnavailableError);
  assert.deepEqual(clock.sleeps, [300, 400]);
  assert.deepEqual(events.at(-1), { type: "giveUp", attempts: 3, reason: "attempts_exhausted", errorType: "ServiceUnavailableError" });
  await client.close();
});

test("500 is not retried", async () => {
  const server = await startServer([{ status: 500 }, allow]);
  after(() => server.close());
  const { client } = build(server.url);
  assert.ok((await rejection(client.isAllowed("alice", "get", "/r"))) instanceof E.ServerError);
  assert.equal(server.requests.length, 1);
  await client.close();
});

test("Retry-After raises the delay on a retryable status", async () => {
  const server = await startServer([{ status: 503, headers: { "Retry-After": "2" } }, allow]);
  after(() => server.close());
  const { client, clock } = build(server.url);
  assert.equal(await client.isAllowed("alice", "get", "/r"), true);
  assert.deepEqual(clock.sleeps, [2000]);
  await client.close();
});

test("a burst 429 whose Retry-After does not fit the deadline is raised at once", async () => {
  const server = await startServer([{ status: 429, headers: { "Retry-After": "6" }, body: { error: "x" } }]);
  after(() => server.close());
  const { client, events } = build(server.url);
  const err = await rejection(client.isAllowed("alice", "get", "/r"));
  assert.ok(err instanceof E.RateLimitedError);
  assert.equal(err.retryAfter, 6);
  assert.equal(events.at(-1)?.reason, "retry_after_exceeds_deadline");
  await client.close();
});

test("connection reset is retried; attempt timeout raises TimeoutError", async () => {
  const server = await startServer([{ fault: "connection_reset" }, { fault: "no_response" }, { fault: "no_response" }]);
  after(() => server.close());
  const { client, events } = build(server.url, { retry: { attemptTimeoutMs: 50 } });
  const err = await rejection(client.isAllowed("alice", "get", "/r"));
  assert.ok(err instanceof E.TimeoutError && !(err instanceof E.DeadlineExceededError), String(err));
  assert.deepEqual(
    events.filter((e) => e.type === "attempt").map((e) => e.errorType),
    ["NetworkError", "TimeoutError", "TimeoutError"],
  );
  assert.equal(events.at(-1)?.reason, "attempts_exhausted");
  await client.close();
});

test("the deadline cuts an attempt with DeadlineExceededError", async () => {
  const server = await startServer([{ fault: "no_response" }]);
  after(() => server.close());
  const { client, events } = build(server.url, { retry: { deadlineMs: 80 } });
  const started = Date.now();
  const err = await rejection(client.isAllowed("alice", "get", "/r"));
  assert.ok(err instanceof E.DeadlineExceededError);
  assert.equal(err.status, undefined);
  assert.ok(Date.now() - started < 1500);
  assert.equal(events.at(-1)?.reason, "deadline");
  await client.close();
});

test("abort during the retry delay rejects with signal.reason, also in closed mode, with no give-up", async () => {
  const server = await startServer([r503, allow]);
  after(() => server.close());
  const controller = new AbortController();
  const reason = new Error("caller gave up");
  const built = build(server.url, {
    failureMode: "closed",
    hooks: { onRetry: () => controller.abort(reason), onGiveUp: () => assert.fail("no give-up on cancel") },
  });
  assert.equal(await rejection(built.client.isAllowed("alice", "get", "/r", undefined, { signal: controller.signal })), reason);
  assert.equal(server.requests.length, 1);
  await built.client.close();
});

test("abort during an attempt and an already aborted signal", async () => {
  const server = await startServer([{ fault: "no_response" }]);
  after(() => server.close());
  const { client } = build(server.url);
  const reason = new Error("stop");
  const signal = AbortSignal.abort(reason);
  assert.equal(await rejection(client.isAllowed("alice", "get", "/r", undefined, { signal })), reason);
  assert.equal(server.requests.length, 0);
  const controller = new AbortController();
  setTimeout(() => controller.abort(reason), 30);
  assert.equal(await rejection(client.check("alice", "get", "/r", undefined, { signal: controller.signal })), reason);
  await client.close();
});

test("fail-closed: isAllowed returns false, check still raises, hooks still see the error", async () => {
  const server = await startServer([r503, r503, { status: 401 }]);
  after(() => server.close());
  const { client, events } = build(server.url, { failureMode: "closed", retry: { maxAttempts: 2 } });
  assert.equal(await client.isAllowed("alice", "get", "/r"), false);
  assert.equal(await client.isAllowed("alice", "get", "/r", { a: null } as never), false);
  assert.ok((await rejection(client.check("alice", "get", "/r"))) instanceof E.UnauthorizedError);
  assert.deepEqual(
    events.filter((e) => e.type === "giveUp").map((e) => [e.reason, e.errorType]),
    [
      ["attempts_exhausted", "ServiceUnavailableError"],
      ["non_retryable", "InvalidRequestError"],
      ["non_retryable", "UnauthorizedError"],
    ],
  );
  await client.close();
});

test("throwing or async hooks never change the result", async () => {
  const server = await startServer([r503, allow]);
  after(() => server.close());
  const boom = (): never => {
    throw new Error("hook");
  };
  const { client } = build(server.url, {
    hooks: { onAttempt: boom, onRetry: () => Promise.reject(new Error("async")) as never, onGiveUp: boom },
  });
  assert.equal(await client.isAllowed("alice", "get", "/r"), true);
  await client.close();
});
