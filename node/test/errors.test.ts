import assert from "node:assert/strict";
import { after, test } from "node:test";
import * as E from "../src/errors.js";
import { build } from "./fixtures.js";
import { type Reply, startServer, TOKEN } from "./helpers.js";

type Case = [string, Reply, "speedle-cloud" | "speedle-plus", new (...a: never[]) => E.SpeedleError, Record<string, unknown>];

const cases: Case[] = [
  ["cloud 400", { status: 400, body: { error: "invalid_request" } }, "speedle-cloud", E.InvalidRequestError, { status: 400, code: "invalid_request" }],
  ["cloud 400 free text", { status: 400, body: { error: "bad attr" } }, "speedle-cloud", E.InvalidRequestError, { code: undefined, message: "HTTP 400: bad attr" }],
  ["plus 400", { status: 400, body: { error: "invalid_request" } }, "speedle-plus", E.InvalidRequestError, { code: undefined }],
  ["401", { status: 401, body: { error: "unauthorized" } }, "speedle-cloud", E.UnauthorizedError, { code: "unauthorized" }],
  ["403", { status: 403, headers: { "X-Quota-Daily-Remaining": "7" }, body: { error: "service_mismatch" } }, "speedle-cloud", E.ForbiddenError, { quotaDailyRemaining: 7 }],
  ["404", { status: 404, rawBody: "not found" }, "speedle-plus", E.HttpError, { status: 404 }],
  ["413", { status: 413, body: { error: "request_too_large" } }, "speedle-cloud", E.RequestTooLargeError, {}],
  ["415", { status: 415 }, "speedle-plus", E.HttpError, {}],
  ["302", { status: 302, headers: { Location: "https://example.com/" } }, "speedle-plus", E.HttpError, { status: 302 }],
  ["cloud 429 daily", { status: 429, headers: { "Retry-After": "3600" }, body: { error: "daily_quota_exceeded" } }, "speedle-cloud", E.QuotaExceededError, { retryAfter: 3600 }],
  ["cloud 429 unknown", { status: 429, headers: { "Retry-After": "5" }, body: { error: "other" } }, "speedle-cloud", E.TooManyRequestsError, { code: undefined }],
  ["cloud 429 burst no Retry-After", { status: 429, body: { error: "rate_limited" } }, "speedle-cloud", E.RateLimitedError, { retryAfter: undefined }],
  ["plus 429 bad Retry-After", { status: 429, headers: { "Retry-After": "0" }, body: { error: "slow down" } }, "speedle-plus", E.RateLimitedError, { retryAfter: undefined, code: undefined }],
  ["500", { status: 500, body: { error: "internal_error" } }, "speedle-cloud", E.ServerError, { code: "internal_error" }],
  ["501", { status: 501 }, "speedle-plus", E.ServerError, {}],
  ["200 non-JSON", { status: 200, rawBody: "<html>" }, "speedle-plus", E.UnexpectedResponseError, { status: 200 }],
  ["200 allowed string", { status: 200, body: { allowed: "true", reason: 0 } }, "speedle-plus", E.UnexpectedResponseError, {}],
  ["200 no reason", { status: 200, body: { allowed: true } }, "speedle-plus", E.UnexpectedResponseError, {}],
];

for (const [name, reply, backend, type, fields] of cases) {
  test(`${name} raises ${type.name} at once`, async () => {
    const server = await startServer([reply], { tls: true });
    after(() => server.close());
    const { client, events, clock } = build(server.url, { backend });
    const err = await client.isAllowed("alice", "get", "/r").then(
      () => assert.fail("expected an error"),
      (e: unknown) => e,
    );
    assert.ok(err instanceof type, `got ${String(err)}`);
    assert.equal((err as E.SpeedleError).name, type.name);
    for (const [k, v] of Object.entries(fields)) assert.deepEqual((err as unknown as Record<string, unknown>)[k], v, k);
    assert.equal(server.requests.length, 1);
    assert.deepEqual(clock.sleeps, []);
    assert.equal(events.at(-1)?.reason, "non_retryable");
    assert.ok(!(err as Error).message.includes(TOKEN));
    await client.close();
  });
}

test("error hierarchy", () => {
  assert.ok(new E.DeadlineExceededError("x") instanceof E.TimeoutError);
  assert.ok(new E.TimeoutError("x") instanceof E.NetworkError);
  assert.ok(new E.RateLimitedError("x") instanceof E.TooManyRequestsError);
  assert.ok(new E.QuotaExceededError("x") instanceof E.TooManyRequestsError);
  assert.ok(new E.CircuitOpenError("x") instanceof E.SpeedleError);
  assert.notEqual(E.TimeoutError, globalThis.Error);
});
