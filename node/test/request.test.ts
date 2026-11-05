import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { after, test } from "node:test";
import { Attr } from "../src/attributes.js";
import { VERSION } from "../src/version.js";
import { build } from "./fixtures.js";
import { startServer, TOKEN } from "./helpers.js";

const allow = { status: 200, body: { allowed: true, reason: 0 } };

test("sends the Speedle+ JsonContext with bearer auth, JSON and the User-Agent", async () => {
  const server = await startServer([allow]);
  after(() => server.close());
  const { client } = build(server.url, { userAgentSuffix: "app/1.2" });
  assert.equal(
    await client.isAllowed("alice", "get", "/books/1", [Attr.string("team", "eng"), Attr.numeric("n", [1, 2])]),
    true,
  );
  const [req] = server.requests;
  assert.ok(req);
  assert.equal(req.method, "POST");
  assert.equal(req.path, "/authz-check/v1/is-allowed");
  assert.equal(req.headers["content-type"], "application/json");
  assert.equal(req.headers.authorization, undefined);
  assert.match(
    req.headers["user-agent"] ?? "",
    new RegExp(`^speedle-sdk-node/${VERSION.replace(/\./g, "\\.")} \\(node/\\d+\\.\\d+\\.\\d+; [a-z0-9]+\\) app/1\\.2$`),
  );
  assert.equal(
    req.body,
    JSON.stringify({
      subject: { principals: [{ type: "user", name: "alice" }] },
      serviceName: "books",
      resource: "/books/1",
      action: "get",
      attributes: [
        { name: "team", type: "string", value: "eng" },
        { name: "n", type: "numeric", value: [1, 2] },
      ],
    }),
  );
  await client.close();
});

test("principal lists, per-call serviceName and no attributes key when empty", async () => {
  const server = await startServer([allow]);
  after(() => server.close());
  const { client } = build(server.url);
  await client.check([{ name: "alice", idd: "corp" }, { type: "group", name: "eng" }], "get", "/r", {}, {
    serviceName: "other",
  });
  assert.deepEqual(JSON.parse(server.requests[0]?.body ?? ""), {
    subject: {
      principals: [
        { type: "user", name: "alice", idd: "corp" },
        { type: "group", name: "eng" },
      ],
    },
    serviceName: "other",
    resource: "/r",
    action: "get",
  });
  await client.close();
});

test("bearer token goes in Authorization over HTTPS, and connections are reused", async () => {
  const server = await startServer([allow, allow], { tls: true });
  after(() => server.close());
  const { client } = build(server.url);
  await client.isAllowed("alice", "get", "/r");
  await client.isAllowed("alice", "get", "/r");
  assert.equal(server.requests[0]?.headers.authorization, `Bearer ${TOKEN}`);
  assert.equal(server.requests[1]?.headers.connection, "keep-alive");
  await client.close();
});

test("check returns the decision fields", async () => {
  const server = await startServer([
    { status: 200, headers: { "X-Quota-Daily-Remaining": "42" }, body: { allowed: false, reason: 4, errorMessage: "boom", extra: 1 } },
    { status: 200, body: { allowed: true, reason: 9 } },
  ]);
  after(() => server.close());
  const { client } = build(server.url);
  assert.deepEqual(await client.check("alice", "get", "/r"), {
    allowed: false,
    reason: 4,
    errorMessage: "boom",
    quotaDailyRemaining: 42,
  });
  assert.deepEqual(await client.check("alice", "get", "/r"), { allowed: true, reason: 9 });
  await client.close();
});

test("VERSION matches package.json", () => {
  const pkg = JSON.parse(readFileSync(new URL("../../package.json", import.meta.url), "utf8")) as { version: string };
  assert.equal(VERSION, pkg.version);
});
