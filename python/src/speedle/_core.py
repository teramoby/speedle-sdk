"""Logic shared by the sync and async clients: options, request, response and the retry loop."""

from __future__ import annotations

import json
import math
import platform
import re
import ssl
import sys
from collections.abc import Callable, Generator, Mapping, Sequence
from dataclasses import dataclass
from typing import Any, Literal, Union
from urllib.parse import urlsplit

from . import _errors as E
from ._attributes import Attributes, to_wire_attributes
from ._auth import Auth
from ._breaker import Breaker, Clock, Outcome, Ticket
from ._types import (
    AttemptEvent,
    Backend,
    BreakerState,
    BreakerStateChangeEvent,
    CircuitBreaker,
    Decision,
    FailureMode,
    GiveUpEvent,
    GiveUpReason,
    Hooks,
    Principal,
    Reason,
    RetryEvent,
    RetryPolicy,
    Subject,
)
from ._version import __version__

IS_ALLOWED_PATH = "/authz-check/v1/is-allowed"
MAX_RESPONSE_BYTES = 1024 * 1024
_MAX_MESSAGE = 512
_CONTROL = re.compile(r"[\x00-\x1f\x7f]")
_LOOPBACK_V4 = re.compile(r"^127\.\d{1,3}\.\d{1,3}\.\d{1,3}$")
_DIGITS = re.compile(r"^[0-9]+$")

CLOUD_CODES = frozenset(
    {
        "invalid_request",
        "unsupported_token_assertion",
        "unauthorized",
        "service_mismatch",
        "request_too_large",
        "rate_limited",
        "daily_quota_exceeded",
        "internal_error",
        "quota_unavailable",
        "snapshot_unavailable",
    }
)


def _fail(message: str) -> E.ConfigurationError:
    return E.ConfigurationError(message)


def _number(value: object, name: str, minimum: float, integer: bool = False, strict: bool = False) -> None:
    ok = isinstance(value, int) if integer else isinstance(value, (int, float))
    if isinstance(value, bool) or not ok:
        raise _fail(f"{name} must be {'an int' if integer else 'a number'}")
    v = float(value)  # type: ignore[arg-type]
    if not math.isfinite(v) or v < minimum or (strict and v <= minimum):
        raise _fail(f"{name} must be {'>' if strict else '>='} {minimum}")


@dataclass(frozen=True)
class Config:
    """Validated client options. Internal."""

    backend: Backend
    url: str
    is_https: bool
    ssl_context: ssl.SSLContext | None
    authorization: str | None
    service_name: str
    retry: RetryPolicy
    failure_mode: FailureMode
    breaker: CircuitBreaker | None
    hooks: Hooks
    user_agent: str


