// Interim in-process conformance mock. It replays a vector's exchanges over
// HTTPS with a throwaway CA and checks every request, following
// conformance/README.md. It is replaced by the shared Go mock in
// conformance/mock once that is on main.
import * as https from "node:https";
import type { IncomingMessage, ServerResponse } from "node:http";
import type { AddressInfo, Socket } from "node:net";
import { isDeepStrictEqual } from "node:util";
import { testCerts } from "../test/helpers.js";
import type { Exchange, HeaderMatcher, Vector } from "./vector.js";

export interface MockServer {
  /** Loads a vector; returns the base URL and the CA (PEM) that signed the server certificate. */
  load(vector: Vector): Promise<{ baseUrl: string; caPem: string }>;
  /** Mismatches, plus missing or extra requests, after the vector ran. */
  verify(): string[];
  close(): Promise<void>;
}

function pointerGet(doc: unknown, pointer: string): unknown {
  if (pointer === "") return doc;
  let cur: unknown = doc;
  for (const raw of pointer.split("/").slice(1)) {
    const key = raw.replace(/~1/g, "/").replace(/~0/g, "~");
    if (cur === null || typeof cur !== "object") return undefined;
    cur = (cur as Record<string, unknown>)[key];
  }
  return cur;
}

function sortedCopy(doc: unknown, pointer: string): unknown {
  const target = pointerGet(doc, pointer);
  if (!Array.isArray(target)) return doc;
  const sorted = [...(target as unknown[])].sort((a, b) => (JSON.stringify(a) < JSON.stringify(b) ? -1 : 1));
  target.splice(0, target.length, ...sorted);
  return doc;
}

function bodyMatches(expected: unknown, actual: unknown, unordered: string[] = []): boolean {
  let e = structuredClone(expected);
  let a = structuredClone(actual);
  for (const p of unordered) {
    e = sortedCopy(e, p);
    a = sortedCopy(a, p);
  }
  return isDeepStrictEqual(e, a);
}

function headerProblem(name: string, matcher: HeaderMatcher, value: string | undefined): string | undefined {
  if (typeof matcher === "string") return value === matcher ? undefined : `header ${name}: ${String(value)}`;
  if ("absent" in matcher) return value === undefined ? undefined : `header ${name} must be absent`;
  return value !== undefined && new RegExp(`^(?:${matcher.pattern})$`).test(value)
    ? undefined
    : `header ${name}: ${String(value)} does not match ${matcher.pattern}`;
}

export class InProcessMock implements MockServer {
  private server: https.Server | undefined;
  private readonly sockets = new Set<Socket>();
  private exchanges: Exchange[] = [];
  private received = 0;
  private problems: string[] = [];

  async load(vector: Vector): Promise<{ baseUrl: string; caPem: string }> {
    this.exchanges = vector.exchanges;
    this.received = 0;
    this.problems = [];
    const certs = testCerts();
    if (!this.server) {
      this.server = https.createServer({ cert: certs.serverCert, key: certs.serverKey }, (req, res) =>
        this.handle(req, res),
      );
      this.server.on("secureConnection", (s: Socket) => {
        this.sockets.add(s);
        s.on("close", () => this.sockets.delete(s));
      });
      await new Promise<void>((resolve) => this.server?.listen(0, "127.0.0.1", resolve));
    }
    const { port } = this.server.address() as AddressInfo;
    return { baseUrl: `https://127.0.0.1:${port}`, caPem: certs.ca };
  }

  private handle(req: IncomingMessage, res: ServerResponse): void {
    const chunks: Buffer[] = [];
    req.on("data", (c: Buffer) => chunks.push(c));
    req.on("end", () => {
      const index = this.received++;
      const exchange = this.exchanges[index];
      if (!exchange) {
        this.problems.push(`unexpected request #${index + 1}`);
        res.writeHead(500).end();
        return;
      }
      const want = exchange.request;
      const tag = `request #${index + 1}`;
      if (req.method !== want.method) this.problems.push(`${tag}: method ${String(req.method)}`);
      if (req.url !== want.path) this.problems.push(`${tag}: path ${String(req.url)}`);
      for (const [name, matcher] of Object.entries(want.headers ?? {})) {
        const raw = req.headers[name.toLowerCase()];
        const problem = headerProblem(name, matcher, Array.isArray(raw) ? raw.join(", ") : raw);
        if (problem) this.problems.push(`${tag}: ${problem}`);
      }
      const text = Buffer.concat(chunks).toString("utf8");
      let body: unknown;
      try {
        body = JSON.parse(text);
      } catch {
        body = Symbol("invalid JSON");
      }
      if (!bodyMatches(want.body, body, want.unorderedArrays)) this.problems.push(`${tag}: body ${text}`);

      const reply = exchange.response;
      if ("fault" in reply) {
        // A TLS socket has no TCP handle to reset; closing it without a response has the same effect.
        if (reply.fault === "connection_reset") req.socket.destroy();
        return; // no_response: hold the connection open.
      }
      const headers: Record<string, string> = { ...reply.headers };
      let payload = "";
      if (reply.rawBody !== undefined) payload = reply.rawBody;
      else if (reply.body !== undefined) {
        payload = JSON.stringify(reply.body);
        headers["Content-Type"] ??= "application/json";
      }
      res.writeHead(reply.status, headers).end(payload);
    });
  }

  verify(): string[] {
    const out = [...this.problems];
    if (this.received < this.exchanges.length) {
      out.push(`expected ${this.exchanges.length} requests, received ${this.received}`);
    }
    // Drop held connections so the next vector starts clean.
    for (const s of this.sockets) s.destroy();
    return out;
  }

  close(): Promise<void> {
    for (const s of this.sockets) s.destroy();
    return new Promise<void>((resolve) => (this.server ? this.server.close(() => resolve()) : resolve()));
  }
}
