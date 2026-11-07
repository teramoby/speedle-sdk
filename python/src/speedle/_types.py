"""Public value types (design §5.2)."""

from __future__ import annotations

from collections.abc import Callable, Sequence
from dataclasses import dataclass
from enum import IntEnum
from typing import TYPE_CHECKING, Literal, Union

if TYPE_CHECKING:
    from ._errors import SpeedleError

__all__ = [
    "AttemptEvent",
    "Backend",
    "BreakerState",
    "BreakerStateChangeEvent",
    "CircuitBreaker",
    "Decision",
    "FailureMode",
    "GiveUpEvent",
    "GiveUpReason",
    "Hooks",
    "Principal",
    "PrincipalType",
    "Reason",
    "RetryEvent",
    "RetryPolicy",
    "Subject",
]

Backend = Literal["speedle-cloud", "speedle"]
FailureMode = Literal["error", "closed"]
BreakerState = Literal["closed", "open", "half_open"]
GiveUpReason = Literal[
    "non_retryable", "attempts_exhausted", "deadline", "retry_after_exceeds_deadline", "circuit_open"
]


class PrincipalType:
    """Constants for the Speedle principal types; any string is accepted."""

    USER = "user"
    GROUP = "group"
    ROLE = "role"
    ENTITY = "entity"


@dataclass(frozen=True)
class Principal:
    name: str
    type: str = "user"
    idd: str | None = None


Subject = Union[str, Sequence[Principal]]


@dataclass(frozen=True)
class RetryPolicy:
    """Retry settings; all durations in seconds."""

    max_attempts: int = 3
    base_delay: float = 0.1
    max_delay: float = 2.0
    attempt_timeout: float = 2.0
    deadline: float = 5.0


@dataclass(frozen=True)
class CircuitBreaker:
    """Circuit breaker settings; passing one turns the breaker on."""

    failure_threshold: int = 5
    cool_down: float = 30.0


@dataclass(frozen=True)
class AttemptEvent:
    attempt: int
    duration: float
    status: int | None
    error_type: str | None


@dataclass(frozen=True)
class RetryEvent:
    attempt: int  # the next attempt
    delay: float
    error_type: str


@dataclass(frozen=True)
class GiveUpEvent:
    attempts: int
    reason: GiveUpReason
    error: SpeedleError


@dataclass(frozen=True)
class BreakerStateChangeEvent:
    from_state: BreakerState
    to_state: BreakerState


@dataclass(frozen=True)
class Hooks:
    """Synchronous, cheap callbacks. Exceptions and return values are ignored."""

    on_attempt: Callable[[AttemptEvent], None] | None = None
    on_retry: Callable[[RetryEvent], None] | None = None
    on_give_up: Callable[[GiveUpEvent], None] | None = None
    on_breaker_state_change: Callable[[BreakerStateChangeEvent], None] | None = None


class Reason(IntEnum):
    GRANT_POLICY_FOUND = 0
    DENY_POLICY_FOUND = 1
    SERVICE_NOT_FOUND = 2
    NO_APPLICABLE_POLICIES = 3
    ERROR_IN_EVALUATION = 4
    DISCOVER_MODE = 5
    """``allowed`` is true, but this is not a production grant."""
    REASON_NOT_AVAILABLE = 6


@dataclass(frozen=True)
class Decision:
    allowed: bool
    reason: Reason | int
    error_message: str | None = None
    quota_daily_remaining: int | None = None