def resolve_config(
    *,
    backend: object,
    endpoint: object,
    auth: object,
    service_name: object,
    retry: object,
    failure_mode: object,
    circuit_breaker: object,
    hooks: object,
    ca_file: object,
    allow_insecure_http: object,
    user_agent_suffix: object,
) -> Config:
    if backend not in ("speedle-cloud", "speedle"):
        raise _fail('backend must be "speedle-cloud" or "speedle"')
    if not isinstance(auth, Auth):
        raise _fail("auth must come from Auth.none(), Auth.bearer() or Auth.mutual_tls()")
    if backend == "speedle-cloud" and auth.kind != "bearer":
        raise _fail("speedle-cloud needs Auth.bearer()")
    if not isinstance(service_name, str) or service_name == "":
        raise _fail("service_name is required")
    if not isinstance(endpoint, str) or endpoint == "":
        raise _fail("endpoint is required")
    if "?" in endpoint or "#" in endpoint:
        raise _fail("endpoint must not have a query or fragment")
    try:
        parts = urlsplit(endpoint)
        port = parts.port
    except ValueError as exc:
        raise _fail("endpoint is not a valid URL") from exc
    host = parts.hostname
    if not host:
        raise _fail("endpoint has no host")
    if parts.username is not None or parts.password is not None:
        raise _fail("endpoint must not contain credentials")
    scheme = parts.scheme.lower()
    if scheme == "http":
        if backend != "speedle" or auth.kind != "none":
            raise _fail('http:// is accepted only for backend "speedle" with Auth.none()')
        loopback = host in {"localhost", "::1"} or bool(_LOOPBACK_V4.match(host))
        if not loopback and allow_insecure_http is not True:
            raise _fail("http:// is accepted only for loopback hosts unless allow_insecure_http is set")
    elif scheme != "https":
        raise _fail("endpoint must use https://")
    netloc = f"[{host}]" if ":" in host else host
    if port is not None:
        netloc += f":{port}"
    url = f"{scheme}://{netloc}{parts.path.rstrip('/')}{IS_ALLOWED_PATH}"

    if not isinstance(retry, RetryPolicy):
        raise _fail("retry must be a RetryPolicy")
    _number(retry.max_attempts, "retry.max_attempts", 1, integer=True)
    _number(retry.base_delay, "retry.base_delay", 0)
    _number(retry.max_delay, "retry.max_delay", 0)
    _number(retry.attempt_timeout, "retry.attempt_timeout", 0, strict=True)
    _number(retry.deadline, "retry.deadline", 0, strict=True)
    if failure_mode not in ("error", "closed"):
        raise _fail('failure_mode must be "error" or "closed"')
    if circuit_breaker is not None:
        if not isinstance(circuit_breaker, CircuitBreaker):
            raise _fail("circuit_breaker must be a CircuitBreaker")
        _number(circuit_breaker.failure_threshold, "circuit_breaker.failure_threshold", 1, integer=True)
        _number(circuit_breaker.cool_down, "circuit_breaker.cool_down", 0, strict=True)
    if hooks is None:
        hooks = Hooks()
    if not isinstance(hooks, Hooks):
        raise _fail("hooks must be a Hooks")
    if user_agent_suffix is not None and (not isinstance(user_agent_suffix, str) or _CONTROL.search(user_agent_suffix)):
        raise _fail("user_agent_suffix must be a str without control characters")
    if ca_file is not None and (not isinstance(ca_file, str) or ca_file == ""):
        raise _fail("ca_file must be a path")

    ctx: ssl.SSLContext | None = None
    if scheme == "https":
        try:
            ctx = ssl.create_default_context()
            if ca_file is not None:
                ctx.load_verify_locations(cafile=ca_file)
            if auth.kind == "mutual-tls":
                ctx.load_cert_chain(auth._cert_file or "", auth._key_file)
        except (OSError, ssl.SSLError) as exc:
            raise _fail(f"cannot load TLS files: {type(exc).__name__}") from None
    elif ca_file is not None:
        raise _fail("ca_file needs an https:// endpoint")

    user_agent = f"speedle-sdk-python/{__version__} (python/{platform.python_version()}; {sys.platform})"
    if user_agent_suffix:
        user_agent += f" {user_agent_suffix}"
    return Config(
        backend=backend,
        url=url,
        is_https=scheme == "https",
        ssl_context=ctx,
        authorization=f"Bearer {auth._token}" if auth.kind == "bearer" else None,
        service_name=service_name,
        retry=retry,
        failure_mode=failure_mode,
        breaker=circuit_breaker,
        hooks=hooks,
        user_agent=user_agent,
    )


def build_body(
    subject: Subject, action: object, resource: object, attributes: Attributes | None, service_name: object
) -> bytes:
    if not isinstance(action, str):
        raise E.InvalidRequestError("action must be a str")
    if not isinstance(resource, str):
        raise E.InvalidRequestError("resource must be a str")
    if not isinstance(service_name, str) or service_name == "":
        raise E.InvalidRequestError("service_name is empty")
    principals: list[dict[str, str]] = []
    if isinstance(subject, str):
        if subject == "":
            raise E.InvalidRequestError("subject is empty")
        principals.append({"type": "user", "name": subject})
    elif isinstance(subject, Sequence) and len(subject) > 0:
        for p in subject:
            if not isinstance(p, Principal):
                raise E.InvalidRequestError("subject items must be Principal values")
            if not isinstance(p.name, str) or p.name == "":
                raise E.InvalidRequestError("principal name is empty")
            if not isinstance(p.type, str) or p.type == "":
                raise E.InvalidRequestError("principal type must be a non-empty str")
            if p.idd is not None and not isinstance(p.idd, str):
                raise E.InvalidRequestError("principal idd must be a str")
            item = {"type": p.type, "name": p.name}
            if p.idd:
                item["idd"] = p.idd
            principals.append(item)
    else:
        raise E.InvalidRequestError("subject must be a user name or a non-empty list of principals")
    wire = to_wire_attributes(attributes)
    body: dict[str, object] = {
        "subject": {"principals": principals},
        "serviceName": service_name,
        "resource": resource,
        "action": action,
    }
    if wire:
        body["attributes"] = wire
    return json.dumps(body, ensure_ascii=False, allow_nan=False, separators=(",", ":")).encode("utf-8")


@dataclass(frozen=True)
class RawResponse:
    status: int
    headers: Mapping[str, str]  # lower-case names
    body: bytes


@dataclass(frozen=True)
class TransportFailure:
    kind: Literal["network", "tls", "timeout", "too_large"]
    message: str


@dataclass(frozen=True)
class Send:
    budget: float
    body: bytes


@dataclass(frozen=True)
class Sleep:
    delay: float


@dataclass(frozen=True)
class GaveUp:
    error: E.SpeedleError


