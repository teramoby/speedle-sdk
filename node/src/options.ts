import * as http from "node:http";
import * as https from "node:https";
import { rootCertificates } from "node:tls";
import { type Auth, authSecret } from "./auth.js";
import type { BreakerState } from "./breaker.js";
import { ConfigurationError, type SpeedleError } from "./errors.js";

export type { BreakerState } from "./breaker.js";

export type Backend = "speedle-cloud" | "speedle";

export interface RetryPolicy {
  /** Attempts per call, counting the first; 1 disables retries. Default 3. */
  maxAttempts?: number;
  /** Backoff base. Default 100. */
  baseDelayMs?: number;
  /** Backoff cap. Default 2000. */
  maxDelayMs?: number;
  /** One attempt: connect, TLS, request and response. Default 2000. */
  attemptTimeoutMs?: number;
  /** The whole call, attempts and delays included. Default 5000. */
  deadlineMs?: number;
}

export type FailureMode = "error" | "closed";

export interface CircuitBreakerOptions {
  /** Consecutive failed calls that open the breaker. Default 5. */
  failureThreshold?: number;
  /** Time the breaker stays open before a half-open trial. Default 30000. */
  coolDownMs?: number;
}

/** An error class name from design §7, for example "ServiceUnavailableError". */
export type ErrorType = string;
export type GiveUpReason =
  | "non_retryable"
  | "attempts_exhausted"
  | "deadline"
  | "retry_after_exceeds_deadline"
  | "circuit_open";

export interface AttemptEvent {
  attempt: number;
  durationMs: number;
  status?: number;
  errorType?: ErrorType;
}
/** `attempt` is the number of the next attempt. */
export interface RetryEvent {
  attempt: number;
  delayMs: number;
  errorType: ErrorType;
}
export interface GiveUpEvent {
  attempts: number;
  reason: GiveUpReason;
  error: SpeedleError;
}
export interface BreakerStateChangeEvent {
  from: BreakerState;
  to: BreakerState;
}
/** Synchronous, cheap callbacks. Exceptions and return values are ignored. */
export interface Hooks {
  onAttempt?(event: AttemptEvent): void;
  onRetry?(event: RetryEvent): void;
  onGiveUp?(event: GiveUpEvent): void;
  onBreakerStateChange?(event: BreakerStateChangeEvent): void;
}

export interface ClientOptions {
  backend: Backend;
  /** Base URL, for example "https://cloud.speedle.io". */
  endpoint: string;
  auth: Auth;
  /** Required; on Speedle Cloud, the token's service. */
  serviceName: string;
  retry?: RetryPolicy;
  failureMode?: FailureMode;
  /** Absent means no breaker. */
  circuitBreaker?: CircuitBreakerOptions;
  hooks?: Hooks;
  /** Extra trust anchor (PEM) for self-hosted servers, added to the default roots. */
  caCert?: string | Buffer;
  /** Allows `http://` to non-loopback hosts for backend "speedle" with Auth.none(). Default false. */
  allowInsecureHttp?: boolean;
  userAgentSuffix?: string;
}

export interface CallOptions {
  /** Aborting rejects the call with `signal.reason`, in every failure mode. */
  signal?: AbortSignal;
  /** Overrides ClientOptions.serviceName for this call. */
  serviceName?: string;
}

export const IS_ALLOWED_PATH = "/authz-check/v1/is-allowed";

/** Validated, normalised options. Internal. */
export interface ResolvedOptions {
  backend: Backend;
  url: URL;
  agent: http.Agent;
  authorization: string | undefined;
  serviceName: string;
  maxAttempts: number;
  baseDelayMs: number;
  maxDelayMs: number;
  attemptTimeoutMs: number;
  deadlineMs: number;
  failureMode: FailureMode;
  breaker: { failureThreshold: number; coolDownMs: number } | undefined;
  hooks: Hooks;
  userAgentSuffix: string | undefined;
}

function fail(message: string): never {
  throw new ConfigurationError(message);
}

function number(value: unknown, name: string, fallback: number, min: number, integer = false): number {
  if (value === undefined) return fallback;
  if (typeof value !== "number" || !Number.isFinite(value) || value < min || (integer && !Number.isInteger(value))) {
    fail(`${name} must be ${integer ? "an integer" : "a finite number"} >= ${min}`);
  }
  return value;
}

function isLoopback(hostname: string): boolean {
  return hostname === "localhost" || hostname === "[::1]" || /^127\.\d{1,3}\.\d{1,3}\.\d{1,3}$/.test(hostname);
}

function isPem(value: unknown): value is string | Buffer {
  return (typeof value === "string" || Buffer.isBuffer(value)) && value.length > 0;
}

