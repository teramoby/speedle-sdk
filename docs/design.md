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

Batch 1: Node.js/TypeScript, Python, Java, Go. Batch 2 (planned only): C#,
Rust. Batch 2 follows the same API shape, error model and conformance suite.

Out of scope for Batch 1: the other ADS operations (`all-granted-roles`,
`all-granted-permissions`, `diagnose`, `discover`), PMS, gRPC, browser use
and Speedle+ token assertion (`subject.token`/`tokenType`, see §3.3).

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

"SDK" in the last column describes what every Batch 1 SDK does.

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
| `subject.token`, `subject.tokenType` | Non-empty value returns 400 `unsupported_token_assertion` | Used by the configured token asserter | Never sent in Batch 1. Speedle+ token assertion may come later as a Speedle+-only option. |
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
| `requestContext`, `attributes`, `grantedRoles`, `rolePolicies`, `policies` | Optional in the schema (diagnostic shape); not in the examples | Never returned by `is-allowed` | Ignored. Not exposed in Batch 1. |
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
| 429 | `rate_limited` (per token, 600 per 60 s) or `daily_quota_exceeded` (account-wide, 10,000 per UTC day); `Retry-After` required | Not produced | `RateLimitedError` or `QuotaExceededError`, both with `retryAfter` | No |
| 500 | `internal_error`; charged when after handler entry | Unexpected errors. Swagger says evaluation failures are 500, but the handler returns them as 200 with `errorMessage` | `ServerError` | Yes |
| 503 | `quota_unavailable` (not charged) or `snapshot_unavailable` (charged) | Not produced (a proxy may) | `ServiceUnavailableError` | Yes |
| Other 5xx | Not declared (edge or proxy) | Not declared (proxy) | `ServerError` | Yes |
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
400, 403, policy denials, 500 and `snapshot_unavailable` 503. Retries of a
charged 5xx therefore cost extra units, at most `maxAttempts` per call. The
docs state this, and `maxAttempts: 1` turns retries off.

## 4. Generated core or hand-written clients

| | Generated core plus idiomatic layer | Hand-written thin client |
| --- | --- | --- |
| Fit with the contract | The Cloud schema is OpenAPI 3.1 with `anyOf`/`null` and recursive `$defs`; common generators handle these poorly and emit awkward types. Speedle+ has no OpenAPI 3.1 contract, and its attribute format differs, so its path would be hand-written anyway. | One operation, written once per language against both backends. |
| Dependencies | Generator runtimes pull in HTTP and JSON libraries per language (for example OkHttp/Gson in Java), which collide with user dependencies. | Standard library HTTP in Node, Java and Go; `httpx` in Python. |
| Idiomatic API, retries, typed errors | Still hand-written as the layer on top. | Hand-written. |
| Cost of a contract change | Regenerate, review generated diffs, keep the generator version and templates pinned per language, then adjust the layer. | Edit roughly 50 to 100 lines per SDK, update vectors. |
| Ongoing cost | Generator upgrades and template patches in 4, later 6, languages. | Roughly 600 to 900 lines per SDK including tests, mostly retry, errors and transport. |
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

- `isAllowed(...)` returns a boolean. It returns `true` only for an HTTP 200
  whose body parses and has `allowed: true`. Any other outcome raises a typed
  error, so callers fail closed.
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

export interface RetryOptions { maxAttempts?: number; baseDelayMs?: number; maxDelayMs?: number }
export interface ClientOptions {
  backend: Backend;
  endpoint: string;              // base URL, e.g. "https://cloud.speedle.io"
  auth: Auth;
  serviceName?: string;          // required for "speedle-plus"
  timeoutMs?: number;            // per attempt, default 2000
  retry?: RetryOptions;          // default { maxAttempts: 3, baseDelayMs: 100, maxDelayMs: 1000 }
  caCert?: string | Buffer;      // extra trust anchor for self-hosted servers
  allowInsecureHttp?: boolean;   // default false
  userAgentSuffix?: string;
}
export interface CallOptions { signal?: AbortSignal }

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

