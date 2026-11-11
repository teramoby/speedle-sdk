import type { IncomingHttpHeaders } from "node:http";
import {
  ForbiddenError,
  HttpError,
  InvalidRequestError,
  QuotaExceededError,
  RateLimitedError,
  RequestTooLargeError,
  ServerError,
  ServiceUnavailableError,
  type SpeedleError,
  TooManyRequestsError,
  UnauthorizedError,
  UnexpectedResponseError,
} from "./errors.js";
import type { Backend } from "./options.js";
import type { RawResponse } from "./transport.js";

/** Speedle reason codes, for Speedle Cloud and self-hosted Speedle (design §3.3). */
export enum Reason {
  GrantPolicyFound = 0,
  DenyPolicyFound = 1,
  ServiceNotFound = 2,
  NoApplicablePolicies = 3,
  ErrorInEvaluation = 4,
  /** `allowed` is true, but this is not a production grant. */
  DiscoverMode = 5,
  ReasonNotAvailable = 6,
}

export interface Decision {
  allowed: boolean;
  /** A {@link Reason}; unknown integers are kept as numbers. */
  reason: Reason | number;
  /** Self-hosted Speedle evaluator error, returned with HTTP 200 and a denial. */
  errorMessage?: string;
  /** Speedle Cloud `X-Quota-Daily-Remaining`. */
  quotaDailyRemaining?: number;
}

/** Result of classifying one HTTP response. Internal. */
export type Classified =
  | { decision: Decision }
  | { error: SpeedleError; retryable: boolean; retryAfterSeconds: number | undefined };

const CLOUD_CODES: ReadonlySet<string> = new Set([
  "invalid_request",
  "unsupported_token_assertion",
  "unauthorized",
  "service_mismatch",
  "service_not_found",
  "request_too_large",
  "rate_limited",
  "daily_quota_exceeded",
  "internal_error",
  "quota_unavailable",
  "snapshot_unavailable",
]);

const MAX_MESSAGE_LENGTH = 512;

function header(headers: IncomingHttpHeaders, name: string): string | undefined {
  const value = headers[name];
  return Array.isArray(value) ? value[0] : value;
}

function nonNegativeInt(value: string | undefined, positive: boolean): number | undefined {
  if (value === undefined) return undefined;
  const trimmed = value.trim();
  if (!/^\d+$/.test(trimmed)) return undefined;
  const n = Number(trimmed);
  if (!Number.isSafeInteger(n) || (positive && n === 0)) return undefined;
  return n;
}

function parseJson(body: Buffer): unknown {
  try {
    return JSON.parse(body.toString("utf8")) as unknown;
  } catch {
    return undefined;
  }
}

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

export function classify(backend: Backend, res: RawResponse): Classified {
  const quotaDailyRemaining = nonNegativeInt(header(res.headers, "x-quota-daily-remaining"), false);
  const status = res.status;
  const parsed = parseJson(res.body);

  if (status === 200) {
    const bad = (why: string): Classified => ({
      error: new UnexpectedResponseError(`unexpected response body: ${why}`, { status, quotaDailyRemaining }),
      retryable: false,
      retryAfterSeconds: undefined,
    });
    if (!isObject(parsed)) return bad("not a JSON object");
    if (typeof parsed.allowed !== "boolean") return bad('"allowed" is not a boolean');
    if (typeof parsed.reason !== "number" || !Number.isInteger(parsed.reason)) return bad('"reason" is not an integer');
    const decision: Decision = { allowed: parsed.allowed, reason: parsed.reason };
    if (typeof parsed.errorMessage === "string" && parsed.errorMessage !== "") decision.errorMessage = parsed.errorMessage;
    if (quotaDailyRemaining !== undefined) decision.quotaDailyRemaining = quotaDailyRemaining;
    return { decision };
  }

  const text = isObject(parsed) && typeof parsed.error === "string" ? parsed.error : undefined;
  let code: string | undefined;
  let detail: string | undefined;
  if (text !== undefined) {
    if (backend === "speedle-cloud" && CLOUD_CODES.has(text)) code = text;
    else detail = text.length > MAX_MESSAGE_LENGTH ? `${text.slice(0, MAX_MESSAGE_LENGTH)}...` : text;
  }
  const message = `HTTP ${status}${code ? `: ${code}` : detail ? `: ${detail}` : ""}`;
  const fields = { status, code, quotaDailyRemaining };
  const retryAfter = nonNegativeInt(header(res.headers, "retry-after"), true);
  const final = (error: SpeedleError): Classified => ({ error, retryable: false, retryAfterSeconds: undefined });
  const retry = (error: SpeedleError): Classified => ({ error, retryable: true, retryAfterSeconds: retryAfter });

  switch (status) {
    case 400:
      return final(new InvalidRequestError(message, fields));
    case 401:
      return final(new UnauthorizedError(message, fields));
    case 403:
      return final(new ForbiddenError(message, fields));
    case 413:
      return final(new RequestTooLargeError(message, fields));
    case 429: {
      const f = { ...fields, retryAfter };
      if (backend === "speedle" || code === "rate_limited") {
        const error = new RateLimitedError(message, f);
        // A burst 429 is retried only with a usable Retry-After (design §6.3).
        return retryAfter === undefined ? final(error) : retry(error);
      }
      if (code === "daily_quota_exceeded") return final(new QuotaExceededError(message, f));
      return final(new TooManyRequestsError(message, f));
    }
    case 500:
      return final(new ServerError(message, fields));
    case 502:
    case 504:
      return retry(new ServerError(message, fields));
    case 503:
      return retry(new ServiceUnavailableError(message, fields));
    default:
      if (status >= 500 && status <= 599) return final(new ServerError(message, fields));
      return final(new HttpError(message, fields));
  }
}