Step = Union[Send, Sleep]
Result = Union[Decision, GaveUp]


def _non_negative_int(value: str | None, positive: bool) -> int | None:
    if value is None:
        return None
    v = value.strip()
    if not _DIGITS.match(v):
        return None
    n = int(v)
    if positive and n == 0:
        return None
    return n


@dataclass(frozen=True)
class Classified:
    error: E.SpeedleError
    retryable: bool
    retry_after: float | None


def classify(backend: Backend, res: RawResponse) -> Decision | Classified:
    quota = _non_negative_int(res.headers.get("x-quota-daily-remaining"), False)
    status = res.status
    try:
        parsed: Any = json.loads(res.body.decode("utf-8"))
    except (ValueError, UnicodeDecodeError):
        parsed = None

    if status == 200:

        def bad(why: str) -> Classified:
            err = E.UnexpectedResponseError(
                f"unexpected response body: {why}", status=status, quota_daily_remaining=quota
            )
            return Classified(err, False, None)

        if not isinstance(parsed, dict):
            return bad("not a JSON object")
        allowed = parsed.get("allowed")
        if not isinstance(allowed, bool):
            return bad('"allowed" is not a boolean')
        reason = parsed.get("reason")
        if isinstance(reason, bool) or not isinstance(reason, int):
            return bad('"reason" is not an integer')
        msg = parsed.get("errorMessage")
        try:
            known: Reason | int = Reason(reason)
        except ValueError:
            known = reason
        return Decision(
            allowed=allowed,
            reason=known,
            error_message=msg if isinstance(msg, str) and msg else None,
            quota_daily_remaining=quota,
        )

    text = parsed.get("error") if isinstance(parsed, dict) else None
    code: str | None = None
    detail: str | None = None
    if isinstance(text, str):
        if backend == "speedle-cloud" and text in CLOUD_CODES:
            code = text
        else:
            detail = text if len(text) <= _MAX_MESSAGE else text[:_MAX_MESSAGE] + "..."
    message = f"HTTP {status}" + (f": {code}" if code else f": {detail}" if detail else "")
    ra = _non_negative_int(res.headers.get("retry-after"), True)
    retry_after = float(ra) if ra is not None else None
    kw: dict[str, Any] = {"status": status, "code": code, "quota_daily_remaining": quota}

    def final(err: E.SpeedleError) -> Classified:
        return Classified(err, False, None)

    def retry(err: E.SpeedleError) -> Classified:
        return Classified(err, True, retry_after)

    if status == 400:
        return final(E.InvalidRequestError(message, **kw))
    if status == 401:
        return final(E.UnauthorizedError(message, **kw))
    if status == 403:
        return final(E.ForbiddenError(message, **kw))
    if status == 413:
        return final(E.RequestTooLargeError(message, **kw))
    if status == 429:
        if backend == "speedle" or code == "rate_limited":
            rl = E.RateLimitedError(message, retry_after=retry_after, **kw)
            # A burst 429 is retried only with a usable Retry-After (design §6.3).
            return final(rl) if retry_after is None else retry(rl)
        if code == "daily_quota_exceeded":
            return final(E.QuotaExceededError(message, retry_after=retry_after, **kw))
        return final(E.TooManyRequestsError(message, retry_after=retry_after, **kw))
    if status == 500:
        return final(E.ServerError(message, **kw))
    if status in (502, 504):
        return retry(E.ServerError(message, **kw))
    if status == 503:
        return retry(E.ServiceUnavailableError(message, **kw))
    if 500 <= status <= 599:
        return final(E.ServerError(message, **kw))
    return final(E.HttpError(message, **kw))


def _call_hook(hook: Callable[[Any], object] | None, event: object) -> None:
    if hook is None:
        return
    try:
        result = hook(event)
        close = getattr(result, "close", None)
        if close is not None and hasattr(result, "__await__"):
            close()  # an un-awaited coroutine; never run it
    except Exception:  # noqa: BLE001, S110 - hook failures never change the result (design §6.7)
        pass


def _breaker_outcome(error: E.SpeedleError) -> Outcome:
    if isinstance(error, E.NetworkError):
        return "failure"
    if error.status is not None:
        return "failure" if error.status >= 500 else "success"
    return "neutral"


