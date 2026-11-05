from __future__ import annotations

import json
import pickle
from collections.abc import Callable
from typing import Any

import pytest

import speedle as S
from speedle import AsyncClient, Auth, CircuitBreaker, Client, Principal, RetryPolicy

from .conftest import build
from .helpers import TOKEN, ScriptServer, certs

BASE: dict[str, Any] = {
    "backend": "speedle-cloud",
    "endpoint": "https://cloud.speedle.io",
    "auth": Auth.bearer(TOKEN),
    "service_name": "books",
}

INVALID: list[tuple[str, dict[str, Any]]] = [
    ("unknown backend", {"backend": "x"}),
    ("empty service name", {"service_name": ""}),
    ("not an Auth", {"auth": "token"}),
    ("cloud without bearer", {"auth": Auth.none()}),
    ("query", {"endpoint": "https://cloud.speedle.io/?a=1"}),
    ("fragment", {"endpoint": "https://cloud.speedle.io/#x"}),
    ("credentials", {"endpoint": "https://u:p@cloud.speedle.io"}),
    ("ftp", {"endpoint": "ftp://cloud.speedle.io"}),
    ("no host", {"endpoint": "https://"}),
    ("http with bearer", {"backend": "speedle-plus", "endpoint": "http://127.0.0.1:6734"}),
    ("http remote", {"backend": "speedle-plus", "auth": Auth.none(), "endpoint": "http://ads.internal:6734"}),
    ("max_attempts 0", {"retry": RetryPolicy(max_attempts=0)}),
    ("float max_attempts", {"retry": RetryPolicy(max_attempts=1.5)}),  # type: ignore[arg-type]
    ("negative delay", {"retry": RetryPolicy(base_delay=-1)}),
    ("zero deadline", {"retry": RetryPolicy(deadline=0)}),
    ("failure mode open", {"failure_mode": "open"}),
    ("breaker threshold 0", {"circuit_breaker": CircuitBreaker(failure_threshold=0)}),
    ("suffix with newline", {"user_agent_suffix": "a\nb"}),
    ("missing CA file", {"ca_file": "/nonexistent/ca.pem"}),
]


@pytest.mark.parametrize(("name", "override"), INVALID, ids=[c[0] for c in INVALID])
@pytest.mark.parametrize("cls", [Client, AsyncClient])
def test_configuration_errors(name: str, override: dict[str, Any], cls: type[Any]) -> None:
    with pytest.raises(S.ConfigurationError):
        cls(**{**BASE, **override})


@pytest.mark.parametrize(
    "make",
    [
        lambda: Auth.bearer(""),
        lambda: Auth.bearer("a b"),
        lambda: Auth.bearer("a\x07"),
        lambda: Auth.mutual_tls("", "k"),
    ],
)
def test_auth_factory_errors(make: Callable[[], Auth]) -> None:
    with pytest.raises(S.ConfigurationError):
        make()


def test_valid_endpoints() -> None:
    for override in (
        {"endpoint": "https://gw.example.com/speedle/"},
        {"backend": "speedle-plus", "auth": Auth.none(), "endpoint": "http://localhost:6734"},
        {"backend": "speedle-plus", "auth": Auth.none(), "endpoint": "http://[::1]:6734"},
        {
            "backend": "speedle-plus",
            "auth": Auth.none(),
            "endpoint": "http://ads.internal",
            "allow_insecure_http": True,
        },
    ):
        Client(**{**BASE, **override}).close()


def test_path_prefix_is_kept(serve: Callable[..., ScriptServer]) -> None:
    server = serve([{"status": 200, "body": {"allowed": True, "reason": 0}}])
    with build(server.url + "/prefix/").client as client:
        client.is_allowed("alice", "get", "/r")
    assert server.requests[0].path == "/prefix/authz-check/v1/is-allowed"


def test_token_is_redacted() -> None:
    auth = Auth.bearer(TOKEN)
    client = Client(**{**BASE, "auth": auth})
    for text in (str(auth), repr(auth), repr(client)):
        assert TOKEN not in text
    with pytest.raises(TypeError):
        pickle.dumps(auth)
    with pytest.raises(TypeError):
        json.dumps(auth)
    with pytest.raises(AttributeError):
        auth.kind = "none"  # type: ignore[misc]
    client.close()


def test_call_time_validation_sends_nothing(serve: Callable[..., ScriptServer]) -> None:
    server = serve([])
    with build(server.url).client as client:
        for args, kwargs in (
            (("", "get", "/r"), {}),
            (([], "get", "/r"), {}),
            (([Principal("")], "get", "/r"), {}),
            ((["alice"], "get", "/r"), {}),
            (("alice", 1, "/r"), {}),
            (("alice", "get", "/r"), {"service_name": ""}),
        ):
            with pytest.raises(S.InvalidRequestError):
                client.is_allowed(*args, **kwargs)
    assert server.requests == []


def test_closed_client_refuses_calls() -> None:
    client = Client(**{**BASE, "backend": "speedle-plus", "auth": Auth.none(), "endpoint": "http://127.0.0.1:1"})
    client.close()
    with pytest.raises(S.SpeedleError, match="closed"):
        client.is_allowed("alice", "get", "/r")


def test_mutual_tls(serve: Callable[..., ScriptServer]) -> None:
    server = serve([{"status": 200, "body": {"allowed": True, "reason": 0}}], tls=True, require_client_cert=True)
    c = certs()
    auth = Auth.mutual_tls(str(c.client_cert), str(c.client_key))
    with build(server.url, backend="speedle-plus", auth=auth).client as client:
        assert client.is_allowed("alice", "get", "/r") is True
    assert "authorization" not in server.requests[0].headers


@pytest.mark.parametrize("ca", [True, False])
def test_tls_failures_are_not_retried(serve: Callable[..., ScriptServer], ca: bool) -> None:
    # With the CA the server rejects the missing client certificate; without it the client rejects the server.
    server = serve([], tls=True, require_client_cert=ca)
    b = build(server.url, backend="speedle-plus", auth=Auth.none(), ca_file=str(certs().ca) if ca else None)
    with b.client as client, pytest.raises(S.NetworkError):
        client.is_allowed("alice", "get", "/r")
    assert [e["type"] for e in b.events] == ["attempt", "giveUp"]
    assert b.events[-1]["reason"] == "non_retryable"
