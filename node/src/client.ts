import { type Attributes, toWireAttributes } from "./attributes.js";
import { type BreakerOutcome, CircuitBreaker } from "./breaker.js";
import { type Clock, type RandomSource, systemClock, systemRandom } from "./clock.js";
import {
  CircuitOpenError,
  DeadlineExceededError,
  InvalidRequestError,
  NetworkError,
  SpeedleError,
  TimeoutError,
  UnexpectedResponseError,
} from "./errors.js";
import {
  type CallOptions,
  type ClientOptions,
  type GiveUpReason,
  type Hooks,
  type ResolvedOptions,
  resolveOptions,
} from "./options.js";
import { classify, type Decision } from "./response.js";
import { send, TransportFailure } from "./transport.js";
import { VERSION } from "./version.js";

/** A principal; `type` defaults to "user". */
export interface Principal {
  type?: string;
  name: string;
  idd?: string;
}
/** A user name, or a list of principals. */
export type Subject = string | Principal[];

/** Constants for the Speedle+ principal types; any string is accepted. */
export const PrincipalType = Object.freeze({
  User: "user",
  Group: "group",
  Role: "role",
  Entity: "entity",
} as const);

type Result = { decision: Decision } | { gaveUp: SpeedleError };

let pendingSeams: { clock: Clock; random: RandomSource } | undefined;

/** Internal test seam (design §11); not exported from the package. */
export function createClientForTesting(options: ClientOptions, clock: Clock, random: RandomSource): SpeedleClient {
  pendingSeams = { clock, random };
  try {
    return new SpeedleClient(options);
  } finally {
    pendingSeams = undefined;
  }
}

function callHook<E>(hook: ((event: E) => void) | undefined, hooks: Hooks, event: E): void {
  if (!hook) return;
  try {
    const result: unknown = hook.call(hooks, event);
    if (result && typeof (result as PromiseLike<unknown>).then === "function") {
      (result as Promise<unknown>).then(undefined, () => undefined);
    }
  } catch {
    // Hook failures never change the call's result (design §6.7).
  }
}

function buildSubject(subject: Subject): { principals: { type: string; name: string; idd?: string }[] } {
  if (typeof subject === "string") {
    if (subject === "") throw new InvalidRequestError("subject is empty");
    return { principals: [{ type: "user", name: subject }] };
  }
  if (!Array.isArray(subject) || subject.length === 0) {
    throw new InvalidRequestError("subject must be a user name or a non-empty list of principals");
  }
  return {
    principals: subject.map((p: unknown) => {
      if (p === null || typeof p !== "object") throw new InvalidRequestError("principal is not an object");
      const { type, name, idd } = p as { type?: unknown; name?: unknown; idd?: unknown };
      if (typeof name !== "string" || name === "") throw new InvalidRequestError("principal name is empty");
      if (type !== undefined && (typeof type !== "string" || type === "")) {
        throw new InvalidRequestError("principal type must be a non-empty string");
      }
      if (idd !== undefined && typeof idd !== "string") throw new InvalidRequestError("principal idd must be a string");
      const out: { type: string; name: string; idd?: string } = { type: type ?? "user", name };
      if (idd !== undefined && idd !== "") out.idd = idd;
      return out;
    }),
  };
}

function breakerOutcome(error: SpeedleError): BreakerOutcome {
  if (error instanceof NetworkError) return "failure";
  if (error.status !== undefined) return error.status >= 500 ? "failure" : "success";
  return "neutral";
}

/**
 * A client for one `is-allowed` endpoint. Create it once and share it; it is
 * safe for concurrent use and owns one keep-alive connection pool.
 */
export class SpeedleClient {
  readonly #opts: ResolvedOptions;
  readonly #clock: Clock;
  readonly #random: RandomSource;
  readonly #breaker: CircuitBreaker | undefined;
  readonly #userAgent: string;
  #closed = false;