class Engine:
    """The retry loop of design §6.3 as a generator of I/O steps. Internal."""

    def __init__(self, config: Config, clock: Clock, rand: Callable[[], float]) -> None:
        self.config = config
        self.clock = clock
        self.random = rand
        hooks = config.hooks
        self.breaker: Breaker | None = None
        if config.breaker is not None:

            def on_change(from_state: BreakerState, to_state: BreakerState) -> None:
                _call_hook(hooks.on_breaker_state_change, BreakerStateChangeEvent(from_state, to_state))

            self.breaker = Breaker(config.breaker.failure_threshold, config.breaker.cool_down, clock, on_change)
        self.headers = {
            "Content-Type": "application/json",
            "Accept": "application/json",
            "User-Agent": config.user_agent,
        }
        if config.authorization:
            self.headers["Authorization"] = config.authorization

    def _give_up(self, attempts: int, reason: GiveUpReason, error: E.SpeedleError, ticket: Ticket | None) -> GaveUp:
        _call_hook(self.config.hooks.on_give_up, GiveUpEvent(attempts, reason, error))
        if ticket is not None and self.breaker is not None:
            self.breaker.record(ticket, _breaker_outcome(error))
        return GaveUp(error)

    def run(
        self,
        subject: Subject,
        action: str,
        resource: str,
        attributes: Attributes | None,
        deadline: float | None,
        service_name: str | None,
    ) -> Generator[Step, RawResponse | TransportFailure | None, Result]:
        cfg = self.config
        policy = cfg.retry
        hooks = cfg.hooks
        clock = self.clock
        try:
            if deadline is not None:
                if isinstance(deadline, bool) or not isinstance(deadline, (int, float)) or not deadline > 0:
                    raise E.InvalidRequestError("deadline must be a positive number of seconds")
                if not math.isfinite(deadline):
                    raise E.InvalidRequestError("deadline must be finite")
            body = build_body(
                subject, action, resource, attributes, cfg.service_name if service_name is None else service_name
            )
        except E.InvalidRequestError as err:
            return self._give_up(0, "non_retryable", err, None)

        ticket: Ticket | None = None
        if self.breaker is not None:
            ticket = self.breaker.acquire()
            if ticket is None:
                return self._give_up(0, "circuit_open", E.CircuitOpenError("circuit breaker is open"), None)

        done = False
        try:
            deadline_at = clock.now() + (policy.deadline if deadline is None else float(deadline))
            attempts = 0
            for attempt in range(1, policy.max_attempts + 1):
                remaining = deadline_at - clock.now()
                if remaining <= 0:
                    done = True
                    return self._give_up(attempts, "deadline", E.DeadlineExceededError("deadline exceeded"), ticket)
                attempts = attempt
                deadline_bound = remaining <= policy.attempt_timeout
                started = clock.now()
                outcome = yield Send(min(policy.attempt_timeout, remaining), body)
                status: int | None = None
                deadline_cut = False
                retry_after: float | None = None
                if isinstance(outcome, RawResponse):
                    status = outcome.status
                    c = classify(cfg.backend, outcome)
                    if isinstance(c, Decision):
                        _call_hook(hooks.on_attempt, AttemptEvent(attempt, clock.now() - started, status, None))
                        done = True
                        if ticket is not None and self.breaker is not None:
                            self.breaker.record(ticket, "success")
                        return c
                    error, retryable, retry_after = c.error, c.retryable, c.retry_after
                else:
                    if not isinstance(outcome, TransportFailure):  # pragma: no cover - driver contract
                        raise TypeError("the driver must send a RawResponse or a TransportFailure")
                    if outcome.kind == "timeout":
                        deadline_cut = deadline_bound
                        error = (
                            E.DeadlineExceededError("deadline exceeded during the attempt")
                            if deadline_bound
                            else E.TimeoutError(outcome.message)
                        )
                        retryable = not deadline_bound
                    elif outcome.kind == "too_large":
                        error, retryable = E.UnexpectedResponseError(outcome.message), False
                    else:
                        error, retryable = E.NetworkError(outcome.message), outcome.kind == "network"
                error_type = type(error).__name__
                _call_hook(hooks.on_attempt, AttemptEvent(attempt, clock.now() - started, status, error_type))
                done = True
                if deadline_cut:
                    return self._give_up(attempts, "deadline", error, ticket)
                if not retryable:
                    return self._give_up(attempts, "non_retryable", error, ticket)
                if attempt == policy.max_attempts:
                    return self._give_up(attempts, "attempts_exhausted", error, ticket)
                delay = self.random() * min(policy.max_delay, policy.base_delay * 2 ** (attempt - 1))
                if retry_after is not None:
                    delay = max(delay, retry_after)
                if delay >= deadline_at - clock.now():
                    reason: GiveUpReason = "retry_after_exceeds_deadline" if retry_after is not None else "deadline"
                    return self._give_up(attempts, reason, error, ticket)
                done = False
                _call_hook(hooks.on_retry, RetryEvent(attempt + 1, delay, error_type))
                yield Sleep(delay)
            done = True
            raise AssertionError("unreachable")  # pragma: no cover
        finally:
            # Caller cancellation: no give-up hook, no breaker count (design §6.3).
            if not done and ticket is not None and self.breaker is not None:
                self.breaker.record(ticket, "neutral")
