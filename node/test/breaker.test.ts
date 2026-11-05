import assert from "node:assert/strict";
import { after, test } from "node:test";
import * as E from "../src/errors.js";
import { build } from "./fixtures.js";
import { type Reply, startServer } from "./helpers.js";

const allow: Reply = { status: 200, body: { allowed: true, reason: 0 } };

async function rejection(p: Promise<unknown>): Promise<unknown> {
  return p.then(
    () => assert.fail("expected a rejection"),
    (e: unknown) => e,
  );
}

test("4xx resets the count; validation errors do not count", async () => {
  const server = await startServer([{ status: 500 }, { status: 403 }, { status: 500 }, { status: 500 }]);
  after(() => server.close());
  const { client, events } = build(server.url, { retry: { maxAttempts: 1 }, circuitBreaker: { failureThreshold: 2 } });
  await rejection(client.isAllowed("alice", "get", "/r"));
  await rejection(client.isAllowed("alice", "get", "/r"));
  await rejection(client.isAllowed("alice", "get", "/r", { bad: [] }));
  await rejection(client.isAllowed("alice", "get", "/r"));
  assert.ok(!events.some((e) => e.type === "breaker"));
  await rejection(client.isAllowed("alice", "get", "/r"));
  assert.deepEqual(events.at(-1), { type: "breaker", from: "closed", to: "open" });
  assert.ok((await rejection(client.isAllowed("alice", "get", "/r"))) instanceof E.CircuitOpenError);
  assert.equal(server.requests.length, 4);
  await client.close();
});

test("half-open lets exactly one trial through; a failed trial reopens", async () => {
  const server = await startServer((_req, index) => (index === 0 ? { status: 502 } : { status: 503 }));
  after(() => server.close());
  const built = build(server.url, {
    retry: { maxAttempts: 1 },
    circuitBreaker: { failureThreshold: 1, coolDownMs: 1000 },
  });
  const { client, clock } = built;
  await rejection(client.isAllowed("alice", "get", "/r"));
  clock.advance(999);
  assert.ok((await rejection(client.isAllowed("alice", "get", "/r"))) instanceof E.CircuitOpenError);
  clock.advance(1);
  const trial = rejection(client.isAllowed("alice", "get", "/r"));
  const concurrent = await rejection(client.isAllowed("alice", "get", "/r"));
  assert.ok(concurrent instanceof E.CircuitOpenError);
  assert.ok((await trial) instanceof E.ServiceUnavailableError);
  assert.deepEqual(
    built.events.filter((e) => e.type === "breaker").map((e) => `${String(e.from)}>${String(e.to)}`),
    ["closed>open", "open>half_open", "half_open>open"],
  );
  assert.equal(server.requests.length, 2);
  await client.close();
});

test("a cancelled half-open trial frees the slot for the next call", async () => {
  const server = await startServer([{ status: 503 }, allow]);
  after(() => server.close());
  const { client, clock } = build(server.url, {
    retry: { maxAttempts: 1 },
    circuitBreaker: { failureThreshold: 1, coolDownMs: 10 },
  });
  await rejection(client.isAllowed("alice", "get", "/r"));
  clock.advance(10);
  const reason = new Error("cancel");
  await rejection(client.isAllowed("alice", "get", "/r", undefined, { signal: AbortSignal.abort(reason) }));
  assert.equal(await client.isAllowed("alice", "get", "/r"), true);
  await client.close();
});

test("an allowed result never comes from an error path", async () => {
  const server = await startServer([allow]);
  after(() => server.close());
  const { client } = build(server.url, { failureMode: "closed", circuitBreaker: {} });
  assert.equal(await client.isAllowed("alice", "get", "/r"), true);
  assert.equal(await client.isAllowed("alice", "get", "/r"), false); // the server answers 599
  await client.close();
});
