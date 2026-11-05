from __future__ import annotations

from collections.abc import Callable, Iterator
from typing import Any

import pytest

from speedle import Auth, Client, Hooks
from speedle._client import AsyncClient, _create_for_testing

from .helpers import TOKEN, Reply, ScriptServer, VirtualClock, certs


@pytest.fixture
def anyio_backend() -> str:
    return "asyncio"


@pytest.fixture
def serve() -> Iterator[Callable[..., ScriptServer]]:
    servers: list[ScriptServer] = []

    def start(replies: list[Reply] | Callable[[Any, int], Reply], **kw: Any) -> ScriptServer:
        s = ScriptServer(replies, **kw).start()
        servers.append(s)
        return s

    yield start
    for s in servers:
        s.stop()


class Built:
    def __init__(self, client: Any, clock: VirtualClock, events: list[dict[str, Any]]) -> None:
        self.client = client
        self.clock = clock
        self.events = events


def build(url: str, cls: type[Client] | type[AsyncClient] = Client, jitter: float = 1.0, **overrides: Any) -> Built:
    """A client with the virtual clock, a fixed jitter fraction and recording hooks."""
    clock = VirtualClock()
    events: list[dict[str, Any]] = []
    https = url.startswith("https:")
    cloud = overrides.get("backend", "speedle-cloud" if https else "speedle-plus") == "speedle-cloud"
    hooks = Hooks(
        on_attempt=lambda e: events.append({"type": "attempt", "attempt": e.attempt, "status": e.status, "errorType": e.error_type}),
        on_retry=lambda e: events.append({"type": "retry", "attempt": e.attempt, "delay": e.delay, "errorType": e.error_type}),
        on_give_up=lambda e: events.append({"type": "giveUp", "attempts": e.attempts, "reason": e.reason, "errorType": type(e.error).__name__}),
        on_breaker_state_change=lambda e: events.append({"type": "breaker", "from": e.from_state, "to": e.to_state}),
    )  # fmt: skip
    kwargs: dict[str, Any] = {
        "backend": "speedle-cloud" if cloud else "speedle-plus",
        "endpoint": url,
        "auth": Auth.bearer(TOKEN) if cloud else Auth.none(),
        "service_name": "books",
        "ca_file": str(certs().ca) if https else None,
        "hooks": hooks,
        **overrides,
    }
    return Built(_create_for_testing(cls, clock, lambda: jitter, **kwargs), clock, events)
