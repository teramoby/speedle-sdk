from __future__ import annotations

import asyncio
import threading
import time
from collections.abc import Callable
from typing import Any

import pytest

import speedle as S
from speedle import AsyncClient, Hooks, RetryPolicy

from .conftest import build
from .helpers import ScriptServer

ALLOW = {"status": 200, "body": {"allowed": True, "reason": 0}}
R503 = {"status": 503, "body": {"error": "snapshot_unavailable"}}


def test_502_503_504_are_retried_with_full_jitter(serve: Callable[..., ScriptServer]) -> None:
    server = serve([R503, {"status": 502}, {"status": 504}, ALLOW])
    b = build(server.url, jitter=0.5, retry=RetryPolicy(max_attempts=4))
    with b.client as client:
        assert client.is_allowed("alice", "get", "/r")
    assert b.clock.sleeps == pytest.approx([0.05, 0.1, 0.2])
    retries = [(e["attempt"], e["errorType"]) for e in b.events if e["type"] == "retry"]
    assert retries == [(2, "ServiceUnavailableError"), (3, "ServerError"), (4, "ServerError")]


def test_backoff_cap_and_attempts_exhausted(serve: Callable[..., ScriptServer]) -> None:
    server = serve([R503, R503, R503])
    b = build(server.url, retry=RetryPolicy(base_delay=0.3, max_delay=0.4))
    with b.client as client, pytest.raises(S.ServiceUnavailableError):
        client.check("alice", "get", "/r")
    assert b.clock.sleeps == pytest.approx([0.3, 0.4])
    assert b.events[-1] == {
        "type": "giveUp",
        "attempts": 3,
        "reason": "attempts_exhausted",
        "errorType": "ServiceUnavailableError",
    }


def test_500_is_not_retried(serve: Callable[..., ScriptServer]) -> None:
    server = serve([{"status": 500}, ALLOW])
    with build(server.url).client as client, pytest.raises(S.ServerError):
        client.is_allowed("alice", "get", "/r")
    assert len(server.requests) == 1


def test_retry_after_raises_the_delay(serve: Callable[..., ScriptServer]) -> None:
    server = serve([{"status": 503, "headers": {"Retry-After": "2"}}, ALLOW])
    b = build(server.url)
    with b.client as client:
        assert client.is_allowed("alice", "get", "/r")
    assert b.clock.sleeps == [2.0]


def test_burst_429_beyond_the_deadline_is_raised(serve: Callable[..., ScriptServer]) -> None:
    server = serve([{"status": 429, "headers": {"Retry-After": "6"}, "body": {"error": "x"}}])
    b = build(server.url)
    with b.client as client, pytest.raises(S.RateLimitedError) as info:
        client.is_allowed("alice", "get", "/r")
    assert info.value.retry_after == 6.0
    assert b.events[-1]["reason"] == "retry_after_exceeds_deadline"


@pytest.mark.parametrize("use_async", [False, True])
def test_reset_is_retried_and_timeouts_raise_timeout_error(serve: Callable[..., ScriptServer], use_async: bool) -> None:
    server = serve([{"fault": "connection_reset"}, {"fault": "no_response"}, {"fault": "no_response"}])
    retry = RetryPolicy(attempt_timeout=0.1)
    if use_async:
        b = build(server.url, AsyncClient, retry=retry)

        async def go() -> None:
            async with b.client as client:
                await client.is_allowed("alice", "get", "/r")

        with pytest.raises(S.TimeoutError) as info:
            asyncio.run(go())
    else:
        b = build(server.url, retry=retry)
        with b.client as client, pytest.raises(S.TimeoutError) as info:
            client.is_allowed("alice", "get", "/r")
    assert not isinstance(info.value, S.DeadlineExceededError)
    assert [e["errorType"] for e in b.events if e["type"] == "attempt"] == [
        "NetworkError",
        "TimeoutError",
        "TimeoutError",
    ]
    assert b.events[-1]["reason"] == "attempts_exhausted"
    server.release()


def test_deadline_cuts_an_attempt(serve: Callable[..., ScriptServer]) -> None:
    server = serve([{"fault": "no_response"}])
    b = build(server.url, retry=RetryPolicy(deadline=0.1))
    started = time.monotonic()
    with b.client as client, pytest.raises(S.DeadlineExceededError) as info:
        client.is_allowed("alice", "get", "/r")
    assert info.value.status is None
    assert time.monotonic() - started < 1.5
    assert b.events[-1]["reason"] == "deadline"
    server.release()


def test_per_call_deadline_overrides(serve: Callable[..., ScriptServer]) -> None:
    server = serve([{"fault": "no_response"}])
    b = build(server.url)
    with b.client as client:
        with pytest.raises(S.DeadlineExceededError):
            client.is_allowed("alice", "get", "/r", deadline=0.1)
        with pytest.raises(S.InvalidRequestError):
            client.is_allowed("alice", "get", "/r", deadline=0)
    server.release()


@pytest.mark.anyio
async def test_cancellation_during_delay_propagates_without_give_up(serve: Callable[..., ScriptServer]) -> None:
    server = serve([R503, ALLOW])
    started = threading.Event()

    def give_up(_: Any) -> None:
        raise AssertionError("no give-up on cancellation")

    b = build(
        server.url,
        AsyncClient,
        failure_mode="closed",
        hooks=Hooks(on_retry=lambda e: started.set(), on_give_up=give_up),
    )
    real_sleep = asyncio.sleep

    async def slow_sleep(seconds: float) -> None:
        await real_sleep(10)

    b.client._clock.async_sleep = slow_sleep
    async with b.client as client:
        task = asyncio.create_task(client.is_allowed("alice", "get", "/r"))
        while not started.is_set():
            await real_sleep(0.01)
        task.cancel()
        with pytest.raises(asyncio.CancelledError):
            await task
    assert len(server.requests) == 1


@pytest.mark.anyio
async def test_asyncio_timeout_during_attempt(serve: Callable[..., ScriptServer]) -> None:
    server = serve([{"fault": "no_response"}])
    async with build(server.url, AsyncClient, failure_mode="closed").client as client:
        with pytest.raises(asyncio.TimeoutError):
            await asyncio.wait_for(client.is_allowed("alice", "get", "/r"), 0.1)
    server.release()


def test_fail_closed(serve: Callable[..., ScriptServer]) -> None:
    server = serve([R503, R503, {"status": 401}])
    b = build(server.url, failure_mode="closed", retry=RetryPolicy(max_attempts=2))
    with b.client as client:
        assert client.is_allowed("alice", "get", "/r") is False
        assert client.is_allowed("alice", "get", "/r", {"a": None}) is False
        with pytest.raises(S.UnauthorizedError):
            client.check("alice", "get", "/r")
    assert [(e["reason"], e["errorType"]) for e in b.events if e["type"] == "giveUp"] == [
        ("attempts_exhausted", "ServiceUnavailableError"),
        ("non_retryable", "InvalidRequestError"),
        ("non_retryable", "UnauthorizedError"),
    ]


def test_failing_hooks_never_change_the_result(serve: Callable[..., ScriptServer]) -> None:
    server = serve([R503, ALLOW])

    def boom(_: Any) -> None:
        raise RuntimeError("hook")

    async def coroutine_hook(_: Any) -> None:
        raise RuntimeError("never awaited")

    hooks = Hooks(on_attempt=boom, on_retry=coroutine_hook, on_give_up=boom)  # type: ignore[arg-type]
    with build(server.url, hooks=hooks).client as client:
        assert client.is_allowed("alice", "get", "/r") is True
