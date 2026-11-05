"""Typed errors (design §7)."""

from __future__ import annotations

__all__ = [
    "CircuitOpenError",
    "ConfigurationError",
    "DeadlineExceededError",
    "ForbiddenError",
    "HttpError",
    "InvalidRequestError",
    "NetworkError",
    "QuotaExceededError",
    "RateLimitedError",
    "RequestTooLargeError",
    "ServerError",
    "ServiceUnavailableError",
    "SpeedleError",
    "TimeoutError",
    "TooManyRequestsError",
    "UnauthorizedError",
    "UnexpectedResponseError",
]


class SpeedleError(Exception):
    """Base type of every error raised by the SDK.

    ``status`` is the HTTP status or None, ``code`` the Speedle Cloud error
    code or None, and ``quota_daily_remaining`` the value of
    ``X-Quota-Daily-Remaining`` when the header was present and valid.
    """

    def __init__(
        self,
        message: str,
        *,
        status: int | None = None,
        code: str | None = None,
        quota_daily_remaining: int | None = None,
    ) -> None:
        super().__init__(message)
        self.message = message
        self.status = status
        self.code = code
        self.quota_daily_remaining = quota_daily_remaining

    def __repr__(self) -> str:
        return f"{type(self).__name__}({self.message!r}, status={self.status!r}, code={self.code!r})"


class ConfigurationError(SpeedleError):
    """Invalid client options. Raised by the constructor and the ``Auth`` factories."""


class InvalidRequestError(SpeedleError):
    """HTTP 400, or a client-side request check failed before sending."""


class UnauthorizedError(SpeedleError):
    """HTTP 401."""


class ForbiddenError(SpeedleError):
    """HTTP 403, including Speedle Cloud ``service_mismatch``."""


class RequestTooLargeError(SpeedleError):
    """HTTP 413."""


class TooManyRequestsError(SpeedleError):
    """HTTP 429. ``retry_after`` is the ``Retry-After`` value in seconds, when usable."""

    def __init__(
        self,
        message: str,
        *,
        status: int | None = None,
        code: str | None = None,
        quota_daily_remaining: int | None = None,
        retry_after: float | None = None,
    ) -> None:
        super().__init__(message, status=status, code=code, quota_daily_remaining=quota_daily_remaining)
        self.retry_after = retry_after


class RateLimitedError(TooManyRequestsError):
    """Burst 429 (Speedle Cloud ``rate_limited``, or any 429 from a Speedle+ gateway)."""


class QuotaExceededError(TooManyRequestsError):
    """Speedle Cloud daily quota (429 ``daily_quota_exceeded``). Never retried."""


class ServiceUnavailableError(SpeedleError):
    """HTTP 503."""


class ServerError(SpeedleError):
    """HTTP 500 and other 5xx."""


class HttpError(SpeedleError):
    """Any other HTTP status, including 3xx and 404."""


class UnexpectedResponseError(SpeedleError):
    """The response body is not the expected JSON."""


class NetworkError(SpeedleError):
    """Transport failure."""


class TimeoutError(NetworkError):  # noqa: A001 - the design names it TimeoutError inside the SDK namespace
    """The last attempt hit ``attempt_timeout``. Not the built-in ``TimeoutError``."""


class DeadlineExceededError(TimeoutError):
    """The call's deadline ended an attempt or left no time to start one."""


class CircuitOpenError(SpeedleError):
    """The circuit breaker rejected the call without sending a request."""
