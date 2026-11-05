"""Adapter for the shared conformance mock in conformance/mock (a Go program).

See conformance/README.md, "Mock server".
"""

from __future__ import annotations

import json
import os
import subprocess
import tempfile
import urllib.error
import urllib.request
from pathlib import Path
from typing import IO, Any, cast

MOCK_DIR = Path(__file__).resolve().parents[3] / "conformance" / "mock"


class GoMock:
    """Builds the mock (or uses SPEEDLE_MOCK_BIN), starts it and talks to its control API."""

    def __init__(self) -> None:
        binary = os.environ.get("SPEEDLE_MOCK_BIN")
        if not binary:
            binary = str(Path(tempfile.mkdtemp(prefix="speedle-mock-")) / "speedle-mock")
            subprocess.run(["go", "build", "-o", binary, "."], cwd=MOCK_DIR, check=True)
        self._proc = subprocess.Popen(
            [binary, "-exit-on-stdin-close"], stdin=subprocess.PIPE, stdout=subprocess.PIPE, text=True
        )
        info = json.loads(cast(IO[str], self._proc.stdout).readline())
        self.endpoint: str = info["endpoint"]
        self.ca_file: str = info["caFile"]
        self._control: str = info["control"]

    def _request(self, method: str, path: str, body: bytes | None = None) -> tuple[int, bytes]:
        req = urllib.request.Request(self._control + path, data=body, method=method)  # noqa: S310 - loopback control URL
        try:
            with urllib.request.urlopen(req, timeout=10) as res:  # noqa: S310
                return int(res.status), res.read()
        except urllib.error.HTTPError as err:
            return err.code, err.read()

    def load(self, vector: dict[str, Any]) -> None:
        status, body = self._request("POST", "/load", json.dumps(vector).encode())
        if status != 204:
            raise RuntimeError(f"mock /load: {status} {body!r}")

    def verify(self) -> list[str]:
        _, body = self._request("GET", "/verify")
        result = json.loads(body)
        return [] if result.get("ok") else list(result.get("errors") or ["verify failed"])

    def close(self) -> None:
        cast(IO[str], self._proc.stdin).close()
        self._proc.wait(timeout=10)
