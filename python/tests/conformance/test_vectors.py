"""Conformance runner: runs every vector in conformance/vectors (design §11), sync and async."""

from __future__ import annotations

import asyncio
import datetime as dt
import json
import math
from collections.abc import Iterator
from pathlib import Path
from typing import Any

import pytest

from speedle import (
    AsyncClient,
    Attribute,
    Auth,
    CircuitBreaker,
    Client,
    Hooks,
    Principal,
    RetryPolicy,
    SpeedleError,
)
from speedle._client import _create_for_testing

from ..helpers import VirtualClock
from .mock import GoMock

VECTORS = Path(__file__).resolve().parents[3] / "conformance" / "vectors"
FILES = sorted(VECTORS.glob("*.json"))
assert FILES, f"no conformance vectors in {VECTORS}"


@pytest.fixture(scope="module")
def mock() -> Iterator[GoMock]:
    m = GoMock()
    yield m
    m.close()


def _time(value: str) -> dt.datetime:
    return dt.datetime.fromisoformat(value.replace("Z", "+00:00"))


def _attribute(a: dict[str, Any]) -> Attribute:
    t, name, value = a["type"], a["name"], a["value"]
    if t == "string":
        return Attribute.string(name, value)
    if t == "numeric":
        return Attribute.numeric(name, value)
    if t == "bool":
        return Attribute.boolean(name, value)
    if t == "datetime":
        return Attribute.datetime(name, [_time(v) for v in value] if isinstance(value, list) else _time(value))
    raise AssertionError(f"unknown attribute type {t}")


def _subject(subject: Any) -> Any:
    if isinstance(subject, str):
        return subject
    return [Principal(p["name"], p.get("type", "user"), p.get("idd")) for p in subject]


def _match(where: str, expected: dict[str, Any], actual: dict[str, Any]) -> None:
    """A present field must match; null means the SDK reports no value."""
    for key, want in expected.items():
        got = actual.get(key)
        if isinstance(want, (int, float)) and not isinstance(want, bool) and isinstance(got, (int, float)):
            assert math.isclose(got, want, abs_tol=1e-6), f"{where}.{key}: {got!r} != {want!r}"
        else:
            assert got == want and type(got) is type(want), f"{where}.{key}: {got!r} != {want!r}"


def _ms(seconds: list[float]) -> list[float]:
    return [s * 1000 for s in seconds]


async def _invoke(client: Any, call: dict[str, Any]) -> Any:
    attributes: Any = call.get("inferredAttributes")
    if attributes is None and "attributes" in call:
        attributes = [_attribute(a) for a in call["attributes"]]
    method = client.check if call["method"] == "check" else client.is_allowed
    result = method(
        _subject(call["subject"]), call["action"], call["resource"], attributes, service_name=call.get("serviceName")
    )
    return await result if asyncio.iscoroutine(result) else result


async def _run_vector(vector: dict[str, Any], mock: GoMock, use_async: bool) -> None:
    mock.load(vector)
    clock = VirtualClock()
    events: list[dict[str, Any]] = []
    cfg = vector["config"]
    retry = cfg.get("retry", {})
    policy = RetryPolicy(
        max_attempts=retry.get("maxAttempts", 3),
        base_delay=retry.get("baseDelayMs", 100) / 1000,
        max_delay=retry.get("maxDelayMs", 2000) / 1000,
        attempt_timeout=retry.get("attemptTimeoutMs", 2000) / 1000,
        deadline=retry.get("deadlineMs", 5000) / 1000,
    )
    cb = cfg.get("circuitBreaker")
    hooks = Hooks(
        on_attempt=lambda e: events.append({"type": "attempt", "attempt": e.attempt, "status": e.status, "errorType": e.error_type}),
        on_retry=lambda e: events.append({"type": "retry", "attempt": e.attempt, "delayMs": e.delay * 1000, "errorType": e.error_type}),
        on_give_up=lambda e: events.append({"type": "giveUp", "attempts": e.attempts, "reason": e.reason, "errorType": type(e.error).__name__}),
        on_breaker_state_change=lambda e: events.append({"type": "breaker", "from": e.from_state, "to": e.to_state}),
    )  # fmt: skip
    auth = Auth.bearer(cfg["auth"]["token"]) if cfg["auth"]["type"] == "bearer" else Auth.none()
    client: Any = _create_for_testing(
        AsyncClient if use_async else Client,
        clock,
        lambda: vector.get("jitterFraction", 1),
        backend=vector["backend"],
        endpoint=mock.endpoint,
        ca_file=mock.ca_file,
        auth=auth,
        service_name=cfg["serviceName"],
        retry=policy,
        failure_mode=cfg.get("failureMode", "error"),
        circuit_breaker=None
        if cb is None
        else CircuitBreaker(cb.get("failureThreshold", 5), cb.get("coolDownMs", 30000) / 1000),
        hooks=hooks,
    )
    try:
        steps = vector.get("steps") or [{"call": vector["call"], "expect": vector["expect"]}]
        for i, step in enumerate(steps):
            where = f"steps[{i}]" if "steps" in vector else "call"
            clock.advance(step.get("advanceMs", 0) / 1000)
            clock.sleeps.clear()
            expect = step["expect"]
            try:
                value, error = await _invoke(client, step["call"]), None
            except SpeedleError as exc:
                value, error = None, exc
            if "error" in expect:
                want = dict(expect["error"])
                assert error is not None, f"{where}: expected {want['type']}, got {value!r}"
                assert type(error).__name__ == want.pop("type"), f"{where}: {error!r}"
                actual = {
                    "status": error.status,
                    "code": error.code,
                    "quotaDailyRemaining": error.quota_daily_remaining,
                    "retryAfterSeconds": getattr(error, "retry_after", None),
                }
                _match(f"{where}.error", want, actual)
            else:
                assert error is None, f"{where}: unexpected {error!r}"
                if "result" in expect:
                    assert value is expect["result"], f"{where}: result {value!r}"
                if "decision" in expect:
                    d = {
                        "allowed": value.allowed,
                        "reason": int(value.reason),
                        "errorMessage": value.error_message,
                        "quotaDailyRemaining": value.quota_daily_remaining,
                    }
                    _match(f"{where}.decision", expect["decision"], d)
            if "delaysMs" in expect:
                got = _ms(clock.sleeps)
                assert len(got) == len(expect["delaysMs"]), f"{where}: delaysMs {got}"
                for g, w in zip(got, expect["delaysMs"], strict=True):
                    assert math.isclose(g, w, abs_tol=1e-6), f"{where}: delaysMs {got}"
        assert mock.verify() == [], "mock exchanges"
        if "events" in vector:
            assert len(events) == len(vector["events"]), f"events: {json.dumps(events)}"
            for i, want in enumerate(vector["events"]):
                _match(f"events[{i}]", want, events[i])
    finally:
        if use_async:
            await client.aclose()
        else:
            client.close()


@pytest.mark.parametrize("use_async", [False, True], ids=["sync", "async"])
@pytest.mark.parametrize("path", FILES, ids=[f.stem for f in FILES])
def test_vector(path: Path, use_async: bool, mock: GoMock) -> None:
    vector = json.loads(path.read_text())
    assert vector["id"] == path.stem
    asyncio.run(_run_vector(vector, mock, use_async))
