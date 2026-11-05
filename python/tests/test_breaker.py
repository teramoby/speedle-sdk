from __future__ import annotations

import asyncio
from collections.abc import Callable
from typing import Any

import pytest

import speedle as S
from speedle import AsyncClient, CircuitBreaker, RetryPolicy

from .conftest import build
from .helpers import Recorded, ScriptServer

ALLOW = {"status": 200, "body": {"allowed": True, "reason": 0}}
ONE = RetryPolicy(max_attempts=1)


def test_4xx_resets_and_validation_does_not_count(serve: Callable[..., ScriptServer]) -> None:
    server = serve([{"status": 500}, {"status": 403}, {"status": 500}, {"status": 500}])
    b = build(server.url, retry=ONE, circuit_breaker=CircuitBreaker(failure_threshold=2))
    with b.client as client:
        attributes: Any
        for attributes in (None, None, {"bad": []}, None):
            with pytest.raises(S.SpeedleError):
                client.is_allowed("alice", "get", "/r", attributes)
        assert not [e for e in b.events if e["type"] == "breaker"]
        with pytest.raises(S.ServerError):
            client.is_allowed("alice", "get", "/r")
        assert b.events[-1] == {"type": "breaker", "from": "closed", "to": "open"}
        with pytest.raises(S.CircuitOpenError):
            client.is_allowed("alice", "get", "/r")
    assert len(server.requests) == 4


@pytest.mark.anyio
async def test_half_open_admits_one_trial(serve: Callable[..., ScriptServer]) -> None:
    gate = asyncio.Event()
    loop = asyncio.get_running_loop()

    def reply(_: Recorded, index: int) -> dict[str, Any]:
        if index == 1:
            asyncio.run_coroutine_threadsafe(_wait(gate), loop).result(5)
        return {"status": 502} if index == 0 else {"status": 503}

    server = serve(reply)
    b = build(server.url, AsyncClient, retry=ONE, circuit_breaker=CircuitBreaker(failure_threshold=1, cool_down=1.0))
    async with b.client as client:
        with pytest.raises(S.ServerError):
            await client.is_allowed("alice", "get", "/r")
        b.clock.advance(0.999)
        with pytest.raises(S.CircuitOpenError):
            await client.is_allowed("alice", "get", "/r")
        b.clock.advance(0.001)
        trial = asyncio.create_task(client.is_allowed("alice", "get", "/r"))
        await asyncio.sleep(0)
        with pytest.raises(S.CircuitOpenError):
            await client.is_allowed("alice", "get", "/r")
        gate.set()
        with pytest.raises(S.ServiceUnavailableError):
            await trial
    assert [(e["from"], e["to"]) for e in b.events if e["type"] == "breaker"] == [
        ("closed", "open"),
        ("open", "half_open"),
        ("half_open", "open"),
    ]
    assert len(server.requests) == 2


async def _wait(event: asyncio.Event) -> None:
    await event.wait()


@pytest.mark.anyio
async def test_cancelled_trial_frees_the_slot(serve: Callable[..., ScriptServer]) -> None:
    server = serve([{"status": 503}, {"fault": "no_response"}, ALLOW])
    b = build(server.url, AsyncClient, retry=ONE, circuit_breaker=CircuitBreaker(failure_threshold=1, cool_down=0.01))
    async with b.client as client:
        with pytest.raises(S.ServiceUnavailableError):
            await client.is_allowed("alice", "get", "/r")
        b.clock.advance(0.01)
        with pytest.raises(asyncio.TimeoutError):
            await asyncio.wait_for(client.is_allowed("alice", "get", "/r"), 0.2)
        assert await client.is_allowed("alice", "get", "/r") is True
    server.release()
