/** Fields shared by every SDK error (design §7). */
export interface SpeedleErrorFields {
  /** HTTP status, or undefined when no response was received. */
  status?: number;
  /** Speedle Cloud error code, or undefined. */
  code?: string;
  /** Value of `X-Quota-Daily-Remaining`, or undefined when absent or malformed. */
  quotaDailyRemaining?: number;
  /** The underlying cause, when there is one. */
  cause?: unknown;
}

/** Base type of every error raised by the SDK. */
export class SpeedleError extends Error {
  readonly status: number | undefined;
  readonly code: string | undefined;
  readonly quotaDailyRemaining: number | undefined;

  constructor(message: string, fields: SpeedleErrorFields = {}) {
    super(message, fields.cause === undefined ? undefined : { cause: fields.cause });
    this.name = new.target.name;
    this.status = fields.status;
    this.code = fields.code;
    this.quotaDailyRemaining = fields.quotaDailyRemaining;
  }
}

/** Invalid client options. Raised by the constructor and the `Auth` factories. */
export class ConfigurationError extends SpeedleError {}
/** HTTP 400, or a client-side request check failed before sending. */
export class InvalidRequestError extends SpeedleError {}
/** HTTP 401. */
export class UnauthorizedError extends SpeedleError {}
/** HTTP 403, including older Speedle Cloud `service_mismatch`. */
export class ForbiddenError extends SpeedleError {}
/** HTTP 413. */
export class RequestTooLargeError extends SpeedleError {}

/** HTTP 429. `retryAfter` is the `Retry-After` value in seconds, when usable. */
export class TooManyRequestsError extends SpeedleError {
  readonly retryAfter: number | undefined;
  constructor(message: string, fields: SpeedleErrorFields & { retryAfter?: number } = {}) {
    super(message, fields);
    this.retryAfter = fields.retryAfter;
  }
}
/** Burst 429 (Speedle Cloud `rate_limited`, or any 429 from a self-hosted Speedle gateway). */
export class RateLimitedError extends TooManyRequestsError {}
/** Speedle Cloud daily quota (429 `daily_quota_exceeded`). Never retried. */
export class QuotaExceededError extends TooManyRequestsError {}

/** HTTP 503. */
export class ServiceUnavailableError extends SpeedleError {}
/** HTTP 500 and other 5xx. */
export class ServerError extends SpeedleError {}
/**
 * Any other HTTP status, including 3xx and 404.
 * Speedle Cloud 404 `service_not_found` uses this type with `code` `service_not_found`.
 */
export class HttpError extends SpeedleError {}
/** The response body is not the expected JSON. */
export class UnexpectedResponseError extends SpeedleError {}
/** Transport failure. */
export class NetworkError extends SpeedleError {}
/** The last attempt hit `attemptTimeoutMs`. Not the global `TimeoutError`. */
export class TimeoutError extends NetworkError {}
/** The call's deadline ended an attempt or left no time to start one. */
export class DeadlineExceededError extends TimeoutError {}
/** The circuit breaker rejected the call without sending a request. */
export class CircuitOpenError extends SpeedleError {}
