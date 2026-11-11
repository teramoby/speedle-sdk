"""Pluggable client authentication (design §6.2)."""

from __future__ import annotations

import re
from typing import Literal

from ._errors import ConfigurationError

__all__ = ["Auth"]

_BAD_TOKEN = re.compile(r"[\s\x00-\x1f\x7f-\x9f]")

AuthKind = Literal["none", "bearer", "mutual-tls"]


class Auth:
    """Client authentication. Build it with :meth:`none`, :meth:`bearer` or :meth:`mutual_tls`."""

    __slots__ = ("_cert_file", "_key_file", "_kind", "_token")

    _kind: AuthKind
    _token: str | None
    _cert_file: str | None
    _key_file: str | None

    def __init__(self) -> None:
        raise TypeError("use Auth.none(), Auth.bearer() or Auth.mutual_tls()")

    @classmethod
    def _make(
        cls, kind: AuthKind, token: str | None = None, cert_file: str | None = None, key_file: str | None = None
    ) -> Auth:
        auth = object.__new__(cls)
        object.__setattr__(auth, "_kind", kind)
        object.__setattr__(auth, "_token", token)
        object.__setattr__(auth, "_cert_file", cert_file)
        object.__setattr__(auth, "_key_file", key_file)
        return auth

    def __setattr__(self, name: str, value: object) -> None:
        raise AttributeError("Auth is immutable")

    @property
    def kind(self) -> AuthKind:
        return self._kind

    @staticmethod
    def none() -> Auth:
        """No credential: self-hosted Speedle on a trusted network or loopback."""
        return Auth._make("none")

    @staticmethod
    def bearer(token: str) -> Auth:
        """``Authorization: Bearer <token>``.

        On Speedle Cloud, ``token`` is the account-level API key. The parameter
        name is unchanged. Requires HTTPS.
        """
        if not isinstance(token, str) or token == "":
            raise ConfigurationError("bearer token is empty")
        if _BAD_TOKEN.search(token):
            raise ConfigurationError("bearer token contains whitespace or control characters")
        return Auth._make("bearer", token=token)

    @staticmethod
    def mutual_tls(cert_file: str, key_file: str) -> Auth:
        """Presents a client certificate (PEM files) on the TLS handshake."""
        if not isinstance(cert_file, str) or not cert_file or not isinstance(key_file, str) or not key_file:
            raise ConfigurationError("mutual_tls needs a certificate file and a key file")
        return Auth._make("mutual-tls", cert_file=cert_file, key_file=key_file)

    def __repr__(self) -> str:
        if self._kind == "none":
            return "Auth.none()"
        if self._kind == "bearer":
            return "Auth.bearer(<redacted>)"
        return f"Auth.mutual_tls({self._cert_file!r}, <key>)"

    __str__ = __repr__

    def __reduce__(self) -> str | tuple[object, ...]:
        raise TypeError("Auth cannot be pickled")
