"""Interim in-process conformance mock.

It replays a vector's exchanges over HTTPS with a throwaway CA and checks
every request, following conformance/README.md. It is replaced by the shared
Go mock in conformance/mock once that is on main.
"""

from __future__ import annotations

import json
import re
from typing import Any

from ..helpers import Recorded, ScriptServer, certs


def json_equal(a: Any, b: Any) -> bool:
    """JSON equality that keeps booleans and numbers apart (Python has True == 1)."""
    if isinstance(a, bool) or isinstance(b, bool):
        return isinstance(a, bool) and isinstance(b, bool) and a == b
    if isinstance(a, (int, float)) and isinstance(b, (int, float)):
        return a == b
    if isinstance(a, dict) and isinstance(b, dict):
        return a.keys() == b.keys() and all(json_equal(a[k], b[k]) for k in a)
    if isinstance(a, list) and isinstance(b, list):
        return len(a) == len(b) and all(json_equal(x, y) for x, y in zip(a, b, strict=True))
    return type(a) is type(b) and a == b


def _sort_at(doc: Any, pointer: str) -> None:
    target = doc
    for raw in pointer.split("/")[1:]:
        key = raw.replace("~1", "/").replace("~0", "~")
        target = target[int(key)] if isinstance(target, list) else target.get(key) if isinstance(target, dict) else None
    if isinstance(target, list):
        target.sort(key=lambda v: json.dumps(v, sort_keys=True))


class InProcessMock:
    def __init__(self) -> None:
        self.exchanges: list[dict[str, Any]] = []
        self.problems: list[str] = []
        self.server = ScriptServer(self._reply, tls=True).start()

    @property
    def endpoint(self) -> str:
        return self.server.url

    @property
    def ca_file(self) -> str:
        return str(certs().ca)

    def load(self, vector: dict[str, Any]) -> None:
        self.server.release()
        self.server = self._restart()
        self.exchanges = vector["exchanges"]
        self.problems = []

    def _restart(self) -> ScriptServer:
        self.server.stop()
        return ScriptServer(self._reply, tls=True).start()

    def _reply(self, rec: Recorded, index: int) -> dict[str, Any]:
        if index >= len(self.exchanges):
            self.problems.append(f"unexpected request #{index + 1}")
            return {"status": 418, "body": {"error": "conformance mock: unexpected request"}}
        want = self.exchanges[index]["request"]
        tag = f"request #{index + 1}"
        if rec.method != want["method"]:
            self.problems.append(f"{tag}: method {rec.method}")
        if rec.path != want["path"]:
            self.problems.append(f"{tag}: path {rec.path}")
        for name, matcher in (want.get("headers") or {}).items():
            value = rec.headers.get(name.lower())
            if isinstance(matcher, str):
                ok = value == matcher
            elif matcher.get("absent"):
                ok = value is None
            else:
                ok = value is not None and re.fullmatch(matcher["pattern"], value) is not None
            if not ok:
                shown = "<redacted>" if name.lower() == "authorization" else value
                self.problems.append(f"{tag}: header {name}: {shown!r}")
        try:
            body = json.loads(rec.body)
        except ValueError:
            body = object()
        expected = json.loads(json.dumps(want["body"]))
        for pointer in want.get("unorderedArrays") or []:
            _sort_at(expected, pointer)
            _sort_at(body, pointer)
        if not json_equal(expected, body):
            self.problems.append(f"{tag}: body {rec.body!r}")
        response: dict[str, Any] = self.exchanges[index]["response"]
        return response

    def verify(self) -> list[str]:
        out = list(self.problems)
        received = len(self.server.requests)
        if received < len(self.exchanges):
            out.append(f"expected {len(self.exchanges)} requests, received {received}")
        return out

    def close(self) -> None:
        self.server.stop()
