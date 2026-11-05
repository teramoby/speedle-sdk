import assert from "node:assert/strict";
import { after, test } from "node:test";
import { inspect } from "node:util";
import { Auth } from "../src/auth.js";
import { SpeedleClient } from "../src/client.js";
import * as E from "../src/errors.js";
import type { ClientOptions } from "../src/options.js";
import { build } from "./fixtures.js";
import { startServer, testCerts, TOKEN } from "./helpers.js";

const base: ClientOptions = {
  backend: "speedle-cloud",
  endpoint: "https://cloud.speedle.io",
  auth: Auth.bearer(TOKEN),
  serviceName: "books",
};

const invalid: [string, Partial<ClientOptions> | (() => unknown)][] = [
  ["unknown backend", { backend: "x" as never }],
  ["empty serviceName", { serviceName: "" }],
  ["bad auth object", { auth: { kind: "bearer" } }],
  ["cloud without bearer", { auth: Auth.none() }],
  ["query string", { endpoint: "https://cloud.speedle.io/?a=1" }],
  ["fragment", { endpoint: "https://cloud.speedle.io/#x" }],
  ["credentials in URL", { endpoint: "https://u:p@cloud.speedle.io" }],
  ["ftp scheme", { endpoint: "ftp://cloud.speedle.io" }],
  ["http with bearer", { backend: "speedle-plus", endpoint: "http://127.0.0.1:6734" }],
  ["http to a remote host", { backend: "speedle-plus", auth: Auth.none(), endpoint: "http://ads.internal:6734" }],
  ["maxAttempts 0", { retry: { maxAttempts: 0 } }],
  ["fractional maxAttempts", { retry: { maxAttempts: 1.5 } }],
  ["negative delay", { retry: { baseDelayMs: -1 } }],
  ["zero deadline", { retry: { deadlineMs: 0 } }],
  ["failure mode open", { failureMode: "open" as never }],
  ["breaker threshold 0", { circuitBreaker: { failureThreshold: 0 } }],
  ["control character in suffix", { userAgentSuffix: "a\nb" }],
  ["empty bearer token", () => Auth.bearer("")],
  ["token with whitespace", () => Auth.bearer("spdl_a b")],
  ["token with control character", () => Auth.bearer("spdl_\u0007")],
  ["mutualTls without key", () => Auth.mutualTls({ cert: "pem", key: "" })],
];
for (const [name, value] of invalid) {
  test(`ConfigurationError: ${name}`, () => {
    assert.throws(() => (typeof value === "function" ? value() : new SpeedleClient({ ...base, ...value })), E.ConfigurationError);
  });
}

test("valid endpoints: path prefix, loopback http, allowInsecureHttp", async () => {
  const clients = [
    new SpeedleClient({ ...base, endpoint: "https://gw.example.com/speedle/" }),
    new SpeedleClient({ ...base, backend: "speedle-plus", auth: Auth.none(), endpoint: "http://localhost:6734" }),
    new SpeedleClient({ ...base, backend: "speedle-plus", auth: Auth.none(), endpoint: "http://[::1]:6734" }),
    new SpeedleClient({
      ...base,
      backend: "speedle-plus",
      auth: Auth.none(),
      endpoint: "http://ads.internal:6734",
      allowInsecureHttp: true,
    }),
  ];
  for (const c of clients) await c.close();
});

test("the endpoint path prefix is kept", async () => {
  const server = await startServer([{ status: 200, body: { allowed: true, reason: 0 } }]);
  after(() => server.close());
  const { client } = build(`${server.url}/prefix/`);
  await client.isAllowed("alice", "get", "/r");
  assert.equal(server.requests[0]?.path, "/prefix/authz-check/v1/is-allowed");
  await client.close();
});

test("the token never appears in toString, JSON or inspect", () => {
  const auth = Auth.bearer(TOKEN);
  for (const text of [String(auth), JSON.stringify(auth), inspect(auth), inspect(new SpeedleClient({ ...base, auth }))]) {
    assert.ok(!text.includes(TOKEN), text);
  }
});

test("call-time validation raises InvalidRequestError without a request", async () => {
  const server = await startServer([]);
  after(() => server.close());
  const { client } = build(server.url);
  for (const call of [
    () => client.isAllowed("", "get", "/r"),
    () => client.isAllowed([], "get", "/r"),
    () => client.isAllowed([{ name: "" }], "get", "/r"),
    () => client.isAllowed("alice", 1 as never, "/r"),
    () => client.isAllowed("alice", "get", "/r", undefined, { serviceName: "" }),
  ]) {
    await assert.rejects(call(), E.InvalidRequestError);
  }
  assert.equal(server.requests.length, 0);
  await client.close();
});

test("a closed client refuses calls", async () => {
  const { client } = build("http://127.0.0.1:1");
  await client.close();
  await assert.rejects(client.isAllowed("alice", "get", "/r"), /client is closed/);
});

test("mutual TLS presents the client certificate", async () => {
  const server = await startServer([{ status: 200, body: { allowed: true, reason: 0 } }], { tls: true, requestCert: true });
  after(() => server.close());
  const c = testCerts();
  const { client } = build(server.url, { backend: "speedle-plus", auth: Auth.mutualTls({ cert: c.clientCert, key: c.clientKey }) });
  assert.equal(await client.isAllowed("alice", "get", "/r"), true);
  assert.equal(server.requests[0]?.headers.authorization, undefined);
  await client.close();
});

test("a TLS failure is a NetworkError that is not retried", async () => {
  const server = await startServer([], { tls: true, requestCert: true });
  after(() => server.close());
  const { client, events } = build(server.url, { backend: "speedle-plus", auth: Auth.none() });
  await assert.rejects(client.isAllowed("alice", "get", "/r"), E.NetworkError);
  assert.equal(events.filter((e) => e.type === "attempt").length, 1);
  assert.equal(events.at(-1)?.reason, "non_retryable");
  await client.close();
  // Without the test CA the server certificate is not trusted.
  const untrusted = build(server.url, { backend: "speedle-plus", auth: Auth.none(), caCert: undefined });
  await assert.rejects(untrusted.client.isAllowed("alice", "get", "/r"), E.NetworkError);
  assert.equal(untrusted.events.at(-1)?.reason, "non_retryable");
  await untrusted.client.close();
});
