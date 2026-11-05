# Speedle SDK design

Status: design phase, proposed in
[teramoby/cloud.speedle.io#212](https://github.com/teramoby/cloud.speedle.io/issues/212).
Nothing here is implemented or published.

## 1. Scope

The SDKs wrap one operation: `POST /authz-check/v1/is-allowed`. They target
two backends:

| Backend id | What it is | Client authentication |
| --- | --- | --- |
| `speedle-cloud` | Speedle Cloud ADS (`https://cloud.speedle.io`, beta `https://b.speedle.io`) | Service-scoped bearer token |
| `speedle-plus` | Self-hosted Speedle+ ADS (REST, default port 6734) | None, or mutual TLS |

Languages, all built together with no phases (owner decision on #212):
Node.js/TypeScript, Python, Java, Go, C# and Rust. Every SDK has the same API
shape (§5), the same retry and failure behaviour (§6), the same error model
(§7), and passes the same conformance suite (§11).

The SDK is more than a thin wrapper: retries with backoff, deadlines,
cancellation, an opt-in fail-closed mode, an optional circuit breaker and
observability hooks are part of the spec (§6).

Out of scope for 0.x: the other ADS operations (`all-granted-roles`,
`all-granted-permissions`, `diagnose`, `discover`), PMS, gRPC, browser use
and Speedle+ token assertion (`subject.token`/`tokenType`, see §3.2).

## 2. Contract sources

| Backend | Source of truth | Pinned at |
| --- | --- | --- |
| Speedle Cloud | `docs/openapi.yaml` in teramoby/cloud.speedle.io, operation `ads-is-allowed`, schemas `AdsRequest`, `AdsDecision`, `Ads*Error`, security scheme `apiToken` | `946eafbb` (contract version 0.4.0) |
| Speedle+ | The handler `RESTService.IsAllowed` and its types in `pkg/svcs/adsrest/check.go`, error mapping in `pkg/httputils`, docs `docs/hugo/content/docs/decisions.md` and `security.md`, Swagger `docs/swagger/policy-check.yaml` | `46a4f90a` |

Where the Speedle+ Swagger and the handler disagree, the handler wins,
because it is what clients observe. The one known disagreement is noted in
§3.4.

The Cloud operation description states that the contract "is not evidence of
runtime cutover". The SDKs code against the contract; the beta smoke test
(§11) starts only after the ADS cutover is live on beta.

Contract drift: the repository keeps a copy of the extracted Cloud operation
and its schemas under `contract/` with the source commit. A scheduled CI job
compares it with upstream and opens an issue when it changes. A change then
goes through a normal PR that updates the copy, the vectors and each SDK.

## 3. Cloud versus Speedle+: `is-allowed` field by field

"SDK" in the last column describes what every SDK does.

### 3.1 Transport and request headers

| Item | Speedle Cloud | Speedle+ ADS | SDK |
| --- | --- | --- | --- |
| Method and path | `POST /authz-check/v1/is-allowed` | `POST /authz-check/v1/is-allowed` (`svcs.PolicyAtzPath`) | Same path for both; the endpoint setting is the base URL only. |
| Scheme | HTTPS only | HTTPS by default; plaintext only with `--insecure=true` | Requires `https://`. `http://` is accepted only for `speedle-plus` with `Auth.none()`, and only for loopback hosts unless the caller sets `allowInsecureHttp`. |
| `Authorization` | Required: `Bearer <AdsBearerToken>`; scheme case-insensitive, credential case-sensitive; otherwise 401 | Not read by ADS | Sent only with `Auth.bearer`. Never sent by `Auth.none()` or `Auth.mutualTls()`. A gateway in front of Speedle+ may still use `Auth.bearer`. |
| Client certificate | Not used | Required when the server runs with `--client-cert` and `--force-client-cert=true`; otherwise optional | `Auth.mutualTls(...)` presents it. A handshake failure is a `NetworkError` that is not retried. |
| `Content-Type` | `application/json` | `application/json` (Swagger documents 415 otherwise) | Always `application/json`. |
| Request size | 64 KiB before parsing, else 413 | No limit in the handler | No client-side size check; server limits can change and 413 is not charged. |
| `User-Agent` | Accepted | Accepted | Always sent, see §8. |

### 3.2 Request body

| Field | Speedle Cloud (`AdsRequest`) | Speedle+ (`JsonContext`) | SDK |
| --- | --- | --- | --- |
| Unknown top-level fields | Rejected (`additionalProperties: false`), 400 `invalid_request` | Ignored by the Go decoder | Sends only the fields below. |
| `subject` | Optional object or `null` | Object; Swagger marks it required | Always sent. |
| `subject.principals[]` | Array or `null`; items are `{type, name, idd}` or `null`; no extra keys | Array of `{type, name, idd}`, all `omitempty` | Sends a non-empty array of non-null items. `name` is required; `type` defaults to `user`; `idd` is sent only when set. |
| Principal `type` values | Any string | `user`, `group`, `role`, `entity` (`api/ads/types.go`) | Open string with constants for the four Speedle+ values. |
| `subject.token`, `subject.tokenType` | Non-empty value returns 400 `unsupported_token_assertion` | Used by the configured token asserter | Never sent in 0.x. Speedle+ token assertion may come later as a Speedle+-only option. |
| `subject.asserted` | Boolean, accepted | Not a field; ignored | Never sent. |
| `serviceName` | Optional. Empty or omitted means the token's service; a different non-empty value returns 403 `service_mismatch` | Required by Swagger. An unknown name gives a 200 with reason 2 `SERVICE_NOT_FOUND` | Taken from config. Required for `speedle-plus`. Optional for `speedle-cloud`, where it is omitted when unset. |
| `resource`, `action` | String | String | Required string arguments. |
| `attributes` | Object: name to any JSON value, nested arrays and objects allowed; at most 50 properties and 16,384 canonical UTF-8 bytes | Array of `{name, type, value}`; `type` is `string`, `numeric`, `bool`, `datetime` or empty; `null` values, `null` array items, mixed-type arrays are rejected with 400; no nested objects | The API always takes a map (§5). For `speedle-cloud` the map is sent as is. For `speedle-plus` each entry becomes `{name, type, value}` with `type` inferred: string to `string`, number to `numeric`, boolean to `bool`, a homogeneous array of one of these to that type, an empty array to an empty type. `null`, objects, `null` items and mixed arrays raise `InvalidRequestError` before any request is sent. |
| Date and time attributes | No date type in the schema | `datetime` strings are converted to Unix seconds (`ConvSingleValue`) | No datetime value type. Callers pass Unix seconds as a number, which is the value Speedle+ stores after conversion, and which Cloud receives unchanged. |

Integers above 2^53 lose precision in both backends (Speedle+ decodes
numbers as `float64`). The SDK documents this and does not guard against it.

### 3.3 Response body (HTTP 200)

| Field | Speedle Cloud (`AdsDecision`) | Speedle+ (`IsAllowedResponse`) | SDK |
| --- | --- | --- | --- |
| `allowed` | Required boolean | Boolean | `isAllowed` returns it. Anything other than JSON `true`/`false` raises `UnexpectedResponseError`. |
| `reason` | Required integer 0 to 6 | `int32`, same codes | `Decision.reason`, an enum with a fallback that keeps unknown integers. |
| `errorMessage` | Not allowed (`additionalProperties: false`) | Set, with HTTP 200, when the evaluator returns an error; the decision is then a denial, normally reason 4 `ERROR_IN_EVALUATION` | `Decision.errorMessage`, optional. `isAllowed` still returns the server's `allowed` value (false). |
| `requestContext`, `attributes`, `grantedRoles`, `rolePolicies`, `policies` | Optional in the schema (diagnostic shape); not in the examples | Never returned by `is-allowed` | Ignored. Not exposed in 0.x. |
| Other fields | None allowed | None | Ignored, so a server can add fields without breaking clients. |

Reason codes are identical in both backends: 0 `GRANT_POLICY_FOUND`,
1 `DENY_POLICY_FOUND`, 2 `SERVICE_NOT_FOUND`, 3 `NO_APPLICABLE_POLICIES`,
4 `ERROR_IN_EVALUATION`, 5 `DISCOVER_MODE`, 6 `REASON_NOT_AVAILABLE`.
`DISCOVER_MODE` returns `allowed: true` but is not a production grant; the SDK
docs say so.

### 3.4 Errors

Cloud error bodies are `{"error": "<code>"}` with a fixed code enum. Speedle+
error bodies are `{"error": "<free-text message>"}` from
`httputils.HandleError`. The SDK reads `error` as `code` for `speedle-cloud`
and as `message` for `speedle-plus` (where `code` is empty). Error type
names are in §7.

| Status | Speedle Cloud | Speedle+ ADS | SDK error | Retried |
| --- | --- | --- | --- | --- |
| 400 | `invalid_request`, `unsupported_token_assertion` | Invalid JSON or attribute conversion (`SPDL-0004`) | `InvalidRequestError` | No |
| 401 | `unauthorized`, with `WWW-Authenticate: Bearer`; not charged | Not produced by ADS (a gateway may) | `UnauthorizedError` | No |
| 403 | `service_mismatch` | `ExceedLimit` maps to 403; not expected on `is-allowed` | `ForbiddenError` | No |
| 404 | Not declared | Wrong path or method | `HttpError` | No |
| 413 | `request_too_large`; not charged | Not produced | `RequestTooLargeError` | No |
| 415 | Not declared | Documented in Swagger | `HttpError` (the SDK always sends JSON) | No |
| 429 burst | `rate_limited` (per token, 600 per 60 s); `Retry-After: 60` | Not produced by ADS; a gateway may send any 429 | `RateLimitedError`, with `retryAfter` | Only if `Retry-After` fits the remaining deadline (§6.3) |
| 429 daily | `daily_quota_exceeded` (account-wide, 10,000 per UTC day); `Retry-After` is the seconds to the next 00:00 UTC | No daily quota | `QuotaExceededError`, with `retryAfter` | Never |
| 500 | `internal_error`; charged when after handler entry | Unexpected errors. Swagger says evaluation failures are 500, but the handler returns them as 200 with `errorMessage` | `ServerError` | No |
| 502, 504 | Not declared (edge or proxy) | Not declared (proxy) | `ServerError` | Yes |
| 503 | `quota_unavailable` (not charged) or `snapshot_unavailable` (charged) | Not produced (a proxy may) | `ServiceUnavailableError` | Yes |
| Other 5xx | Not declared | Not declared | `ServerError` | No |
| Other status, 3xx, non-JSON body | Not declared | Not declared | `HttpError` or `UnexpectedResponseError`; redirects are never followed | No |

### 3.5 Response headers

| Header | Speedle Cloud | Speedle+ ADS | SDK |
| --- | --- | --- | --- |
| `Cache-Control` | `no-store` on every response | Not set | Never caches (§9). |
| `X-RateLimit-Limit` | `"600"` after bearer verification | Not set | Not exposed; the value is a documented constant. |
| `X-Quota-Daily-Remaining` | Non-negative integer string, present when the call was charged (200, 400, 403, 500, charged 503). A conservative lower bound | Not set | `quotaDailyRemaining` on `Decision` and on every error, or empty when the header is absent or malformed. |
| `Retry-After` | Positive integer seconds on every 429. `rate_limited`: 60. `daily_quota_exceeded`: seconds to the next 00:00 UTC | Not set | `retryAfter` on the 429 errors; empty if absent or not a positive integer. |
| `WWW-Authenticate` | `Bearer` on 401 | Not set | Ignored. |
| `Speedle-Principals` | Not set | First `user` principal name | Ignored. |
| `Content-Type` | JSON | `application/json; charset=UTF-8` | Body parsed as UTF-8 JSON regardless of parameters. |

### 3.6 Charging (Cloud only)

Cloud charges one daily unit per call that reaches the handler, including
400, 403, policy denials, 500 and `snapshot_unavailable` 503. A retried
`snapshot_unavailable` 503 therefore costs one unit per attempt, at most
`maxAttempts` per call. 500 is not retried, partly for this reason. The docs
state this, and `maxAttempts: 1` turns retries off.

## 4. Generated core or hand-written clients

| | Generated core plus idiomatic layer | Hand-written thin client |
| --- | --- | --- |
| Fit with the contract | The Cloud schema is OpenAPI 3.1 with `anyOf`/`null` and recursive `$defs`; common generators handle these poorly and emit awkward types. Speedle+ has no OpenAPI 3.1 contract, and its attribute format differs, so its path would be hand-written anyway. | One operation, written once per language against both backends. |
| Dependencies | Generator runtimes pull in HTTP and JSON libraries per language (for example OkHttp/Gson in Java), which collide with user dependencies. | Standard library HTTP in Node, Java, Go and .NET; `httpx` in Python; `reqwest` on `tokio` in Rust. |
| Idiomatic API, retries, typed errors | Still hand-written as the layer on top. | Hand-written. |
| Cost of a contract change | Regenerate, review generated diffs, keep the generator version and templates pinned per language, then adjust the layer. | Edit roughly 50 to 100 lines per SDK, update vectors. |
| Ongoing cost | Generator upgrades and template patches in 6 languages. | Roughly 1,000 to 1,500 lines per SDK including tests, mostly retry, breaker, errors and transport. |
| Drift protection | Generation from the file. | The contract copy and drift job (§2), plus the shared conformance vectors that every SDK must pass. |

**Recommendation: hand-written thin clients.** The surface is one operation;
most of the code is transport, retry and error handling, which a generator does
not provide. The main cost is that a contract change needs one manual edit
per SDK. The drift job makes the change visible, and the conformance vectors
make the edits uniform and testable. If the SDKs later cover many PMS
operations, generating types only (not clients) can be revisited.

## 5. Common API shape

Every SDK has one client object per backend endpoint, created once and shared.
It is safe for concurrent use. The positional order is always
`subject, action, resource, attributes`.

- `isAllowed(...)` returns a boolean: the `allowed` value of an HTTP 200
  whose body parses. By default every other outcome raises a typed error.
  With the opt-in fail-closed mode it returns `false` instead (§6.5).
- Every client takes the same resilience settings (§6): `RetryPolicy`,
  `FailureMode`, an optional circuit breaker, and optional hooks. Defaults:
  3 attempts, 100 ms base delay, 2 s cap, 2 s per attempt, 5 s overall
  deadline, failure mode `error`, no breaker, no hooks.
- `check(...)` is the detailed form. It returns a `Decision` with `allowed`,
  `reason`, `errorMessage` (Speedle+ only) and `quotaDailyRemaining` (Cloud
  only).
- A subject is either a string, meaning one `user` principal, or a list of
  principals.
- Attributes are a map from name to a JSON value (string, number, boolean,
  null, list, map). See §3.2 for the Speedle+ restrictions.

### 5.1 TypeScript (Node.js 22 or later, ESM and CJS)

```ts
export type Backend = "speedle-cloud" | "speedle-plus";
export type AttributeValue =
  | string | number | boolean | null
  | AttributeValue[] | { [name: string]: AttributeValue };
export type Attributes = Record<string, AttributeValue>;
export interface Principal { type?: string; name: string; idd?: string }
export type Subject = string | Principal[];

export interface Auth { readonly kind: "none" | "bearer" | "mutual-tls" }
export declare const Auth: {
  none(): Auth;
  bearer(token: string): Auth;
  mutualTls(options: { cert: string | Buffer; key: string | Buffer }): Auth; // PEM
};

export interface RetryPolicy {
  maxAttempts?: number;          // default 3, counts the first attempt; 1 disables retries
  baseDelayMs?: number;          // default 100
  maxDelayMs?: number;           // default 2000 (backoff cap)
  attemptTimeoutMs?: number;     // default 2000
  deadlineMs?: number;           // default 5000, whole call including delays
}
export type FailureMode = "error" | "closed";      // default "error"
export interface CircuitBreakerOptions {
  failureThreshold?: number;     // default 5 consecutive failed calls
  coolDownMs?: number;           // default 30000
}
export type ErrorType = string;  // an error class name from §7, e.g. "ServiceUnavailableError"
export type BreakerState = "closed" | "open" | "half_open";
export type GiveUpReason =
  | "non_retryable" | "attempts_exhausted" | "deadline"
  | "retry_after_exceeds_deadline" | "circuit_open";
export interface AttemptEvent { attempt: number; durationMs: number; status?: number; errorType?: ErrorType }
export interface RetryEvent { attempt: number; delayMs: number; errorType: ErrorType } // attempt = the next attempt
export interface GiveUpEvent { attempts: number; reason: GiveUpReason; error: SpeedleError }
export interface BreakerStateChangeEvent { from: BreakerState; to: BreakerState }
export interface Hooks {
  onAttempt?(event: AttemptEvent): void;
  onRetry?(event: RetryEvent): void;
  onGiveUp?(event: GiveUpEvent): void;
  onBreakerStateChange?(event: BreakerStateChangeEvent): void;
}
export interface ClientOptions {
  backend: Backend;
  endpoint: string;              // base URL, e.g. "https://cloud.speedle.io"
  auth: Auth;
  serviceName?: string;          // required for "speedle-plus"
  retry?: RetryPolicy;
  failureMode?: FailureMode;
  circuitBreaker?: CircuitBreakerOptions; // absent means no breaker
  hooks?: Hooks;
  caCert?: string | Buffer;      // extra trust anchor for self-hosted servers
  allowInsecureHttp?: boolean;   // default false
  userAgentSuffix?: string;
}
export interface CallOptions { signal?: AbortSignal } // abort rejects with signal.reason

export enum Reason {
  GrantPolicyFound = 0, DenyPolicyFound = 1, ServiceNotFound = 2,
  NoApplicablePolicies = 3, ErrorInEvaluation = 4, DiscoverMode = 5,
  ReasonNotAvailable = 6,
}
export interface Decision {
  allowed: boolean;
  reason: Reason | number;
  errorMessage?: string;
  quotaDailyRemaining?: number;
}

export declare class SpeedleClient {
  constructor(options: ClientOptions);
  isAllowed(subject: Subject, action: string, resource: string,
            attributes?: Attributes, options?: CallOptions): Promise<boolean>;
  check(subject: Subject, action: string, resource: string,
        attributes?: Attributes, options?: CallOptions): Promise<Decision>;
  close(): Promise<void>;
}
```

```ts
const speedle = new SpeedleClient({
  backend: "speedle-cloud",
  endpoint: "https://cloud.speedle.io",
  auth: Auth.bearer(process.env.SPEEDLE_TOKEN!),
});
if (await speedle.isAllowed("alice", "get", "/books/123", { team: "eng" })) { /* ... */ }
```

Package `@speedle/sdk`. Transport: `node:https` with a keep-alive `Agent`;
no runtime dependencies. The package declares Node only and is not built for
browsers. An aborted `signal` stops the call, including during a retry delay,
and rejects with `signal.reason` in every failure mode;
`AbortSignal.timeout(ms)` gives a shorter per-call bound.

### 5.2 Python (3.10 or later)

```python
Backend = Literal["speedle-cloud", "speedle-plus"]
AttributeValue = Union[str, int, float, bool, None,
                       Sequence["AttributeValue"], Mapping[str, "AttributeValue"]]

@dataclass(frozen=True)
class Principal:
    name: str
    type: str = "user"
    idd: str | None = None

Subject = Union[str, Sequence[Principal]]

class Auth:
    @staticmethod
    def none() -> "Auth": ...
    @staticmethod
    def bearer(token: str) -> "Auth": ...
    @staticmethod
    def mutual_tls(cert_file: str, key_file: str) -> "Auth": ...

@dataclass(frozen=True)
class RetryPolicy:              # all durations in seconds
    max_attempts: int = 3
    base_delay: float = 0.1
    max_delay: float = 2.0
    attempt_timeout: float = 2.0
    deadline: float = 5.0

FailureMode = Literal["error", "closed"]

@dataclass(frozen=True)
class CircuitBreaker:
    failure_threshold: int = 5
    cool_down: float = 30.0

BreakerState = Literal["closed", "open", "half_open"]
GiveUpReason = Literal["non_retryable", "attempts_exhausted", "deadline",
                       "retry_after_exceeds_deadline", "circuit_open"]

@dataclass(frozen=True)
class AttemptEvent:
    attempt: int
    duration: float
    status: int | None
    error_type: str | None      # class name from §7

@dataclass(frozen=True)
class RetryEvent:
    attempt: int                # the next attempt
    delay: float
    error_type: str

@dataclass(frozen=True)
class GiveUpEvent:
    attempts: int
    reason: GiveUpReason
    error: "SpeedleError"

@dataclass(frozen=True)
class BreakerStateChangeEvent:
    from_state: BreakerState
    to_state: BreakerState

@dataclass(frozen=True)
class Hooks:
    on_attempt: Callable[[AttemptEvent], None] | None = None
    on_retry: Callable[[RetryEvent], None] | None = None
    on_give_up: Callable[[GiveUpEvent], None] | None = None
    on_breaker_state_change: Callable[[BreakerStateChangeEvent], None] | None = None

class Reason(IntEnum):
    GRANT_POLICY_FOUND = 0
    DENY_POLICY_FOUND = 1
    SERVICE_NOT_FOUND = 2
    NO_APPLICABLE_POLICIES = 3
    ERROR_IN_EVALUATION = 4
    DISCOVER_MODE = 5
    REASON_NOT_AVAILABLE = 6

@dataclass(frozen=True)
class Decision:
    allowed: bool
    reason: Reason | int
    error_message: str | None = None
    quota_daily_remaining: int | None = None

class Client:
    def __init__(self, *, backend: Backend, endpoint: str, auth: Auth,
                 service_name: str | None = None,
                 retry: RetryPolicy = RetryPolicy(),
                 failure_mode: FailureMode = "error",
                 circuit_breaker: CircuitBreaker | None = None,
                 hooks: Hooks | None = None, ca_file: str | None = None,
                 allow_insecure_http: bool = False,
                 user_agent_suffix: str | None = None) -> None: ...
    # deadline overrides RetryPolicy.deadline for this call only.
    def is_allowed(self, subject: Subject, action: str, resource: str,
                   attributes: Mapping[str, AttributeValue] | None = None, *,
                   deadline: float | None = None) -> bool: ...
    def check(self, subject: Subject, action: str, resource: str,
              attributes: Mapping[str, AttributeValue] | None = None, *,
              deadline: float | None = None) -> Decision: ...
    def close(self) -> None: ...
    def __enter__(self) -> "Client": ...
    def __exit__(self, *exc: object) -> None: ...

class AsyncClient:
    # Same constructor; the methods are coroutines. Task cancellation and
    # asyncio.timeout() propagate asyncio.CancelledError / TimeoutError as usual.
    async def is_allowed(self, subject: Subject, action: str, resource: str,
                         attributes: Mapping[str, AttributeValue] | None = None, *,
                         deadline: float | None = None) -> bool: ...
    async def check(self, subject: Subject, action: str, resource: str,
                    attributes: Mapping[str, AttributeValue] | None = None, *,
                    deadline: float | None = None) -> Decision: ...
    async def aclose(self) -> None: ...
    async def __aenter__(self) -> "AsyncClient": ...
    async def __aexit__(self, *exc: object) -> None: ...
```

```python
with Client(backend="speedle-cloud", endpoint="https://cloud.speedle.io",
            auth=Auth.bearer(os.environ["SPEEDLE_TOKEN"])) as speedle:
    if speedle.is_allowed("alice", "get", "/books/123", {"team": "eng"}):
        ...
```

Transport: `httpx` (the only runtime dependency), one pooled
`httpx.Client`/`httpx.AsyncClient` per SDK client. Fully typed (`py.typed`).
Distribution `speedle`, import `speedle`. The sync client is bounded by the
deadline; the async client also honours task cancellation, including during
a retry delay, and re-raises `asyncio.CancelledError` in every failure mode.

### 5.3 Java (17 or later)

```java
package io.speedle.sdk;   // Maven io.speedle:speedle-sdk, module name io.speedle.sdk

public enum Backend { SPEEDLE_CLOUD, SPEEDLE_PLUS }

public record Principal(String type, String name, String idd) {
  public static Principal user(String name);
  public static Principal group(String name);
}

public final class Subject {
  public static Subject user(String name);
  public static Subject of(Principal... principals);
  public List<Principal> principals();
}

public interface Auth {
  static Auth none();
  static Auth bearer(String token);
  static Auth mutualTls(KeyStore clientKeyStore, char[] keyPassword);
}

public record RetryPolicy(int maxAttempts, Duration baseDelay, Duration maxDelay,
                          Duration attemptTimeout, Duration deadline) {
  public static RetryPolicy defaults();      // 3, 100 ms, 2 s, 2 s, 5 s
  public static RetryPolicy disabled();      // maxAttempts = 1, other values default
}

public enum FailureMode { ERROR, CLOSED }

public record CircuitBreakerPolicy(int failureThreshold, Duration coolDown) {
  public static CircuitBreakerPolicy defaults();   // 5, 30 s
}

public enum BreakerState { CLOSED, OPEN, HALF_OPEN }
public enum GiveUpReason { NON_RETRYABLE, ATTEMPTS_EXHAUSTED, DEADLINE,
                           RETRY_AFTER_EXCEEDS_DEADLINE, CIRCUIT_OPEN }

public record AttemptEvent(int attempt, Duration duration, OptionalInt status,
                           Optional<String> errorType) {}
public record RetryEvent(int attempt, Duration delay, String errorType) {}
public record GiveUpEvent(int attempts, GiveUpReason reason, SpeedleException error) {}
public record BreakerStateChangeEvent(BreakerState from, BreakerState to) {}

public interface SpeedleHooks {
  default void onAttempt(AttemptEvent event) {}
  default void onRetry(RetryEvent event) {}
  default void onGiveUp(GiveUpEvent event) {}
  default void onBreakerStateChange(BreakerStateChangeEvent event) {}
}

public record CallOptions(Optional<Duration> deadline) {
  public static CallOptions withDeadline(Duration deadline);
}

public enum Reason {
  GRANT_POLICY_FOUND, DENY_POLICY_FOUND, SERVICE_NOT_FOUND, NO_APPLICABLE_POLICIES,
  ERROR_IN_EVALUATION, DISCOVER_MODE, REASON_NOT_AVAILABLE, UNKNOWN;
}

public record Decision(boolean allowed, Reason reason, int reasonCode,
                       Optional<String> errorMessage, OptionalLong quotaDailyRemaining) {}

public final class SpeedleClient implements AutoCloseable {
  public static Builder builder();

  public boolean isAllowed(Subject subject, String action, String resource);
  public boolean isAllowed(Subject subject, String action, String resource,
                           Map<String, ?> attributes);
  public boolean isAllowed(Subject subject, String action, String resource,
                           Map<String, ?> attributes, CallOptions options);
  public Decision check(Subject subject, String action, String resource,
                        Map<String, ?> attributes);
  public Decision check(Subject subject, String action, String resource,
                        Map<String, ?> attributes, CallOptions options);
  @Override public void close();

  public static final class Builder {
    public Builder backend(Backend backend);
    public Builder endpoint(URI endpoint);
    public Builder auth(Auth auth);
    public Builder serviceName(String serviceName);
    public Builder retry(RetryPolicy retry);
    public Builder failureMode(FailureMode mode);              // default ERROR
    public Builder circuitBreaker(CircuitBreakerPolicy policy); // default none
    public Builder hooks(SpeedleHooks hooks);
    public Builder trustStore(KeyStore trustStore);
    public Builder allowInsecureHttp(boolean allow);
    public Builder userAgentSuffix(String suffix);
    public SpeedleClient build();
  }
}
```

```java
try (SpeedleClient speedle = SpeedleClient.builder()
    .backend(Backend.SPEEDLE_CLOUD)
    .endpoint(URI.create("https://cloud.speedle.io"))
    .auth(Auth.bearer(System.getenv("SPEEDLE_TOKEN")))
    .build()) {
  if (speedle.isAllowed(Subject.user("alice"), "get", "/books/123", Map.of("team", "eng"))) { /* ... */ }
}
```

Errors are unchecked (`SpeedleException extends RuntimeException`).
Transport: `java.net.http.HttpClient`. No runtime dependencies: a small
internal JSON codec covers this one schema and is tested by the vectors.
Attribute values are `String`, `Number`, `Boolean`, `null`, `List<?>` and
`Map<String, ?>`. A `CompletableFuture` variant is deferred; virtual threads
make the blocking call cheap on Java 21. Cancellation: interrupting the
calling thread aborts the attempt or the retry delay and throws
`CancelledError` with the interrupt flag restored; the deadline is the
time bound.

### 5.4 Go (the two latest Go releases)

```go
package speedle

type Backend string

const (
	BackendSpeedleCloud Backend = "speedle-cloud"
	BackendSpeedlePlus  Backend = "speedle-plus"
)

type Principal struct {
	Type string // defaults to "user"
	Name string
	IDD  string
}

type Subject []Principal

func User(name string) Subject

type Attributes map[string]any

type Auth interface{ apply(*transportConfig) error }

func NoAuth() Auth
func BearerToken(token string) Auth
func MutualTLS(certificate tls.Certificate) Auth

// Zero fields take the defaults.
type RetryPolicy struct {
	MaxAttempts    int           // default 3; 1 disables retries
	BaseDelay      time.Duration // default 100ms
	MaxDelay       time.Duration // default 2s
	AttemptTimeout time.Duration // default 2s
	Deadline       time.Duration // default 5s; a shorter ctx deadline wins
}

type FailureMode int

const (
	FailureModeError  FailureMode = iota // default
	FailureModeClosed                    // IsAllowed returns (false, nil) on SDK errors
)

type CircuitBreaker struct {
	FailureThreshold int           // default 5
	CoolDown         time.Duration // default 30s
}

type BreakerState string // "closed", "open", "half_open"
type GiveUpReason string // "non_retryable", "attempts_exhausted", "deadline",
// "retry_after_exceeds_deadline", "circuit_open"

type AttemptEvent struct {
	Attempt   int
	Duration  time.Duration
	Status    int    // 0 when no response
	ErrorType string // §7 name, "" on success
}
type RetryEvent struct {
	Attempt   int // the next attempt
	Delay     time.Duration
	ErrorType string
}
type GiveUpEvent struct {
	Attempts int
	Reason   GiveUpReason
	Err      *Error
}
type BreakerStateChangeEvent struct{ From, To BreakerState }

type Hooks struct {
	OnAttempt            func(AttemptEvent)
	OnRetry              func(RetryEvent)
	OnGiveUp             func(GiveUpEvent)
	OnBreakerStateChange func(BreakerStateChangeEvent)
}

type Config struct {
	Backend           Backend
	Endpoint          string // base URL
	Auth              Auth
	ServiceName       string // required for BackendSpeedlePlus
	Retry             RetryPolicy
	FailureMode       FailureMode
	CircuitBreaker    *CircuitBreaker // nil means no breaker
	Hooks             Hooks
	RootCAs           *x509.CertPool // nil means system roots
	AllowInsecureHTTP bool
	UserAgentSuffix   string
}

type Reason int

const (
	GrantPolicyFound Reason = iota
	DenyPolicyFound
	ServiceNotFound
	NoApplicablePolicies
	ErrorInEvaluation
	DiscoverMode
	ReasonNotAvailable
)

type Decision struct {
	Allowed             bool
	Reason              Reason
	ErrorMessage        string // Speedle+ only
	QuotaDailyRemaining *int64 // Speedle Cloud only
}

type Client struct{ /* unexported */ }

func NewClient(cfg Config) (*Client, error)
func (c *Client) IsAllowed(ctx context.Context, subject Subject, action, resource string, attrs Attributes) (bool, error)
func (c *Client) Check(ctx context.Context, subject Subject, action, resource string, attrs Attributes) (*Decision, error)
func (c *Client) Close()
```

```go
client, err := speedle.NewClient(speedle.Config{
	Backend:  speedle.BackendSpeedleCloud,
	Endpoint: "https://cloud.speedle.io",
	Auth:     speedle.BearerToken(os.Getenv("SPEEDLE_TOKEN")),
})
if err != nil { return err }
defer client.Close()
ok, err := client.IsAllowed(ctx, speedle.User("alice"), "get", "/books/123", speedle.Attributes{"team": "eng"})
```

Module `github.com/teramoby/speedle-sdk/go` (§12), package `speedle`.
Transport: `net/http` with its own `http.Transport`; no third-party
dependencies. Cancellation: when `ctx` is cancelled or its deadline passes,
the call stops, including during a retry delay, and returns an error for
which `errors.Is(err, ctx.Err())` holds. This error is returned even in
`FailureModeClosed`.

### 5.5 C# (.NET 8 or later)

```csharp
namespace Speedle.Sdk;   // NuGet package Speedle.Sdk, target net8.0

public enum Backend { SpeedleCloud, SpeedlePlus }

public sealed record Principal(string Name, string Type = "user", string? Idd = null)
{
    public static Principal User(string name);
    public static Principal Group(string name);
}

public sealed class Subject
{
    public static Subject User(string name);
    public static Subject Of(params Principal[] principals);
    public IReadOnlyList<Principal> Principals { get; }
    public static implicit operator Subject(string userName);
}

public abstract class Auth
{
    public static Auth None();
    public static Auth Bearer(string token);
    public static Auth MutualTls(X509Certificate2 clientCertificate); // with private key
}

public sealed record RetryPolicy
{
    public int MaxAttempts { get; init; } = 3;
    public TimeSpan BaseDelay { get; init; } = TimeSpan.FromMilliseconds(100);
    public TimeSpan MaxDelay { get; init; } = TimeSpan.FromSeconds(2);
    public TimeSpan AttemptTimeout { get; init; } = TimeSpan.FromSeconds(2);
    public TimeSpan Deadline { get; init; } = TimeSpan.FromSeconds(5);
    public static RetryPolicy Disabled { get; }      // MaxAttempts = 1
}

public enum FailureMode { Error, Closed }

public sealed record CircuitBreakerPolicy
{
    public int FailureThreshold { get; init; } = 5;
    public TimeSpan CoolDown { get; init; } = TimeSpan.FromSeconds(30);
}

public enum BreakerState { Closed, Open, HalfOpen }
public enum GiveUpReason { NonRetryable, AttemptsExhausted, Deadline,
                           RetryAfterExceedsDeadline, CircuitOpen }

public sealed record AttemptEvent(int Attempt, TimeSpan Duration, int? Status, string? ErrorType);
public sealed record RetryEvent(int Attempt, TimeSpan Delay, string ErrorType);
public sealed record GiveUpEvent(int Attempts, GiveUpReason Reason, SpeedleException Error);
public sealed record BreakerStateChangeEvent(BreakerState From, BreakerState To);

public sealed class SpeedleHooks
{
    public Action<AttemptEvent>? OnAttempt { get; init; }
    public Action<RetryEvent>? OnRetry { get; init; }
    public Action<GiveUpEvent>? OnGiveUp { get; init; }
    public Action<BreakerStateChangeEvent>? OnBreakerStateChange { get; init; }
}

public sealed class SpeedleClientOptions
{
    public required Backend Backend { get; init; }
    public required Uri Endpoint { get; init; }
    public required Auth Auth { get; init; }
    public string? ServiceName { get; init; }               // required for SpeedlePlus
    public RetryPolicy Retry { get; init; } = new();
    public FailureMode FailureMode { get; init; } = FailureMode.Error;
    public CircuitBreakerPolicy? CircuitBreaker { get; init; } // null means no breaker
    public SpeedleHooks? Hooks { get; init; }
    public X509Certificate2Collection? ExtraTrustedCertificates { get; init; }
    public bool AllowInsecureHttp { get; init; }
    public string? UserAgentSuffix { get; init; }
}

public enum Reason
{
    GrantPolicyFound = 0, DenyPolicyFound = 1, ServiceNotFound = 2,
    NoApplicablePolicies = 3, ErrorInEvaluation = 4, DiscoverMode = 5,
    ReasonNotAvailable = 6,  // unknown codes are kept as the cast integer
}

public sealed record Decision(bool Allowed, Reason Reason, string? ErrorMessage,
                              long? QuotaDailyRemaining);

public sealed class SpeedleClient : IDisposable, IAsyncDisposable
{
    public SpeedleClient(SpeedleClientOptions options);
    public Task<bool> IsAllowedAsync(Subject subject, string action, string resource,
        IReadOnlyDictionary<string, object?>? attributes = null,
        CancellationToken cancellationToken = default);
    public Task<Decision> CheckAsync(Subject subject, string action, string resource,
        IReadOnlyDictionary<string, object?>? attributes = null,
        CancellationToken cancellationToken = default);
    public void Dispose();
    public ValueTask DisposeAsync();
}
```

```csharp
await using var speedle = new SpeedleClient(new SpeedleClientOptions
{
    Backend = Backend.SpeedleCloud,
    Endpoint = new Uri("https://cloud.speedle.io"),
    Auth = Auth.Bearer(Environment.GetEnvironmentVariable("SPEEDLE_TOKEN")!),
});
if (await speedle.IsAllowedAsync("alice", "get", "/books/123",
        new Dictionary<string, object?> { ["team"] = "eng" }, ct)) { /* ... */ }
```

Errors derive from `SpeedleException`, named with the .NET `Exception`
suffix (`InvalidRequestException`, `RateLimitedException`, ...); the
conformance runner maps them to the §7 names. Attribute values are `string`,
numeric primitives, `bool`, `null`, `IEnumerable<object?>`,
`IReadOnlyDictionary<string, object?>` and `System.Text.Json.Nodes.JsonNode`.
Transport: one `HttpClient` over a `SocketsHttpHandler` with
`PooledConnectionLifetime` of 5 minutes, and `System.Text.Json` with source
generation, so the package has no dependencies and is trimming and AOT
compatible. A cancelled `cancellationToken` stops the call, including during
a retry delay, and throws `OperationCanceledException`, also in
`FailureMode.Closed`. Callers set a shorter time bound with
`CancellationTokenSource.CancelAfter`.

### 5.6 Rust (stable, MSRV 1.75, edition 2021)

```rust
// crate `speedle`
pub enum Backend { SpeedleCloud, SpeedlePlus }

pub struct Principal {
    pub kind: String,          // serialized as "type"; default "user"
    pub name: String,
    pub idd: Option<String>,
}

pub struct Subject(/* private */);
impl Subject {
    pub fn user(name: impl Into<String>) -> Self;
    pub fn of(principals: impl IntoIterator<Item = Principal>) -> Self;
    pub fn principals(&self) -> &[Principal];
}
impl From<&str> for Subject { /* one user principal */ }

pub type Attributes = serde_json::Map<String, serde_json::Value>;

pub struct Auth(/* private */);
impl Auth {
    pub fn none() -> Self;
    pub fn bearer(token: impl Into<String>) -> Self;
    pub fn mutual_tls(cert_pem: &[u8], key_pem: &[u8]) -> Result<Self, Error>;
}

#[derive(Clone, Debug)]
pub struct RetryPolicy {
    pub max_attempts: u32,         // 3
    pub base_delay: Duration,      // 100 ms
    pub max_delay: Duration,       // 2 s
    pub attempt_timeout: Duration, // 2 s
    pub deadline: Duration,        // 5 s
}
impl Default for RetryPolicy { /* the values above */ }
impl RetryPolicy { pub fn disabled() -> Self; }

pub enum FailureMode { Error, Closed }    // Default: Error

#[derive(Clone, Debug)]
pub struct CircuitBreaker { pub failure_threshold: u32, pub cool_down: Duration } // Default: 5, 30 s

pub enum BreakerState { Closed, Open, HalfOpen }
pub enum GiveUpReason { NonRetryable, AttemptsExhausted, Deadline, RetryAfterExceedsDeadline, CircuitOpen }

pub struct AttemptEvent { pub attempt: u32, pub duration: Duration, pub status: Option<u16>, pub error_kind: Option<ErrorKind> }
pub struct RetryEvent { pub attempt: u32, pub delay: Duration, pub error_kind: ErrorKind }
pub struct GiveUpEvent<'a> { pub attempts: u32, pub reason: GiveUpReason, pub error: &'a Error }
pub struct BreakerStateChangeEvent { pub from: BreakerState, pub to: BreakerState }

pub trait Hooks: Send + Sync + 'static {
    fn on_attempt(&self, _event: &AttemptEvent) {}
    fn on_retry(&self, _event: &RetryEvent) {}
    fn on_give_up(&self, _event: &GiveUpEvent<'_>) {}
    fn on_breaker_state_change(&self, _event: &BreakerStateChangeEvent) {}
}

#[non_exhaustive]
pub enum Reason { GrantPolicyFound, DenyPolicyFound, ServiceNotFound, NoApplicablePolicies,
                  ErrorInEvaluation, DiscoverMode, ReasonNotAvailable, Unknown(i64) }

pub struct Decision {
    pub allowed: bool,
    pub reason: Reason,
    pub error_message: Option<String>,
    pub quota_daily_remaining: Option<u64>,
}

#[non_exhaustive]
pub enum ErrorKind { Configuration, InvalidRequest, Unauthorized, Forbidden, RequestTooLarge,
                     TooManyRequests, RateLimited, QuotaExceeded, ServiceUnavailable, Server,
                     Http, UnexpectedResponse, Network, Timeout, DeadlineExceeded, CircuitOpen }

pub struct Error(/* private */);   // implements std::error::Error + Send + Sync
impl Error {
    pub fn kind(&self) -> ErrorKind;
    pub fn status(&self) -> Option<u16>;
    pub fn code(&self) -> Option<&str>;
    pub fn retry_after(&self) -> Option<Duration>;
    pub fn quota_daily_remaining(&self) -> Option<u64>;
}

#[derive(Clone)]                 // cheap clone, shares the pool and breaker
pub struct Client(/* Arc<...> */);
impl Client {
    pub fn builder(backend: Backend, endpoint: &str, auth: Auth) -> ClientBuilder;
    pub async fn is_allowed(&self, subject: &Subject, action: &str, resource: &str,
                            attributes: Option<&Attributes>) -> Result<bool, Error>;
    pub async fn check(&self, subject: &Subject, action: &str, resource: &str,
                       attributes: Option<&Attributes>) -> Result<Decision, Error>;
}

pub struct ClientBuilder(/* private */);
impl ClientBuilder {
    pub fn service_name(self, name: impl Into<String>) -> Self;
    pub fn retry(self, policy: RetryPolicy) -> Self;
    pub fn failure_mode(self, mode: FailureMode) -> Self;
    pub fn circuit_breaker(self, breaker: CircuitBreaker) -> Self;
    pub fn hooks(self, hooks: impl Hooks) -> Self;
    pub fn add_root_certificate_pem(self, pem: &[u8]) -> Self;
    pub fn allow_insecure_http(self, allow: bool) -> Self;
    pub fn user_agent_suffix(self, suffix: impl Into<String>) -> Self;
    pub fn build(self) -> Result<Client, Error>;
}

#[cfg(feature = "blocking")]
pub mod blocking {
    pub struct Client(/* private */);   // same builder and methods, not async
}
```

```rust
let speedle = speedle::Client::builder(Backend::SpeedleCloud, "https://cloud.speedle.io",
                                       Auth::bearer(std::env::var("SPEEDLE_TOKEN")?))
    .build()?;
if speedle.is_allowed(&"alice".into(), "get", "/books/123", Some(&attrs)).await? { /* ... */ }
```

Runtime and HTTP stack: **`tokio` with `reqwest`** (default features off,
rustls TLS with the platform's native root store, HTTP/1.1 and HTTP/2), plus
`serde`/`serde_json`. Reasons: `reqwest` on `hyper` is the most used Rust
HTTP client, and it already provides keep-alive pooling, timeouts, PEM
client identities for mutual TLS and extra root certificates. Tokio is the
runtime `reqwest` needs and the one most Rust services already run. Rustls
avoids a system OpenSSL dependency, which keeps builds and cross-compilation
simple. Alternatives considered: raw `hyper` (more code for the same
result), `ureq` (blocking only), runtime-agnostic clients (less maintained).

Cargo features: `default = []`; `blocking` enables `reqwest/blocking` and
`speedle::blocking::Client`, for programs without an async runtime. As with
`reqwest`, the blocking client must not be called from inside an async
runtime; the docs say so. Cancellation: dropping the future stops the call,
including during a retry delay; `tokio::time::timeout` gives a shorter
bound. The blocking client is bounded by the deadline only.

## 6. Configuration, transport and retries

### 6.1 Endpoint and backend

- `backend` is required. It is never auto-detected, because it selects the
  attribute wire format (§3.2) and the error body interpretation (§3.4).
- `endpoint` is a base URL with scheme, host and optional port and path
  prefix. The SDK appends `/authz-check/v1/is-allowed`. Query strings and
  fragments are rejected.
- `serviceName` is required for `speedle-plus` and optional for
  `speedle-cloud`, where the token already names the service.
- Configuration errors raise `ConfigurationError` from the constructor, before
  any request.

### 6.2 Pluggable auth

`Auth` is a closed type with three built-in implementations. It
is an interface or factory in every language, so another implementation (for
example a custom gateway header) can be added later without breaking the
API.

| Implementation | Use | Behaviour |
| --- | --- | --- |
| `bearer(token)` | Speedle Cloud; Speedle+ behind an authenticating gateway | Sends `Authorization: Bearer <token>`. Rejects empty tokens and tokens with whitespace or control characters. It does not check the `spdl_` format, so a future token format keeps working. Requires HTTPS. |
| `none()` | Speedle+ on a trusted network or loopback | Sends no credential. |
| `mutualTls(...)` | Speedle+ with `--client-cert` and `--force-client-cert=true` | Presents the client certificate on the TLS handshake. Server verification stays on. |

Server trust uses the system roots plus an optional extra CA (`caCert`,
`ca_file`, `trustStore`, `RootCAs`, `ExtraTrustedCertificates`,
`add_root_certificate_pem`). There is no option to disable certificate
verification.

### 6.3 Retry policy

`is-allowed` is read-only, so retrying it is safe; the only side effect is
the Cloud daily charge (§3.6).

| Setting | Default | Meaning |
| --- | --- | --- |
| `maxAttempts` | 3 | Attempts per call, counting the first. `1` disables retries. |
| `baseDelay` | 100 ms | Backoff base. |
| `maxDelay` | 2 s | Backoff cap. |
| `attemptTimeout` | 2 s | One attempt: connect, TLS, request and response. |
| `deadline` | 5 s | The whole call, attempts and delays included. |

Backoff is exponential with full jitter. Before attempt `n + 1` the SDK
sleeps `random(0, min(maxDelay, baseDelay × 2^(n-1)))`, where `random` is
uniform. If the failed attempt carries a usable `Retry-After`, the delay is
`max(that delay, Retry-After)`.

Deadline: the call starts a deadline at `now + deadline`. A shorter caller
bound (Go `ctx` deadline, per-call `deadline` in Python and Java) replaces
it. Each attempt gets `min(attemptTimeout, remaining)`.

**Retried**, while attempts and deadline remain:

- Connection errors: DNS failure, connection refused, connection reset or
  closed before a full response.
- Attempt timeouts (`attemptTimeout` expired, not the deadline).
- HTTP 502, 503 and 504.
- A burst 429 (below) with a `Retry-After` that is strictly less than the
  remaining deadline.

**Never retried:** 400, 401, 403, 404 and the daily-quota 429. These raise
their typed error at once, with `retryAfter` when the header is present.
Every other outcome is also final: 500 and other unlisted 5xx, 413, 415,
other statuses, unexpected bodies, TLS certificate or handshake failures,
and client-side validation errors.

**Burst versus daily-quota 429.** The SDK never guesses from the size of
`Retry-After`.

- `speedle-cloud`: it reads the body's `error` code from the Cloud contract.
  `rate_limited` is a burst 429 (`RateLimitedError`). `daily_quota_exceeded`
  is the daily quota (`QuotaExceededError`), never retried; its
  `Retry-After` points at the next 00:00 UTC. Any other code, or a body that
  does not parse, gives `TooManyRequestsError` and is not retried.
- `speedle-plus`: ADS has no rate limit or quota and never returns 429. A 429
  can only come from a gateway, with a free-text body. The SDK treats it as a
  burst 429 (`RateLimitedError`, empty `code`).
- In both cases a burst 429 is retried only when `Retry-After` is present, a
  positive integer of seconds, and fits as above. Otherwise the SDK raises
  `RateLimitedError` (give-up reason `retry_after_exceeds_deadline` or
  `non_retryable` when the header is missing). With Cloud's current
  `Retry-After: 60` and the 5 s default deadline, a Cloud burst 429 is raised
  at once; a caller with a longer deadline gets the wait.

**Algorithm**, the reference for every SDK and for the conformance vectors:

```text
check breaker (§6.6); if it rejects: give up(circuit_open, CircuitOpenError)
deadlineAt = now + deadline (or the caller's shorter bound)
for attempt = 1 .. maxAttempts:
    remaining = deadlineAt - now
    if remaining <= 0: give up(deadline, DeadlineExceededError)
    outcome = send(timeout = min(attemptTimeout, remaining)); hook onAttempt
    if outcome is a 200 decision: record breaker success; return it
    if the deadline cut the attempt: give up(deadline, DeadlineExceededError)
    if outcome is not retryable: give up(non_retryable, its error)
    if attempt == maxAttempts: give up(attempts_exhausted, its error)
    delay = jittered backoff, raised to Retry-After when usable
    if delay >= deadlineAt - now:
        give up(Retry-After set ? retry_after_exceeds_deadline : deadline, its error)
    hook onRetry; sleep(delay), cancellable
give up(reason, error): hook onGiveUp; record breaker outcome; apply failure mode (§6.5)
```

A caller cancellation (§5) stops the loop at any point, including during
`sleep`. It raises the language's cancellation error, fires no give-up hook,
does not count for the breaker and is never turned into `false`.

### 6.4 Connection reuse

Each client owns one keep-alive connection pool (Node `https.Agent` with
`keepAlive`, `httpx` pool, Java `HttpClient`, Go `http.Transport`, .NET
`SocketsHttpHandler`, `reqwest` pool). Clients are thread-safe and meant to
be created once per process. `close` releases the pool.

### 6.5 Failure mode

- `error` (default): when the call gives up, `isAllowed` and `check` raise
  the typed error (§7).
- `closed` (opt-in): `isAllowed` returns `false` instead of raising any SDK
  error, including `CircuitOpenError` and client-side validation errors.
  `onGiveUp` still fires with the error, so the failure stays visible.
  `check` always raises, because a `Decision` must come from the server.
  Constructor `ConfigurationError` and caller cancellation are never
  converted.
- **Fail-open is never offered.** No setting, hook or error path makes
  `isAllowed` return `true` without a 200 response whose `allowed` is `true`.
  Hook return values are ignored.

### 6.6 Circuit breaker

Off by default. When configured, one breaker per client counts calls, not
attempts:

- A call that gives up with `NetworkError`, `TimeoutError`,
  `DeadlineExceededError` or any 5xx error counts as a failure. A 200 or
  any 4xx response resets the count, because the server answered. Client-side
  validation errors and caller cancellations do not change it.
- `closed`: after `failureThreshold` (default 5) consecutive failures the
  breaker moves to `open`.
- `open`: calls fail at once with `CircuitOpenError`, without a request,
  until `coolDown` (default 30 s) has passed; then the breaker moves to
  `half_open`.
- `half_open`: exactly one call is let through as a trial; concurrent calls
  get `CircuitOpenError`. Success moves to `closed`, failure back to `open`
  with a new cool-down.

### 6.7 Hooks

Optional callbacks, all off by default: `onAttempt` after every attempt
(attempt number, duration, status or error type), `onRetry` before every
delay (next attempt number, delay, error type), `onGiveUp` when a call ends
in an error (attempts made, reason, error) and `onBreakerStateChange`
(`from`, `to`). Signatures are in §5.

- Hooks run synchronously on the calling thread or task and should be cheap.
  An exception or panic in a hook is caught and ignored; it never changes the
  call's result.
- Payloads never contain the token, headers, subject, action, resource,
  attributes or request body. Errors in payloads follow the §7 redaction
  rule.
- The SDK ships no metrics or tracing integration and sends nothing
  anywhere. Callers attach their own logging or metrics through the hooks.

## 7. Typed errors

All SDK errors share one base type. Each error carries `status` (HTTP status
or empty), `code` (Cloud error code or empty), `message`, and
`quotaDailyRemaining` when the header was present. Messages never contain the
token, request attributes or the full request body.

| Error | When | TypeScript / Python / Java | Go | C# | Rust `ErrorKind` |
| --- | --- | --- | --- | --- | --- |
| Base | Any SDK error | `SpeedleError` / `SpeedleError` / `SpeedleException` | `*speedle.Error` | `SpeedleException` | `speedle::Error` |
| Configuration | Invalid client options | `ConfigurationError` | `ErrConfiguration` | `ConfigurationException` | `Configuration` |
| Invalid request | 400; Speedle+ attribute conversion before sending | `InvalidRequestError` | `ErrInvalidRequest` | `InvalidRequestException` | `InvalidRequest` |
| Unauthorized | 401 | `UnauthorizedError` | `ErrUnauthorized` | `UnauthorizedException` | `Unauthorized` |
| Forbidden | 403, including `service_mismatch` | `ForbiddenError` | `ErrForbidden` | `ForbiddenException` | `Forbidden` |
| Request too large | 413 | `RequestTooLargeError` | `ErrRequestTooLarge` | `RequestTooLargeException` | `RequestTooLarge` |
| Too many requests | 429 with an unknown code | `TooManyRequestsError` | `ErrTooManyRequests` | `TooManyRequestsException` | `TooManyRequests` |
| Rate limited | Burst 429 (§6.3) | `RateLimitedError` | `ErrRateLimited` | `RateLimitedException` | `RateLimited` |
| Daily quota exceeded | 429 `daily_quota_exceeded` | `QuotaExceededError` | `ErrQuotaExceeded` | `QuotaExceededException` | `QuotaExceeded` |
| Service unavailable | 503 | `ServiceUnavailableError` | `ErrServiceUnavailable` | `ServiceUnavailableException` | `ServiceUnavailable` |
| Server error | 500 and other 5xx | `ServerError` | `ErrServer` | `ServerException` | `Server` |
| HTTP error | Any other status, including 3xx and 404 | `HttpError` | `ErrHTTP` | `HttpException` | `Http` |
| Unexpected response | Body is not the expected JSON | `UnexpectedResponseError` | `ErrUnexpectedResponse` | `UnexpectedResponseException` | `UnexpectedResponse` |
| Network | Transport failure | `NetworkError` | `ErrNetwork` | `NetworkException` | `Network` |
| Timeout | The last attempt hit `attemptTimeout` (subtype of Network) | `TimeoutError` | `ErrTimeout` | `SpeedleTimeoutException` | `Timeout` |
| Deadline exceeded | The call's deadline ended an attempt or left no time to start one (subtype of Timeout) | `DeadlineExceededError` | `ErrDeadlineExceeded` | `DeadlineExceededException` | `DeadlineExceeded` |
| Circuit open | The breaker rejected the call | `CircuitOpenError` | `ErrCircuitOpen` | `CircuitOpenException` | `CircuitOpen` |
| Cancelled | Java only: the calling thread was interrupted | `CancelledError` (Java) | n/a, `ctx.Err()` | n/a, `OperationCanceledException` | n/a, future dropped |

`RateLimitedError` and `QuotaExceededError` are subtypes of
`TooManyRequestsError` so callers can handle both. `retryAfter` is a
duration in seconds (`number` in TypeScript, `float` in Python, `Duration` in
Java, `time.Duration` in Go, `TimeSpan` in C#, `Duration` in Rust). In Go,
`*speedle.Error` has `Kind`, `Status`, `Code`, `Message`, `RetryAfter` and
`QuotaDailyRemaining` fields; the `Err*` values are sentinels for
`errors.Is`, and `errors.As` gives the details. TypeScript and Python name
the timeout class `TimeoutError` inside the SDK namespace; it does not
replace the built-in. When a call gives up after retries, the SDK raises the
error of the last attempt, except for `DeadlineExceededError` and
`CircuitOpenError`.

## 8. User-Agent, telemetry and logging

- `User-Agent: speedle-sdk-<language>/<sdk version> (<runtime>/<version>; <os>)`,
  for example `speedle-sdk-go/0.1.0 (go/1.26.1; linux)`. `<language>` is
  `node`, `python`, `java`, `go`, `dotnet` or `rust`. `userAgentSuffix` is
  appended after a space.
- No telemetry: the SDK sends nothing except the `is-allowed` request, adds no
  tracking headers and makes no other network calls.
- No logging. The hooks in §6.7 are the only observability interface.

## 9. Decision caching

Off, and not offered in 0.x. Cloud returns `Cache-Control: no-store`.
Policy changes reach Cloud in about 60 seconds and token revocation takes
effect within 60 seconds; a client cache would extend both windows without
the server knowing. A cache keyed on the subject and attributes is also easy
to get wrong. An opt-in cache can be proposed later in its own design, with
a mandatory short TTL, a key over the full canonical request plus backend and
service, and no caching of errors.

## 10. Security

- The Cloud token is a server-side secret. Every README and quickstart says
  never to ship it to browsers, mobile apps or desktop clients. The Node
  package targets Node only and is not built for browsers.
- `Auth.bearer` refuses `http://` endpoints. The token is redacted from
  `toString`, `repr`, hook payloads and error messages.
- Redirects are never followed, so the `Authorization` header cannot leak to
  another host.
- TLS verification cannot be disabled.
- Subjects and attributes must come from the application's own authenticated
  context, not from untrusted client input. The docs say so, and repeat the
  Speedle+ warning about unauthenticated plaintext endpoints.
- Fail closed: no error path returns `true`, and fail-open is never offered
  (§6.5).
- Tokens used in CI live only in GitHub encrypted secrets, are scoped to the
  beta smoke test and are never printed.

## 11. Conformance suite and CI

- `conformance/vectors/*.json`: language-neutral test vectors in the format of
  `conformance/schema.json`; see `conformance/README.md`. A vector scripts a
  sequence of responses and faults (connection reset, no response) for one or
  more calls, and can assert the retry delays, the hook events and breaker
  transitions.
- Virtual clock: every SDK's retry loop and breaker read time through an
  internal clock and draw jitter from an internal random source. Runners
  inject a test clock whose `sleep` returns at once and adds the duration to
  an offset (`now = real monotonic time + offset`), and a random source
  that returns the vector's `jitterFraction` (default 1, the top of the
  range). Delays and cool-downs therefore take no real time and are exact,
  while attempt timeouts against a silent mock stay real and short. The seam
  is not part of the public API; each language exposes it only to its own
  runner (Go `internal/`, Java package-private, and so on).
- A mock server (planned: one small Go program in `conformance/mock`) replays
  a vector's exchanges, checks each request against the expectation and
  injects the scripted faults. It serves HTTPS with a throwaway CA, so bearer
  vectors run under the same HTTPS-only rule as production.
- Each SDK has a conformance runner that loads every vector, builds a client
  from `config`, runs the vector's calls against the mock, and compares the
  outcome with `expect`. A vector failure fails CI.
- Required retry vectors: 503, 503, 200; a network error then 200; a burst
  429 with `Retry-After: 1` (retried); a burst 429 with `Retry-After: 60`
  (not retried under the default deadline); a daily-quota 429; 401; deadline
  exceeded; fail-closed returns `false`; the breaker opens and recovers.
- CI runs on GitHub-hosted `ubuntu-latest` runners only, one workflow per
  language with path filters, `permissions: contents: read` and actions
  pinned by commit SHA. Each SDK is tested on its supported runtime versions.
  C# is verified in CI only, because the coordinator host has no .NET.
- Scheduled smoke test, weekly and on demand: one allowed and one denied call
  per SDK against beta (`https://b.speedle.io`), using a beta-only test token
  from an encrypted secret. It is added once the Cloud ADS cutover is live on
  beta and the owner provides the token. It never runs on pull requests from
  forks.

## 12. Repository layout, packages, versioning and releases

Layout: `node/`, `python/`, `java/`, `go/`, `dotnet/`, `rust/`,
`conformance/`, `contract/`, `docs/`. Each language has its own directory, PR
and CI workflow.

License: UPL-1.0, the same text and copyright notice as speedle-plus, in the
root `LICENSE`. Every package declares the SPDX identifier `UPL-1.0` in its
metadata and ships the license file.

### 12.1 Package names

Names are based on `speedle`; where a registry needs a domain it is
`speedle.io`. Availability was checked read-only on 2026-11-05 through the
public registry APIs.

| Registry | Package | Import or namespace | Availability (2026-11-05) |
| --- | --- | --- | --- |
| npm | `@speedle/sdk` | `import { SpeedleClient } from "@speedle/sdk"` | Free: `@speedle/sdk` returns 404 and the `speedle` scope does not exist (`/-/org/speedle/package`: "Scope not found"). The unscoped name `speedle` is taken by an unrelated package (a download tool, v1.0.8), so the scope is required. |
| PyPI | `speedle` | `import speedle` | Free: `/pypi/speedle/json` returns 404 (`speedle-sdk` is also free). |
| Maven Central | groupId `io.speedle`, artifactId `speedle-sdk` | Java package and JPMS module `io.speedle.sdk` | Namespace `io.speedle` is verified for the owner (comment 6648469082); Central search finds no artifacts under it. |
| NuGet | `Speedle.Sdk` | `namespace Speedle.Sdk` | Free: registration for `speedle.sdk` returns 404; a search for `speedle` finds no packages. |
| crates.io | `speedle` | `use speedle::Client` | Free: `/api/v1/crates/speedle` returns 404 (`speedle-sdk` is also free). |
| Go | `github.com/teramoby/speedle-sdk/go` | `package speedle` | No versions on `proxy.golang.org` yet, as expected; the repository path needs no registration. |

Go has two options, decided before the first `go/v0.1.0` tag because the
module path is written into `go.mod` and every import:

- **Repository path (recommended default):** `github.com/teramoby/speedle-sdk/go`.
  Nothing to set up; tags `go/vX.Y.Z`.
- **Vanity path (optional):** `speedle.io/sdk/go`. `speedle.io` is served by
  GitHub Pages, and `https://speedle.io/sdk?go-get=1` returns 404 today. The
  site would serve `<meta name="go-import" content="speedle.io/sdk git https://github.com/teramoby/speedle-sdk">`
  at both `/sdk/` and `/sdk/go/`; `go.mod` would say
  `module speedle.io/sdk/go`; the tags stay `go/vX.Y.Z`. No DNS change is
  needed. If the site breaks, `go get` breaks for new users, so the page
  becomes production infrastructure.

### 12.2 Owner actions per registry

| Registry | Owner action |
| --- | --- |
| npm | Create the npm organization `speedle` (free for public packages) on the owner's npm account with 2FA. That reserves the `@speedle` scope. Configure trusted publishing (GitHub Actions OIDC, repository `teramoby/speedle-sdk`, the release workflow) for `@speedle/sdk`; if npm requires the package to exist first, provide one short-lived granular token for the first release. |
| PyPI | A PyPI account with 2FA. Add a pending trusted publisher for project `speedle` (repository `teramoby/speedle-sdk`, release workflow, environment). Optionally request a PyPI organization `speedle`. |
| Maven Central | Done: namespace `io.speedle` verified. At release time, a Central Portal user token and a GPG signing key as GitHub Actions secrets in `teramoby/speedle-sdk` (names listed in the release-automation PR). |
| NuGet | A nuget.org account and an organization `speedle`. Request the ID prefix reservation `Speedle` from the nuget.org team, which marks `Speedle.*` packages as verified and blocks others from using the prefix. Configure trusted publishing for the repository if available, otherwise an API key scoped to `Speedle.*` in a protected environment. |
| crates.io | A crates.io account (GitHub login, verified email). crates.io has no prefix reservation; the first publish claims `speedle`. Configure trusted publishing for the repository, or a scoped API token in a protected environment. Optionally add a `teramoby` GitHub team as co-owner. |
| Go | Repository path: nothing. Vanity path, only if chosen: add the two static pages on the speedle.io GitHub Pages site. |

### 12.3 Versioning and releases

- Each SDK has its own semantic version, starting at `0.1.0`. Breaking API
  changes before `1.0.0` bump the minor version.
- Releases come only from tags on the default branch, one tag prefix per SDK:
  `node/v0.1.0`, `python/v0.1.0`, `java/v0.1.0`, `go/v0.1.0`,
  `dotnet/v0.1.0`, `rust/v0.1.0`. The Go prefix is what Go modules in a
  subdirectory require.
- A release workflow (not part of this phase) starts on a tag push, checks
  that the tagged commit is on the default branch, runs that SDK's tests and
  conformance, and publishes behind a protected environment that needs owner
  approval. Registry OIDC trusted publishing is preferred over stored tokens
  wherever the registry supports it. Publishing waits for the owner actions
  in §12.2.
- Each SDK keeps a `CHANGELOG.md`; commits follow Conventional Commits with
  the SDK as scope, for example `feat(python): ...`.

## 13. Docs plan

- Each package README: install, a quickstart for Speedle Cloud and for
  Speedle+, configuration, errors, retries, failure mode, breaker, hooks and
  the security notes.
- API reference from the source: TypeDoc, a Python doc site (tool to be
  chosen), Javadoc, pkg.go.dev, XML docs for NuGet, and docs.rs.
- The Speedle Cloud console docs get a quickstart per language in English and
  zh-CN, through a separate PR in teramoby/cloud.speedle.io.
- The READMEs of speedle-sdk, speedle-plus and cloud.speedle.io link to each
  other; the links in the other two repositories are separate PRs there.

## 14. Remaining owner actions

1. Registry accounts and reservations in §12.2 (npm org `speedle`, PyPI
   pending publisher, NuGet organization and `Speedle` prefix, crates.io
   account). None block implementation; they block publishing.
2. Go module path: keep the repository path (default if no answer) or choose
   the `speedle.io/sdk/go` vanity path before the first Go tag.
3. Release credentials as GitHub Actions secrets or environments, listed in
   the release-automation PR.
4. A beta-only test token for the smoke test, once the Cloud ADS cutover is
   live on beta.