// eslint-disable-next-line no-control-regex
const CONTROL = /[\u0000-\u001f\u007f]/;

export function resolveOptions(options: ClientOptions): ResolvedOptions {
  if (options === null || typeof options !== "object") fail("options are required");
  const { backend, endpoint, auth, serviceName } = options;
  if (backend !== "speedle-cloud" && backend !== "speedle") {
    fail('backend must be "speedle-cloud" or "speedle"');
  }
  const secret = auth === null || typeof auth !== "object" ? undefined : authSecret(auth);
  if (!secret) fail("auth must come from Auth.none(), Auth.bearer() or Auth.mutualTls()");
  if (backend === "speedle-cloud" && auth.kind !== "bearer") fail("speedle-cloud needs Auth.bearer()");
  if (typeof serviceName !== "string" || serviceName === "") fail("serviceName is required");

  if (typeof endpoint !== "string" || endpoint === "") fail("endpoint is required");
  if (endpoint.includes("?") || endpoint.includes("#")) fail("endpoint must not have a query or fragment");
  let url: URL;
  try {
    url = new URL(endpoint);
  } catch {
    fail("endpoint is not a valid URL");
  }
  if (url.username || url.password) fail("endpoint must not contain credentials");
  if (url.protocol === "http:") {
    if (backend !== "speedle" || auth.kind !== "none") {
      fail('http:// is accepted only for backend "speedle" with Auth.none()');
    }
    if (!isLoopback(url.hostname) && options.allowInsecureHttp !== true) {
      fail("http:// is accepted only for loopback hosts unless allowInsecureHttp is set");
    }
  } else if (url.protocol !== "https:") {
    fail("endpoint must use https://");
  }
  url.pathname = url.pathname.replace(/\/+$/, "") + IS_ALLOWED_PATH;

  const retry = options.retry ?? {};
  if (typeof retry !== "object" || retry === null) fail("retry must be an object");
  const failureMode = options.failureMode ?? "error";
  if (failureMode !== "error" && failureMode !== "closed") fail('failureMode must be "error" or "closed"');

  let breaker: ResolvedOptions["breaker"];
  if (options.circuitBreaker !== undefined) {
    const cb = options.circuitBreaker;
    if (typeof cb !== "object" || cb === null) fail("circuitBreaker must be an object");
    breaker = {
      failureThreshold: number(cb.failureThreshold, "circuitBreaker.failureThreshold", 5, 1, true),
      coolDownMs: number(cb.coolDownMs, "circuitBreaker.coolDownMs", 30000, 1),
    };
  }

  const hooks = options.hooks ?? {};
  if (typeof hooks !== "object" || hooks === null) fail("hooks must be an object");

  const suffix = options.userAgentSuffix;
  if (suffix !== undefined && (typeof suffix !== "string" || CONTROL.test(suffix))) {
    fail("userAgentSuffix must be a string without control characters");
  }

  if (options.caCert !== undefined && !isPem(options.caCert)) fail("caCert must be a PEM string or Buffer");
  if (options.caCert !== undefined && url.protocol !== "https:") fail("caCert needs an https:// endpoint");

  let agent: http.Agent;
  if (url.protocol === "https:") {
    const tlsOptions: https.AgentOptions = { keepAlive: true };
    if (options.caCert !== undefined) {
      const extra = Buffer.isBuffer(options.caCert) ? options.caCert.toString("utf8") : options.caCert;
      tlsOptions.ca = [...rootCertificates, extra];
    }
    if (auth.kind === "mutual-tls") {
      tlsOptions.cert = secret.cert;
      tlsOptions.key = secret.key;
    }
    agent = new https.Agent(tlsOptions);
  } else {
    agent = new http.Agent({ keepAlive: true });
  }

  return {
    backend,
    url,
    agent,
    authorization: auth.kind === "bearer" ? `Bearer ${secret.token as string}` : undefined,
    serviceName,
    maxAttempts: number(retry.maxAttempts, "retry.maxAttempts", 3, 1, true),
    baseDelayMs: number(retry.baseDelayMs, "retry.baseDelayMs", 100, 0),
    maxDelayMs: number(retry.maxDelayMs, "retry.maxDelayMs", 2000, 0),
    attemptTimeoutMs: number(retry.attemptTimeoutMs, "retry.attemptTimeoutMs", 2000, 1),
    deadlineMs: number(retry.deadlineMs, "retry.deadlineMs", 5000, 1),
    failureMode,
    breaker,
    hooks,
    userAgentSuffix: suffix === "" ? undefined : suffix,
  };
}