Transport: `node:https` with a keep-alive `Agent`; no runtime dependencies.
The package declares Node only and is not built for browsers.

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
class RetryPolicy:
    max_attempts: int = 3
    base_delay: float = 0.1   # seconds
    max_delay: float = 1.0

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
                 service_name: str | None = None, timeout: float = 2.0,
                 retry: RetryPolicy = RetryPolicy(), ca_file: str | None = None,
                 allow_insecure_http: bool = False,
                 user_agent_suffix: str | None = None) -> None: ...
    def is_allowed(self, subject: Subject, action: str, resource: str,
                   attributes: Mapping[str, AttributeValue] | None = None) -> bool: ...
    def check(self, subject: Subject, action: str, resource: str,
              attributes: Mapping[str, AttributeValue] | None = None) -> Decision: ...
    def close(self) -> None: ...
    def __enter__(self) -> "Client": ...
    def __exit__(self, *exc: object) -> None: ...

class AsyncClient:
    # Same constructor; the methods are coroutines.
    async def is_allowed(self, subject: Subject, action: str, resource: str,
                         attributes: Mapping[str, AttributeValue] | None = None) -> bool: ...
    async def check(self, subject: Subject, action: str, resource: str,
                    attributes: Mapping[str, AttributeValue] | None = None) -> Decision: ...
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

### 5.3 Java (17 or later)

