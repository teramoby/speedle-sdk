from __future__ import annotations

import json
import re
from collections.abc import Callable
from pathlib import Path

import pytest

import speedle
from speedle import AsyncClient, Attribute, Client, Principal, Reason

from .conftest import build
from .helpers import TOKEN, ScriptServer

ALLOW = {"status": 200, "body": {"allowed": True, "reason": 0}}


def test_sends_json_context_user_agent_and_no_auth(serve: Callable[..., ScriptServer]) -> None:
    server = serve([ALLOW])
    b = build(server.url, user_agent_suffix="app/1.2")
    with b.client as client:
        assert client.is_allowed(
            "alice", "get", "/books/1", [Attribute.string("team", "eng"), Attribute.numeric("n", [1, 2])]
        )
    req = server.requests[0]
    assert (req.method, req.path) == ("POST", "/authz-check/v1/is-allowed")
    assert req.headers["content-type"] == "application/json"
    assert "authorization" not in req.headers
    version = re.escape(speedle.__version__)
    assert re.fullmatch(
        rf"speedle-sdk-python/{version} \(python/\d+\.\d+\.\d+\S*; [a-z0-9]+\) app/1\.2", req.headers["user-agent"]
    )
    assert list(json.loads(req.body)) == ["subject", "serviceName", "resource", "action", "attributes"]
    assert json.loads(req.body) == {
        "subject": {"principals": [{"type": "user", "name": "alice"}]},
        "serviceName": "books",
        "resource": "/books/1",
        "action": "get",
        "attributes": [
            {"name": "team", "type": "string", "value": "eng"},
            {"name": "n", "type": "numeric", "value": [1, 2]},
        ],
    }


def test_principals_service_override_and_no_empty_attributes(serve: Callable[..., ScriptServer]) -> None:
    server = serve([ALLOW])
    with build(server.url).client as client:
        client.check(
            [Principal("alice", idd="corp"), Principal("eng", type="group")], "get", "/r", {}, service_name="other"
        )
    assert json.loads(server.requests[0].body) == {
        "subject": {"principals": [{"type": "user", "name": "alice", "idd": "corp"}, {"type": "group", "name": "eng"}]},
        "serviceName": "other",
        "resource": "/r",
        "action": "get",
    }


def test_bearer_over_https_and_connection_reuse(serve: Callable[..., ScriptServer]) -> None:
    server = serve([ALLOW, ALLOW], tls=True)
    with build(server.url).client as client:
        assert client.is_allowed("alice", "get", "/r")
        assert client.is_allowed("alice", "get", "/r")
    assert server.requests[0].headers["authorization"] == f"Bearer {TOKEN}"
    assert len(server.requests) == 2


def test_check_returns_decision(serve: Callable[..., ScriptServer]) -> None:
    server = serve(
        [
            {
                "status": 200,
                "headers": {"X-Quota-Daily-Remaining": "42"},
                "body": {"allowed": False, "reason": 4, "errorMessage": "boom", "extra": 1},
            },
            {"status": 200, "body": {"allowed": True, "reason": 9}},
        ]
    )
    with build(server.url).client as client:
        d = client.check("alice", "get", "/r")
        assert (d.allowed, d.reason, d.error_message, d.quota_daily_remaining) == (False, 4, "boom", 42)
        assert d.reason is Reason.ERROR_IN_EVALUATION
        d2 = client.check("alice", "get", "/r")
        assert (d2.reason, d2.error_message, d2.quota_daily_remaining) == (9, None, None)


@pytest.mark.anyio
async def test_async_client(serve: Callable[..., ScriptServer]) -> None:
    server = serve([ALLOW, {"status": 200, "body": {"allowed": False, "reason": 3}}], tls=True)
    async with build(server.url, AsyncClient).client as client:
        assert await client.is_allowed("alice", "get", "/r", {"team": "eng"})
        d = await client.check("alice", "get", "/r")
        assert d.reason is Reason.NO_APPLICABLE_POLICIES
    assert json.loads(server.requests[0].body)["attributes"] == [{"name": "team", "type": "string", "value": "eng"}]


def test_version_matches_pyproject() -> None:
    text = (Path(__file__).parents[1] / "pyproject.toml").read_text()
    match = re.search(r'^version = "([^"]+)"$', text, re.MULTILINE)
    assert match is not None
    assert speedle.__version__ == match.group(1)


def test_public_names() -> None:
    assert not issubclass(speedle.TimeoutError, TimeoutError)
    assert issubclass(speedle.DeadlineExceededError, speedle.TimeoutError)
    assert Client.__name__ == "Client"
