"""Test helpers: virtual clock, throwaway certificates and a scripted HTTP(S) server."""

from __future__ import annotations

import functools
import json
import socket
import ssl
import struct
import subprocess
import tempfile
import threading
import time
from collections.abc import Callable
from dataclasses import dataclass, field
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Any

TOKEN = "spdl_test_token_value"


class VirtualClock:
    """Design §11: sleep returns at once and advances an offset."""

    def __init__(self) -> None:
        self.offset = 0.0
        self.sleeps: list[float] = []

    def now(self) -> float:
        return time.monotonic() + self.offset

    def sleep(self, seconds: float) -> None:
        self.sleeps.append(seconds)
        self.offset += seconds

    async def async_sleep(self, seconds: float) -> None:
        self.sleep(seconds)

    def advance(self, seconds: float) -> None:
        self.offset += seconds


@dataclass(frozen=True)
class Certs:
    dir: Path
    ca: Path
    server_cert: Path
    server_key: Path
    client_cert: Path
    client_key: Path


@functools.cache
def certs() -> Certs:
    """A throwaway CA, a server certificate for 127.0.0.1 and a client certificate."""
    d = Path(tempfile.mkdtemp(prefix="speedle-python-certs-"))

    def run(*args: str) -> None:
        subprocess.run(["openssl", *args], cwd=d, check=True, capture_output=True)

    run(
        "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-keyout", "ca.key", "-out", "ca.crt", "-days", "2",
        "-subj", "/CN=speedle-sdk test CA", "-addext", "basicConstraints=critical,CA:TRUE",
        "-addext", "keyUsage=critical,keyCertSign,cRLSign",
    )  # fmt: skip
    for name, ext in (
        ("server", "subjectAltName=IP:127.0.0.1,DNS:localhost\nextendedKeyUsage=serverAuth\n"),
        ("client", "extendedKeyUsage=clientAuth\n"),
    ):
        run(
            "req",
            "-newkey",
            "rsa:2048",
            "-nodes",
            "-keyout",
            f"{name}.key",
            "-out",
            f"{name}.csr",
            "-subj",
            f"/CN={name}",
        )
        (d / f"{name}.ext").write_text(ext)
        run(
            "x509", "-req", "-in", f"{name}.csr", "-CA", "ca.crt", "-CAkey", "ca.key", "-CAcreateserial",
            "-out", f"{name}.crt", "-days", "2", "-extfile", f"{name}.ext",
        )  # fmt: skip
    return Certs(d, d / "ca.crt", d / "server.crt", d / "server.key", d / "client.crt", d / "client.key")


@dataclass
class Recorded:
    method: str
    path: str
    headers: dict[str, str]
    body: bytes


Reply = dict[str, Any]  # {"status", "headers", "body" | "rawBody"} or {"fault": ...}


def reset(conn: socket.socket) -> None:
    """Closes a TCP connection with a reset (SO_LINGER 0)."""
    conn.setsockopt(socket.SOL_SOCKET, socket.SO_LINGER, struct.pack("ii", 1, 0))
    conn.close()


@dataclass
class ScriptServer:
    replies: list[Reply] | Callable[[Recorded, int], Reply]
    tls: bool = False
    require_client_cert: bool = False
    requests: list[Recorded] = field(default_factory=list)
    problems: list[str] = field(default_factory=list)

    def start(self) -> ScriptServer:
        owner = self
        self._release = threading.Event()

        class Handler(BaseHTTPRequestHandler):
            protocol_version = "HTTP/1.1"

            def log_message(self, format: str, *args: object) -> None:  # noqa: A002
                pass

            def do_POST(self) -> None:
                self._handle()

            def do_GET(self) -> None:
                self._handle()

            def _handle(self) -> None:
                length = int(self.headers.get("Content-Length") or 0)
                rec = Recorded(
                    self.command, self.path, {k.lower(): v for k, v in self.headers.items()}, self.rfile.read(length)
                )
                index = len(owner.requests)
                owner.requests.append(rec)
                if callable(owner.replies):
                    reply: Reply | None = owner.replies(rec, index)
                else:
                    reply = owner.replies[index] if index < len(owner.replies) else None
                if reply is None:
                    owner.problems.append(f"unexpected request #{index + 1}")
                    self._write(599, {}, b"")
                    return
                fault = reply.get("fault")
                if fault == "connection_reset":
                    self.wfile.flush()
                    reset(self.connection)
                    self.close_connection = True
                    return
                if fault == "no_response":
                    owner._release.wait(30)
                    self.close_connection = True
                    return
                headers: dict[str, str] = dict(reply.get("headers") or {})
                if "rawBody" in reply:
                    payload = str(reply["rawBody"]).encode()
                elif "body" in reply:
                    payload = json.dumps(reply["body"]).encode()
                    if not any(k.lower() == "content-type" for k in headers):
                        headers["Content-Type"] = "application/json"
                else:
                    payload = b""
                self._write(int(reply["status"]), headers, payload)

            def _write(self, status: int, headers: dict[str, str], payload: bytes) -> None:
                self.send_response(status)
                for k, v in headers.items():
                    self.send_header(k, v)
                self.send_header("Content-Length", str(len(payload)))
                self.end_headers()
                self.wfile.write(payload)

        self._server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self._server.daemon_threads = True
        if self.tls:
            c = certs()
            ctx = ssl.create_default_context(ssl.Purpose.CLIENT_AUTH)
            ctx.load_cert_chain(c.server_cert, c.server_key)
            if self.require_client_cert:
                ctx.load_verify_locations(c.ca)
                ctx.verify_mode = ssl.CERT_REQUIRED
            self._server.socket = ctx.wrap_socket(self._server.socket, server_side=True)
        self._thread = threading.Thread(target=self._server.serve_forever, args=(0.02,), daemon=True)
        self._thread.start()
        return self

    @property
    def url(self) -> str:
        host, port = self._server.server_address[:2]
        return f"{'https' if self.tls else 'http'}://{host!s}:{port}"

    def release(self) -> None:
        """Lets no_response handlers finish."""
        self._release.set()

    def stop(self) -> None:
        self._release.set()
        self._server.shutdown()
        self._server.server_close()
