from __future__ import annotations

from collections.abc import Callable
from typing import Any

import pytest

import speedle as S

from .conftest import build
from .helpers import TOKEN, ScriptServer

CASES: list[tuple[str, dict[str, Any], str, type[S.SpeedleError], dict[str, Any]]] = [
    ("cloud 400", {"status": 400, "body": {"error": "invalid_request"}}, "speedle-cloud", S.InvalidRequestError, {"status": 400, "code": "invalid_request"}),
    ("cloud 400 free text", {"status": 400, "body": {"error": "bad attr"}}, "speedle-cloud", S.InvalidRequestError, {"code": None, "message": "HTTP 400: bad attr"}),
    ("plus 400", {"status": 400, "body": {"error": "invalid_request"}}, "speedle-plus", S.InvalidRequestError, {"code": None}),
    ("401", {"status": 401, "body": {"error": "unauthorized"}}, "speedle-cloud", S.UnauthorizedError, {"code": "unauthorized"}),
    ("403", {"status": 403, "headers": {"X-Quota-Daily-Remaining": "7"}, "body": {"error": "service_mismatch"}}, "speedle-cloud", S.ForbiddenError, {"quota_daily_remaining": 7}),
    ("404", {"status": 404, "rawBody": "not found"}, "speedle-plus", S.HttpError, {"status": 404}),
    ("413", {"status": 413, "body": {"error": "request_too_large"}}, "speedle-cloud", S.RequestTooLargeError, {}),
    ("415", {"status": 415}, "speedle-plus", S.HttpError, {}),
    ("302", {"status": 302, "headers": {"Location": "https://example.com/"}}, "speedle-plus", S.HttpError, {"status": 302}),
    ("cloud 429 daily", {"status": 429, "headers": {"Retry-After": "3600"}, "body": {"error": "daily_quota_exceeded"}}, "speedle-cloud", S.QuotaExceededError, {"retry_after": 3600.0}),
    ("cloud 429 unknown", {"status": 429, "headers": {"Retry-After": "5"}, "body": {"error": "other"}}, "speedle-cloud", S.TooManyRequestsError, {"code": None}),
    ("cloud 429 burst no Retry-After", {"status": 429, "body": {"error": "rate_limited"}}, "speedle-cloud", S.RateLimitedError, {"retry_after": None}),
    ("plus 429 bad Retry-After", {"status": 429, "headers": {"Retry-After": "0"}, "body": {"error": "slow"}}, "speedle-plus", S.RateLimitedError, {"retry_after": None, "code": None}),
    ("500", {"status": 500, "body": {"error": "internal_error"}}, "speedle-cloud", S.ServerError, {"code": "internal_error"}),
    ("501", {"status": 501}, "speedle-plus", S.ServerError, {}),
    ("200 non-JSON", {"status": 200, "rawBody": "<html>"}, "speedle-plus", S.UnexpectedResponseError, {"status": 200}),
    ("200 allowed string", {"status": 200, "body": {"allowed": "true", "reason": 0}}, "speedle-plus", S.UnexpectedResponseError, {}),
    ("200 no reason", {"status": 200, "body": {"allowed": True}}, "speedle-plus", S.UnexpectedResponseError, {}),
]  # fmt: skip


@pytest.mark.parametrize(("name", "reply", "backend", "error_type", "fields"), CASES, ids=[c[0] for c in CASES])
def test_status_mapping(
    serve: Callable[..., ScriptServer],
    name: str,
    reply: dict[str, Any],
    backend: str,
    error_type: type[S.SpeedleError],
    fields: dict[str, Any],
) -> None:
    server = serve([reply], tls=True)
    b = build(server.url, backend=backend)
    with b.client as client, pytest.raises(S.SpeedleError) as info:
        client.is_allowed("alice", "get", "/r")
    err = info.value
    assert type(err) is error_type
    for key, value in fields.items():
        assert getattr(err, key) == value, key
    assert len(server.requests) == 1
    assert b.clock.sleeps == []
    assert b.events[-1]["reason"] == "non_retryable"
    assert TOKEN not in str(err) and TOKEN not in repr(err)


def test_hierarchy() -> None:
    assert issubclass(S.DeadlineExceededError, S.TimeoutError)
    assert issubclass(S.TimeoutError, S.NetworkError)
    assert issubclass(S.RateLimitedError, S.TooManyRequestsError)
    assert issubclass(S.QuotaExceededError, S.TooManyRequestsError)
    assert issubclass(S.CircuitOpenError, S.SpeedleError)
