"""Sync and async clients (design §5.2)."""

from __future__ import annotations

import asyncio
import ssl
from collections.abc import Callable, Generator
from typing import Any

import httpx

from . import _errors as E
from ._attributes import Attributes
from ._auth import Auth
from ._breaker import SYSTEM_CLOCK, SYSTEM_RANDOM, Clock
from ._core import (
    MAX_RESPONSE_BYTES,
    Config,
    Engine,
    GaveUp,
    RawResponse,
    Result,
    Send,
    Sleep,
    Step,
    TransportFailure,
    resolve_config,
)
from ._types import Backend, CircuitBreaker, Decision, FailureMode, Hooks, RetryPolicy, Subject

__all__ = ["AsyncClient", "Client"]


def _is_tls(exc: BaseException) -> bool:
    seen: set[int] = set()
    cur: BaseException | None = exc
    while cur is not None and id(cur) not in seen:
        if isinstance(cur, ssl.SSLError):
            return True
        seen.add(id(cur))
        cur = cur.__cause__ or cur.__context__
    return False


def _failure(exc: Exception) -> TransportFailure:
    if isinstance(exc, httpx.TimeoutException):
        return TransportFailure("timeout", "no complete response within the attempt timeout")
    if _is_tls(exc):
        return TransportFailure("tls", f"TLS failure ({type(exc).__name__})")
    return TransportFailure("network", f"connection failure ({type(exc).__name__})")


class _TooLarge(Exception):
    pass


def _headers(response: httpx.Response) -> dict[str, str]:
    return {k.lower(): v for k, v in response.headers.items()}


class _Base:
    _config: Config
    _engine: Engine

    def _setup(self, kwargs: dict[str, Any], clock: Clock, rand: Callable[[], float]) -> None:
        self._config = resolve_config(**kwargs)
        self._engine = Engine(self._config, clock, rand)
        self._closed = False

    def _httpx_options(self) -> dict[str, Any]:
        cfg = self._config
        return {
            "verify": cfg.ssl_context if cfg.ssl_context is not None else True,
            "follow_redirects": False,
            "trust_env": False,  # no proxies, netrc credentials or SSL_CERT_FILE from the environment
            "http2": False,
        }

    def _run(
        self,
        subject: Subject,
        action: str,
        resource: str,
        attributes: Attributes | None,
        deadline: float | None,
        service_name: str | None,
    ) -> Generator[Step, RawResponse | TransportFailure | None, Result]:
        if self._closed:
            raise E.SpeedleError("client is closed")
        return self._engine.run(subject, action, resource, attributes, deadline, service_name)

    def _bool(self, result: Result) -> bool:
        if isinstance(result, Decision):
            return result.allowed is True
        if self._config.failure_mode == "closed":
            return False
        raise result.error

    @staticmethod
    def _decision(result: Result) -> Decision:
        if isinstance(result, GaveUp):
            raise result.error
        return result

    def __repr__(self) -> str:
        return f"{type(self).__name__}({self._config.backend!r}, {self._config.url!r})"


_ARGS = (
    "backend",
    "endpoint",
    "auth",
    "service_name",
    "retry",
    "failure_mode",
    "circuit_breaker",
    "hooks",
    "ca_file",
    "allow_insecure_http",
    "user_agent_suffix",
)


class Client(_Base):
    """A blocking client for one ``is-allowed`` endpoint.

    Create it once and share it; it is thread-safe and owns one pooled
    ``httpx.Client``. Every call is bounded by the deadline.
    """

    def __init__(
        self,
        *,
        backend: Backend,
        endpoint: str,
        auth: Auth,
        service_name: str,
        retry: RetryPolicy = RetryPolicy(),  # noqa: B008 - frozen dataclass
        failure_mode: FailureMode = "error",
        circuit_breaker: CircuitBreaker | None = None,
        hooks: Hooks | None = None,
        ca_file: str | None = None,
        allow_insecure_http: bool = False,
        user_agent_suffix: str | None = None,
    ) -> None:
        values = locals()
        self._init({k: values[k] for k in _ARGS}, SYSTEM_CLOCK, SYSTEM_RANDOM)

    def _init(self, kwargs: dict[str, Any], clock: Clock, rand: Callable[[], float]) -> None:
        self._setup(kwargs, clock, rand)
        self._clock = clock
        self._http = httpx.Client(**self._httpx_options())

    def _send(self, step: Send) -> RawResponse | TransportFailure:
        try:
            with self._http.stream(
                "POST",
                self._config.url,
                content=step.body,
                headers=self._engine.headers,
                timeout=httpx.Timeout(step.budget),
            ) as response:
                chunks: list[bytes] = []
                size = 0
                for chunk in response.iter_bytes():
                    size += len(chunk)
                    if size > MAX_RESPONSE_BYTES:
                        raise _TooLarge
                    chunks.append(chunk)
                return RawResponse(response.status_code, _headers(response), b"".join(chunks))
        except _TooLarge:
            return TransportFailure("too_large", "response body too large")
        except (httpx.TransportError, ssl.SSLError, OSError) as exc:
            return _failure(exc)

    def _drive(self, gen: Generator[Step, RawResponse | TransportFailure | None, Result]) -> Result:
        try:
            step = next(gen)
            while True:
                if isinstance(step, Sleep):
                    self._clock.sleep(step.delay)
                    step = gen.send(None)
                else:
                    step = gen.send(self._send(step))
        except StopIteration as stop:
            result: Result = stop.value
            return result
        finally:
            gen.close()

    def is_allowed(
        self,
        subject: Subject,
        action: str,
        resource: str,
        attributes: Attributes | None = None,
        *,
        deadline: float | None = None,
        service_name: str | None = None,
    ) -> bool:
        """Returns the server's ``allowed`` value; see the README for errors and fail-closed mode."""
        return self._bool(self._drive(self._run(subject, action, resource, attributes, deadline, service_name)))

    def check(
        self,
        subject: Subject,
        action: str,
        resource: str,
        attributes: Attributes | None = None,
        *,
        deadline: float | None = None,
        service_name: str | None = None,
    ) -> Decision:
        """The detailed form. Always raises on failure, in every failure mode."""
        return self._decision(self._drive(self._run(subject, action, resource, attributes, deadline, service_name)))

    def close(self) -> None:
        """Releases the connection pool. The client must not be used afterwards."""
        self._closed = True
        self._http.close()

    def __enter__(self) -> Client:
        return self

    def __exit__(self, *exc: object) -> None:
        self.close()


