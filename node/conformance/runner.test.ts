// Conformance runner: runs every vector in conformance/vectors (design §11).
import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import { after, test } from "node:test";
import { type Attribute, Attr } from "../src/attributes.js";
import { Auth } from "../src/auth.js";
import { createClientForTesting } from "../src/client.js";
import { SpeedleError } from "../src/errors.js";
import { VirtualClock } from "../test/helpers.js";
import { GoMock } from "./go-mock.js";
import type { Vector, VectorCall, VectorExpect } from "./vector.js";

const VECTORS = new URL("../../../conformance/vectors/", import.meta.url);

function toAttribute(a: { name: string; type: string; value: unknown }): Attribute {
  switch (a.type) {
    case "string":
      return Attr.string(a.name, a.value as string | string[]);
    case "numeric":
      return Attr.numeric(a.name, a.value as number | number[]);
    case "bool":
      return Attr.bool(a.name, a.value as boolean | boolean[]);
    case "datetime":
      return Attr.datetime(
        a.name,
        Array.isArray(a.value) ? a.value.map((v: string) => new Date(v)) : new Date(a.value as string),
      );
    default:
      throw new Error(`unknown attribute type ${a.type}`);
  }
}

/** A present field must match; null means the SDK reports no value. */
function fieldsMatch(where: string, expected: Record<string, unknown>, actual: Record<string, unknown>): void {
  for (const [key, want] of Object.entries(expected)) {
    const got = actual[key];
    assert.deepEqual(got === undefined ? null : got, want, `${where}.${key}`);
  }
}

async function runCall(
  client: ReturnType<typeof createClientForTesting>,
  clock: VirtualClock,
  call: VectorCall,
  expect: VectorExpect,
  where: string,
): Promise<void> {
  clock.sleeps.length = 0;
  const attributes = call.inferredAttributes ?? call.attributes?.map(toAttribute);
  const options = call.serviceName === undefined ? undefined : { serviceName: call.serviceName };
  const invoke = (): Promise<unknown> =>
    call.method === "check"
      ? client.check(call.subject, call.action, call.resource, attributes as never, options)
      : client.isAllowed(call.subject, call.action, call.resource, attributes as never, options);
  let value: unknown;
  let error: unknown;
  try {
    value = await invoke();
  } catch (e) {
    error = e;
  }
  if (expect.error) {
    assert.ok(error instanceof SpeedleError, `${where}: expected ${expect.error.type}, got ${String(error ?? value)}`);
    assert.equal(error.name, expect.error.type, `${where}: error type (${error.message})`);
    const { type: _type, retryAfterSeconds, ...rest } = expect.error;
    void _type;
    const actual: Record<string, unknown> = {
      status: error.status,
      code: error.code,
      quotaDailyRemaining: error.quotaDailyRemaining,
      retryAfterSeconds: (error as { retryAfter?: number }).retryAfter,
    };
    fieldsMatch(`${where}.error`, retryAfterSeconds === undefined ? rest : { ...rest, retryAfterSeconds }, actual);
  } else {
    assert.equal(error, undefined, `${where}: unexpected error ${String(error)}`);
    if (expect.result !== undefined) assert.equal(value, expect.result, `${where}: result`);
    if (expect.decision) fieldsMatch(`${where}.decision`, expect.decision, value as Record<string, unknown>);
  }
  if (expect.delaysMs) assert.deepEqual(clock.sleeps, expect.delaysMs, `${where}: delaysMs`);
}

const mock = new GoMock();
after(() => mock.close());

const files = readdirSync(VECTORS)
  .filter((f) => f.endsWith(".json"))
  .sort();
assert.ok(files.length > 0, "no conformance vectors found");

for (const file of files) {
  const vector = JSON.parse(readFileSync(new URL(file, VECTORS), "utf8")) as Vector;
  test(vector.id, async () => {
    assert.equal(`${vector.id}.json`, file, "id equals the file name");
    const { baseUrl, caPem } = await mock.load(vector);
    const clock = new VirtualClock();
    const events: Record<string, unknown>[] = [];
    const cfg = vector.config;
    const client = createClientForTesting(
      {
        backend: vector.backend,
        endpoint: baseUrl,
        caCert: caPem,
        auth: cfg.auth.type === "bearer" ? Auth.bearer(cfg.auth.token) : Auth.none(),
        serviceName: cfg.serviceName,
        retry: cfg.retry,
        failureMode: cfg.failureMode,
        circuitBreaker: cfg.circuitBreaker,
        hooks: {
          onAttempt: (e) => events.push({ type: "attempt", attempt: e.attempt, status: e.status, errorType: e.errorType }),
          onRetry: (e) => events.push({ type: "retry", attempt: e.attempt, delayMs: e.delayMs, errorType: e.errorType }),
          onGiveUp: (e) =>
            events.push({ type: "giveUp", attempts: e.attempts, reason: e.reason, errorType: e.error.name }),
          onBreakerStateChange: (e) => events.push({ type: "breaker", from: e.from, to: e.to }),
        },
      },
      clock,
      () => vector.jitterFraction ?? 1,
    );
    try {
      const steps = vector.steps ?? [{ call: vector.call as VectorCall, expect: vector.expect as VectorExpect }];
      for (const [i, step] of steps.entries()) {
        clock.advance((step as { advanceMs?: number }).advanceMs ?? 0);
        await runCall(client, clock, step.call, step.expect, vector.steps ? `steps[${i}]` : "call");
      }
      assert.deepEqual(await mock.verify(), [], "mock exchanges");
      if (vector.events) {
        assert.equal(events.length, vector.events.length, `events: got ${JSON.stringify(events)}`);
        vector.events.forEach((want, i) => fieldsMatch(`events[${i}]`, want, events[i] ?? {}));
      }
    } finally {
      await client.close();
    }
  });
}