  constructor(options: ClientOptions) {
    this.#opts = resolveOptions(options);
    this.#clock = pendingSeams?.clock ?? systemClock;
    this.#random = pendingSeams?.random ?? systemRandom;
    const { breaker, hooks, userAgentSuffix } = this.#opts;
    if (breaker) {
      this.#breaker = new CircuitBreaker(breaker.failureThreshold, breaker.coolDownMs, this.#clock, (from, to) =>
        callHook(hooks.onBreakerStateChange, hooks, { from, to }),
      );
    }
    this.#userAgent =
      `speedle-sdk-node/${VERSION} (node/${process.versions.node}; ${process.platform})` +
      (userAgentSuffix ? ` ${userAgentSuffix}` : "");
  }

  /**
   * Returns the server's `allowed` value. Every other outcome raises a typed
   * error, or returns false with `failureMode: "closed"`.
   */
  async isAllowed(
    subject: Subject,
    action: string,
    resource: string,
    attributes?: Attributes,
    options?: CallOptions,
  ): Promise<boolean> {
    const result = await this.#call(subject, action, resource, attributes, options);
    if ("decision" in result) return result.decision.allowed === true;
    if (this.#opts.failureMode === "closed") return false;
    throw result.gaveUp;
  }

  /** The detailed form. Always raises on failure, in every failure mode. */
  async check(
    subject: Subject,
    action: string,
    resource: string,
    attributes?: Attributes,
    options?: CallOptions,
  ): Promise<Decision> {
    const result = await this.#call(subject, action, resource, attributes, options);
    if ("decision" in result) return result.decision;
    throw result.gaveUp;
  }

  /** Releases the connection pool. The client must not be used afterwards. */
  close(): Promise<void> {
    this.#closed = true;
    this.#opts.agent.destroy();
    return Promise.resolve();
  }

  toString(): string {
    return `SpeedleClient(${this.#opts.backend}, ${this.#opts.url.origin})`;
  }

  #giveUp(attempts: number, reason: GiveUpReason, error: SpeedleError, ticket?: { trial: boolean }): Result {
    const hooks = this.#opts.hooks;
    callHook(hooks.onGiveUp, hooks, { attempts, reason, error });
    if (ticket && this.#breaker) this.#breaker.record(ticket, breakerOutcome(error));
    return { gaveUp: error };
  }

  #body(subject: Subject, action: string, resource: string, attributes: Attributes | undefined, serviceName: unknown): string {
    if (typeof action !== "string") throw new InvalidRequestError("action must be a string");
    if (typeof resource !== "string") throw new InvalidRequestError("resource must be a string");
    if (typeof serviceName !== "string" || serviceName === "") throw new InvalidRequestError("serviceName is empty");
    const wire = toWireAttributes(attributes);
    const body: Record<string, unknown> = { subject: buildSubject(subject), serviceName, resource, action };
    if (wire.length > 0) body.attributes = wire;
    return JSON.stringify(body);
  }

  async #call(
    subject: Subject,
    action: string,
    resource: string,
    attributes: Attributes | undefined,
    options: CallOptions | undefined,
  ): Promise<Result> {
    const signal = options?.signal;
    if (signal?.aborted) throw signal.reason;
    if (this.#closed) throw new SpeedleError("client is closed");
    const opts = this.#opts;
    const clock = this.#clock;

    let body: string;
    try {
      body = this.#body(subject, action, resource, attributes, options?.serviceName ?? opts.serviceName);
    } catch (err) {
      if (err instanceof InvalidRequestError) return this.#giveUp(0, "non_retryable", err);
      throw err;
    }

    let ticket: { trial: boolean } | undefined;
    if (this.#breaker) {
      ticket = this.#breaker.acquire();
      if (!ticket) return this.#giveUp(0, "circuit_open", new CircuitOpenError("circuit breaker is open"));
    }

    const headers: Record<string, string> = {
      "Content-Type": "application/json",
      Accept: "application/json",
      "User-Agent": this.#userAgent,
    };
    if (opts.authorization) headers.Authorization = opts.authorization;

    const hooks = opts.hooks;
    const deadlineAt = clock.now() + opts.deadlineMs;
    let attempts = 0;
    try {
      for (let attempt = 1; attempt <= opts.maxAttempts; attempt++) {
        const remaining = deadlineAt - clock.now();
        if (remaining <= 0) {
          return this.#giveUp(attempts, "deadline", new DeadlineExceededError("deadline exceeded"), ticket);
        }
        attempts = attempt;
        const deadlineBound = remaining <= opts.attemptTimeoutMs;
        const started = clock.now();
        let error: SpeedleError;
        let retryable: boolean;
        let retryAfterSeconds: number | undefined;
        let status: number | undefined;
        let deadlineCut = false;
        try {
          const res = await send({
            url: opts.url,
            agent: opts.agent,
            headers,
            body,
            timeoutMs: Math.min(opts.attemptTimeoutMs, remaining),
            signal,
          });
          status = res.status;
          const c = classify(opts.backend, res);
          if ("decision" in c) {
            callHook(hooks.onAttempt, hooks, { attempt, durationMs: clock.now() - started, status });
            if (ticket && this.#breaker) this.#breaker.record(ticket, "success");
            return c;
          }
          ({ error, retryable, retryAfterSeconds } = c);
        } catch (err) {
          if (!(err instanceof TransportFailure)) throw err;
          if (err.kind === "aborted") throw signal?.reason ?? err.original;
          retryAfterSeconds = undefined;
          if (err.kind === "timeout") {
            deadlineCut = deadlineBound;
            error = deadlineBound
              ? new DeadlineExceededError("deadline exceeded during the attempt")
              : new TimeoutError(err.message);
            retryable = !deadlineBound;
          } else if (err.kind === "too_large") {
            error = new UnexpectedResponseError(err.message);
            retryable = false;
          } else {
            error = new NetworkError(err.message, { cause: err.original });
            retryable = err.kind === "network";
          }
        }
        callHook(hooks.onAttempt, hooks, {
          attempt,
          durationMs: clock.now() - started,
          status,
          errorType: error.name,
        });
        if (deadlineCut) return this.#giveUp(attempts, "deadline", error, ticket);
        if (!retryable) return this.#giveUp(attempts, "non_retryable", error, ticket);
        if (attempt === opts.maxAttempts) return this.#giveUp(attempts, "attempts_exhausted", error, ticket);
        let delay = this.#random() * Math.min(opts.maxDelayMs, opts.baseDelayMs * 2 ** (attempt - 1));
        if (retryAfterSeconds !== undefined) delay = Math.max(delay, retryAfterSeconds * 1000);
        if (delay >= deadlineAt - clock.now()) {
          const reason = retryAfterSeconds !== undefined ? "retry_after_exceeds_deadline" : "deadline";
          return this.#giveUp(attempts, reason, error, ticket);
        }
        callHook(hooks.onRetry, hooks, { attempt: attempt + 1, delayMs: delay, errorType: error.name });
        await clock.sleep(delay, signal);
      }
    } catch (err) {
      // Caller cancellation: no give-up hook, no breaker count (design §6.3).
      if (ticket && this.#breaker) this.#breaker.record(ticket, "neutral");
      throw err;
    }
    // Unreachable: the loop always returns.
    return this.#giveUp(attempts, "attempts_exhausted", new SpeedleError("retry loop ended"), ticket);
  }
}
