import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import * as http from "node:http";
import * as https from "node:https";
import type { AddressInfo, Socket } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { performance } from "node:perf_hooks";
import type { Clock } from "../src/clock.js";

/** Virtual clock of design §11: sleep returns at once and advances an offset. */
export class VirtualClock implements Clock {
  offset = 0;
  readonly sleeps: number[] = [];
  now(): number {
    return performance.now() + this.offset;
  }
  sleep(ms: number, signal?: AbortSignal): Promise<void> {
    if (signal?.aborted) return Promise.reject(signal.reason);
    this.sleeps.push(ms);
    this.offset += ms;
    return Promise.resolve();
  }
  advance(ms: number): void {
    this.offset += ms;
  }
}

export interface TestCerts {
  ca: string;
  serverCert: string;
  serverKey: string;
  clientCert: string;
  clientKey: string;
}

let certs: TestCerts | undefined;

/** A throwaway CA, a server certificate for 127.0.0.1 and a client certificate. */
export function testCerts(): TestCerts {
  if (certs) return certs;
  const dir = mkdtempSync(join(tmpdir(), "speedle-node-certs-"));
  const run = (...args: string[]): void => {
    execFileSync("openssl", args, { cwd: dir, stdio: "ignore" });
  };
  try {
    run("req", "-x509", "-newkey", "rsa:2048", "-nodes", "-keyout", "ca.key", "-out", "ca.crt", "-days", "2",
      "-subj", "/CN=speedle-sdk test CA", "-addext", "basicConstraints=critical,CA:TRUE",
      "-addext", "keyUsage=critical,keyCertSign,cRLSign");
    const leaf = (name: string, ext: string): void => {
      run("req", "-newkey", "rsa:2048", "-nodes", "-keyout", `${name}.key`, "-out", `${name}.csr`, "-subj", `/CN=${name}`);
      writeFileSync(join(dir, `${name}.ext`), ext);
      run("x509", "-req", "-in", `${name}.csr`, "-CA", "ca.crt", "-CAkey", "ca.key", "-CAcreateserial",
        "-out", `${name}.crt`, "-days", "2", "-extfile", `${name}.ext`);
    };
    leaf("server", "subjectAltName=IP:127.0.0.1,DNS:localhost\nextendedKeyUsage=serverAuth\n");
    leaf("client", "extendedKeyUsage=clientAuth\n");
    const read = (f: string): string => readFileSync(join(dir, f), "utf8");
    certs = {
      ca: read("ca.crt"),
      serverCert: read("server.crt"),
      serverKey: read("server.key"),
      clientCert: read("client.crt"),
      clientKey: read("client.key"),
    };
    return certs;
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
}

export interface RecordedRequest {
  method: string;
  path: string;
  headers: http.IncomingHttpHeaders;
  body: string;
}

export type Reply =
  | { status: number; headers?: Record<string, string>; body?: unknown; rawBody?: string }
  | { fault: "connection_reset" | "no_response" };

export interface TestServer {
  url: string;
  requests: RecordedRequest[];
  close(): Promise<void>;
}

/** Starts a loopback server that answers requests with the scripted replies, in order. */
export async function startServer(
  replies: Reply[] | ((req: RecordedRequest, index: number) => Reply),
  options: { tls?: boolean; requestCert?: boolean } = {},
): Promise<TestServer> {
  const requests: RecordedRequest[] = [];
  const sockets = new Set<Socket>();
  const handler = (req: http.IncomingMessage, res: http.ServerResponse): void => {
    const chunks: Buffer[] = [];
    req.on("data", (c: Buffer) => chunks.push(c));
    req.on("end", () => {
      const recorded: RecordedRequest = {
        method: req.method ?? "",
        path: req.url ?? "",
        headers: req.headers,
        body: Buffer.concat(chunks).toString("utf8"),
      };
      const index = requests.push(recorded) - 1;
      const reply = typeof replies === "function" ? replies(recorded, index) : replies[index];
      if (!reply) {
        res.writeHead(599).end();
        return;
      }
      if ("fault" in reply) {
        if (reply.fault === "connection_reset") req.socket.resetAndDestroy();
        return;
      }
      const payload = reply.rawBody ?? (reply.body === undefined ? "" : JSON.stringify(reply.body));
      res.writeHead(reply.status, { "Content-Type": "application/json", ...reply.headers });
      res.end(payload);
    });
  };
  let server: http.Server;
  if (options.tls) {
    const c = testCerts();
    server = https.createServer(
      { cert: c.serverCert, key: c.serverKey, ca: c.ca, requestCert: options.requestCert, rejectUnauthorized: options.requestCert },
      handler,
    );
  } else {
    server = http.createServer(handler);
  }
  server.on("connection", (s: Socket) => {
    sockets.add(s);
    s.on("close", () => sockets.delete(s));
  });
  server.on("secureConnection", (s: Socket) => {
    sockets.add(s);
    s.on("close", () => sockets.delete(s));
  });
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const { port } = server.address() as AddressInfo;
  return {
    url: `${options.tls ? "https" : "http"}://127.0.0.1:${port}`,
    requests,
    close: () =>
      new Promise<void>((resolve) => {
        for (const s of sockets) s.destroy();
        server.close(() => resolve());
      }),
  };
}

export const TOKEN = "spdl_test_token_value";