```java
package <namespace to be decided>;

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

public record RetryPolicy(int maxAttempts, Duration baseDelay, Duration maxDelay) {
  public static RetryPolicy defaults();      // 3, 100 ms, 1 s
  public static RetryPolicy disabled();      // maxAttempts = 1
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
  public Decision check(Subject subject, String action, String resource,
                        Map<String, ?> attributes);
  @Override public void close();

  public static final class Builder {
    public Builder backend(Backend backend);
    public Builder endpoint(URI endpoint);
    public Builder auth(Auth auth);
    public Builder serviceName(String serviceName);
    public Builder timeout(Duration perAttempt);   // default 2 s
    public Builder retry(RetryPolicy retry);
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
make the blocking call cheap on Java 21.

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

type RetryPolicy struct {
	MaxAttempts int           // default 3; 1 disables retries
	BaseDelay   time.Duration // default 100ms
	MaxDelay    time.Duration // default 1s
}

type Config struct {
	Backend           Backend
	Endpoint          string // base URL
	Auth              Auth
	ServiceName       string        // required for BackendSpeedlePlus
	Timeout           time.Duration // per attempt, default 2s
	Retry             RetryPolicy
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

Transport: `net/http` with its own `http.Transport`; no third-party
dependencies.

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

`Auth` is a closed type with three built-in implementations in Batch 1. It
is an interface or factory in every language, so another implementation (for
example a custom gateway header) can be added later without breaking the
API.

| Implementation | Use | Behaviour |
| --- | --- | --- |
| `bearer(token)` | Speedle Cloud; Speedle+ behind an authenticating gateway | Sends `Authorization: Bearer <token>`. Rejects empty tokens and tokens with whitespace or control characters. It does not check the `spdl_` format, so a future token format keeps working. Requires HTTPS. |
| `none()` | Speedle+ on a trusted network or loopback | Sends no credential. |
| `mutualTls(...)` | Speedle+ with `--client-cert` and `--force-client-cert=true` | Presents the client certificate on the TLS handshake. Server verification stays on. |

Server trust uses the system roots plus an optional extra CA (`caCert`,
`ca_file`, `trustStore`, `RootCAs`). There is no option to disable certificate
verification.

### 6.3 Timeouts and connection reuse

- One timeout per attempt, covering connect, TLS, request and response, with
  a default of 2 seconds. A decision call sits on the caller's request path,
  so the default is short.
- The total time of one call is bounded by
  `maxAttempts × timeout + retry delays`. Go `context.Context`, Node
  `AbortSignal` and Python async cancellation stop the call, including during
  a retry delay.
- Each client owns one keep-alive connection pool (Node `https.Agent` with
  `keepAlive`, `httpx` pool, Java `HttpClient`, Go `http.Transport`). Clients
  are thread-safe and meant to be created once per process. `close` releases
  the pool.

### 6.4 Retries

- Retried: network errors (DNS failure, connection refused or reset, timeout
  of one attempt) and any 5xx status.
- Not retried: every 4xx, including both 429 codes; TLS certificate or
  handshake failures; client-side validation errors; responses with a 200
  status but an unexpected body.
- Bounded: `maxAttempts` counts the first attempt; default 3, `1` disables
  retries.
- Jitter: "full jitter". Before retry `n` (1-based) the SDK sleeps a random
  duration in `[0, min(maxDelay, baseDelay × 2^(n-1))]`, defaults 100 ms and
  1 s.
- A 5xx with a `Retry-After` header waits at least that long if it fits
  within `maxDelay`; otherwise the SDK stops and raises the error.
- `is-allowed` has no side effects other than the Cloud daily charge, so
  retrying it is safe. The charge cost is documented (§3.6).

## 7. Typed errors

All SDK errors share one base type. Each error carries `status` (HTTP status
or empty), `code` (Cloud error code or empty), `message`, and
`quotaDailyRemaining` when the header was present. Messages never contain the
token, request attributes or the full request body.

| Error | When | TypeScript / Python / Java | Go |
| --- | --- | --- | --- |
| Base | Any SDK error | `SpeedleError` / `SpeedleError` / `SpeedleException` | `*speedle.Error` |
| Configuration | Invalid client options | `ConfigurationError` | `ErrConfiguration` |
| Invalid request | 400; Speedle+ attribute conversion before sending | `InvalidRequestError` | `ErrInvalidRequest` |
| Unauthorized | 401 | `UnauthorizedError` | `ErrUnauthorized` |
| Forbidden | 403, including `service_mismatch` | `ForbiddenError` | `ErrForbidden` |
| Request too large | 413 | `RequestTooLargeError` | `ErrRequestTooLarge` |
| Rate limited | 429 `rate_limited` | `RateLimitedError`, with `retryAfter` | `ErrRateLimited` |
| Daily quota exceeded | 429 `daily_quota_exceeded` | `QuotaExceededError`, with `retryAfter` | `ErrQuotaExceeded` |
| Service unavailable | 503 | `ServiceUnavailableError` | `ErrServiceUnavailable` |
| Server error | 500 and other 5xx | `ServerError` | `ErrServer` |
| HTTP error | Any other status, including 3xx and 404 | `HttpError` | `ErrHTTP` |
| Unexpected response | Body is not the expected JSON | `UnexpectedResponseError` | `ErrUnexpectedResponse` |
| Network | Transport failure or timeout after the last attempt | `NetworkError` (`TimeoutError` subtype) | `ErrNetwork`, `ErrTimeout` |

`RateLimitedError` and `QuotaExceededError` share a parent
(`TooManyRequestsError`) so callers can handle both. `retryAfter` is a
duration in seconds (`number` in TypeScript, `float` in Python, `Duration` in
Java, `time.Duration` in Go). A 429 with an unknown code is a
`TooManyRequestsError`. In Go, `*speedle.Error` has `Kind`, `Status`, `Code`,
`Message`, `RetryAfter` and `QuotaDailyRemaining` fields; the `Err*` values
are sentinels for `errors.Is`, and `errors.As` gives the details. After the
last retry the SDK raises the error of the last attempt.

## 8. User-Agent, telemetry and logging

- `User-Agent: speedle-sdk-<language>/<sdk version> (<runtime>/<version>; <os>)`,
  for example `speedle-sdk-go/0.1.0 (go/1.26.1; linux)`. `userAgentSuffix` is
  appended after a space.
- No telemetry: the SDK sends nothing except the `is-allowed` request, adds no
  tracking headers and makes no other network calls.
- No logging by default. An optional debug hook receives method, URL path,
  status, attempt number and duration, never headers, tokens, subjects or
  attributes.

## 9. Decision caching

Off, and not offered in Batch 1. Cloud returns `Cache-Control: no-store`.
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
  `toString`, `repr`, debug output and error messages.
- Redirects are never followed, so the `Authorization` header cannot leak to
  another host.
- TLS verification cannot be disabled.
- Subjects and attributes must come from the application's own authenticated
  context, not from untrusted client input. The docs say so, and repeat the
  Speedle+ warning about unauthenticated plaintext endpoints.
- Fail closed: no error path returns `true`.
- Tokens used in CI live only in GitHub encrypted secrets, are scoped to the
  beta smoke test and are never printed.

## 11. Conformance suite and CI

- `conformance/vectors/*.json`: language-neutral test vectors in the format of
  `conformance/schema.json`; see `conformance/README.md`.
- A mock server (planned: one small Go program in `conformance/mock`) replays
  a vector's exchanges, checks each request against the expectation and can
  inject faults such as a connection reset.
- Each SDK has a conformance runner that loads every vector, builds a client
  from `config`, runs `call` against the mock, and compares the outcome with
  `expect`. A vector failure fails CI.
- CI runs on GitHub-hosted `ubuntu-latest` runners only, with
  `permissions: contents: read`, actions pinned by commit SHA, and per-language
  path filters. Each SDK is tested on its supported runtime versions.
- Scheduled smoke test, weekly and on demand: one allowed and one denied call
  per SDK against beta (`https://b.speedle.io`), using a beta-only test token
  from an encrypted secret. It is added once the Cloud ADS cutover is live on
  beta and the owner provides the token. It never runs on pull requests from
  forks.

## 12. Repository layout, versioning and releases

Planned layout: `node/`, `python/`, `java/`, `go/`, `conformance/`,
`contract/`, `docs/`. Batch 2 adds `dotnet/` and `rust/`.

- Each SDK has its own semantic version, starting at `0.1.0`. Breaking API
  changes before `1.0.0` bump the minor version.
- Releases come only from tags on the default branch, one tag prefix per SDK:
  `node/v0.1.0`, `python/v0.1.0`, `java/v0.1.0`, `go/v0.1.0`. The Go prefix is
  what Go modules in a subdirectory require.
- A release workflow (not part of this phase) starts on a tag push, checks
  that the tagged commit is on the default branch, runs that SDK's tests and
  conformance, and publishes behind a protected environment that needs owner
  approval. Registry OIDC trusted publishing (npm, PyPI) is preferred over
  stored tokens where the registry supports it.
- Each SDK keeps a `CHANGELOG.md`; commits follow Conventional Commits with
  the SDK as scope, for example `feat(python): ...`.

## 13. Docs plan

- Each package README: install, a quickstart for Speedle Cloud and for
  Speedle+, configuration, errors, retries and the security notes.
- API reference from the source: TypeDoc, a Python doc site (tool to be
  chosen), Javadoc, and pkg.go.dev.
- The Speedle Cloud console docs get a quickstart per language in English and
  zh-CN, through a separate PR in teramoby/cloud.speedle.io.
- The READMEs of speedle-sdk, speedle-plus and cloud.speedle.io link to each
  other; the links in the other two repositories are separate PRs there.

## 14. Open questions for the owner

1. Package names and namespaces: npm, PyPI, Maven Central group id (for
   example `io.speedle`, which needs speedle.io domain verification), Go
   module path (for example `github.com/teramoby/speedle-sdk/go`), and later
   NuGet and crates.io.
2. Which accounts own and publish each package, and how release credentials
   are stored (trusted publishing where possible, otherwise environment
   secrets).
3. Whether Batch 1 stays Node.js/TypeScript, Python, Java and Go.