class AsyncClient(_Base):
    """An asyncio client for one ``is-allowed`` endpoint.

    Same constructor as :class:`Client`. Task cancellation and
    ``asyncio.timeout()`` propagate as usual, also during a retry delay.
    """

    def __init__(
        self,
        *,
        backend: Backend,
        endpoint: str,
        auth: Auth,
        service_name: str,
        retry: RetryPolicy = RetryPolicy(),  # noqa: B008 - frozen dataclass
        failure_mode: FailureMode = "error",
        circuit_breaker: CircuitBreaker | None = None,
        hooks: Hooks | None = None,
        ca_file: str | None = None,
        allow_insecure_http: bool = False,
        user_agent_suffix: str | None = None,
    ) -> None:
        values = locals()
        self._init({k: values[k] for k in _ARGS}, SYSTEM_CLOCK, SYSTEM_RANDOM)

    def _init(self, kwargs: dict[str, Any], clock: Clock, rand: Callable[[], float]) -> None:
        self._setup(kwargs, clock, rand)
        self._clock = clock
        self._http = httpx.AsyncClient(**self._httpx_options())

    async def _send_once(self, step: Send) -> RawResponse:
        async with self._http.stream(
            "POST",
            self._config.url,
            content=step.body,
            headers=self._engine.headers,
            timeout=httpx.Timeout(step.budget),
        ) as response:
            chunks: list[bytes] = []
            size = 0
            async for chunk in response.aiter_bytes():
                size += len(chunk)
                if size > MAX_RESPONSE_BYTES:
                    raise _TooLarge
                chunks.append(chunk)
            return RawResponse(response.status_code, _headers(response), b"".join(chunks))

    async def _send(self, step: Send) -> RawResponse | TransportFailure:
        try:
            # wait_for bounds the whole attempt, not only each I/O phase.
            return await asyncio.wait_for(self._send_once(step), step.budget)
        except asyncio.TimeoutError:
            return TransportFailure("timeout", "no complete response within the attempt timeout")
        except _TooLarge:
            return TransportFailure("too_large", "response body too large")
        except (httpx.TransportError, ssl.SSLError, OSError) as exc:
            return _failure(exc)

    async def _drive(self, gen: Generator[Step, RawResponse | TransportFailure | None, Result]) -> Result:
        try:
            step = next(gen)
            while True:
                if isinstance(step, Sleep):
                    await self._clock.async_sleep(step.delay)
                    step = gen.send(None)
                else:
                    step = gen.send(await self._send(step))
        except StopIteration as stop:
            result: Result = stop.value
            return result
        finally:
            gen.close()

    async def is_allowed(
        self,
        subject: Subject,
        action: str,
        resource: str,
        attributes: Attributes | None = None,
        *,
        deadline: float | None = None,
        service_name: str | None = None,
    ) -> bool:
        """Returns the server's ``allowed`` value; see the README for errors and fail-closed mode."""
        return self._bool(await self._drive(self._run(subject, action, resource, attributes, deadline, service_name)))

    async def check(
        self,
        subject: Subject,
        action: str,
        resource: str,
        attributes: Attributes | None = None,
        *,
        deadline: float | None = None,
        service_name: str | None = None,
    ) -> Decision:
        """The detailed form. Always raises on failure, in every failure mode."""
        return self._decision(
            await self._drive(self._run(subject, action, resource, attributes, deadline, service_name))
        )

    async def aclose(self) -> None:
        """Releases the connection pool. The client must not be used afterwards."""
        self._closed = True
        await self._http.aclose()

    async def __aenter__(self) -> AsyncClient:
        return self

    async def __aexit__(self, *exc: object) -> None:
        await self.aclose()


def _create_for_testing(
    cls: type[Client] | type[AsyncClient], clock: Clock, rand: Callable[[], float], **kwargs: Any
) -> Any:
    """Internal test seam (design §11): a client with an injected clock and random source."""
    defaults: dict[str, Any] = {
        "retry": RetryPolicy(),
        "failure_mode": "error",
        "circuit_breaker": None,
        "hooks": None,
        "ca_file": None,
        "allow_insecure_http": False,
        "user_agent_suffix": None,
    }
    client = object.__new__(cls)
    client._init({**defaults, **kwargs}, clock, rand)
    return client
