import * as http from "node:http";
import * as https from "node:https";
import type { TLSSocket } from "node:tls";

export interface RawResponse {
  status: number;
  headers: http.IncomingHttpHeaders;
  body: Buffer;
}

/** Why an attempt produced no response. Internal. */
export type TransportFailureKind = "network" | "tls" | "timeout" | "aborted" | "too_large";

export class TransportFailure extends Error {
  constructor(
    readonly kind: TransportFailureKind,
    message: string,
    readonly original?: unknown,
  ) {
    super(message);
  }
}

/** Upper bound on a response body; a decision is a few dozen bytes. */
export const MAX_RESPONSE_BYTES = 1024 * 1024;

const TLS_ERROR_CODE = /^(ERR_TLS_|ERR_SSL_|CERT_|UNABLE_TO_|DEPTH_ZERO_|SELF_SIGNED_|HOSTNAME_MISMATCH|EPROTO$)/;

export interface SendOptions {
  url: URL;
  agent: http.Agent;
  headers: Record<string, string>;
  body: string;
  timeoutMs: number;
  signal?: AbortSignal;
}

/** Sends one request. Never follows redirects. */
export function send(options: SendOptions): Promise<RawResponse> {
  const { url, agent, headers, body, timeoutMs, signal } = options;
  const isTls = url.protocol === "https:";
  const request = isTls ? https.request : http.request;

  return new Promise<RawResponse>((resolve, reject) => {
    let settled = false;
    let failure: TransportFailure | undefined;
    // Set when a new TLS socket connected over TCP but did not finish the handshake.
    let handshakePending = false;

    const finish = (err: TransportFailure | undefined, res?: RawResponse): void => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      signal?.removeEventListener("abort", onAbort);
      if (err) reject(err);
      else resolve(res as RawResponse);
    };

    const req = request(url, {
      method: "POST",
      agent,
      headers: { ...headers, "Content-Length": String(Buffer.byteLength(body)) },
    });

    const abortWith = (f: TransportFailure): void => {
      failure ??= f;
      req.destroy(f);
      finish(f);
    };
    const onAbort = (): void => abortWith(new TransportFailure("aborted", "aborted", signal?.reason));
    const timer = setTimeout(
      () => abortWith(new TransportFailure("timeout", `no complete response within ${timeoutMs} ms`)),
      timeoutMs,
    );
    if (signal) {
      if (signal.aborted) {
        onAbort();
        return;
      }
      signal.addEventListener("abort", onAbort, { once: true });
    }

    req.on("socket", (socket) => {
      if (!isTls || !socket.connecting) return;
      const tls = socket as TLSSocket;
      tls.once("connect", () => {
        handshakePending = true;
      });
      tls.once("secureConnect", () => {
        handshakePending = false;
      });
    });

    req.on("error", (err: NodeJS.ErrnoException) => {
      if (failure) {
        finish(failure);
        return;
      }
      const code = typeof err.code === "string" ? err.code : "";
      const tls = isTls && (handshakePending || TLS_ERROR_CODE.test(code));
      finish(
        new TransportFailure(
          tls ? "tls" : "network",
          `${tls ? "TLS failure" : "connection failure"}${code ? ` (${code})` : ""}`,
          err,
        ),
      );
    });

    req.on("response", (res) => {
      const chunks: Buffer[] = [];
      let size = 0;
      res.on("data", (chunk: Buffer) => {
        size += chunk.length;
        if (size > MAX_RESPONSE_BYTES) {
          abortWith(new TransportFailure("too_large", "response body too large"));
          return;
        }
        chunks.push(chunk);
      });
      res.on("error", (err) => {
        if (!failure) finish(new TransportFailure("network", "connection closed before a full response", err));
      });
      res.on("aborted", () => {
        if (!failure) finish(new TransportFailure("network", "connection closed before a full response"));
      });
      res.on("end", () => {
        if (!res.complete) {
          finish(new TransportFailure("network", "connection closed before a full response"));
          return;
        }
        finish(undefined, { status: res.statusCode ?? 0, headers: res.headers, body: Buffer.concat(chunks) });
      });
    });

    req.end(body);
  });
}
