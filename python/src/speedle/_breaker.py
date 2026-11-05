"""Circuit breaker (design §6.6) and the internal clock seam (design §11)."""

from __future__ import annotations

import asyncio
import random
import threading
import time
from collections.abc import Callable
from typing import Literal, Protocol

from ._types import BreakerState

Outcome = Literal["success", "failure", "neutral"]


class Clock(Protocol):
    """Time source of the retry loop and the breaker. Internal; replaced by test runners."""

    def now(self) -> float: ...
    def sleep(self, seconds: float) -> None: ...
    async def async_sleep(self, seconds: float) -> None: ...


class SystemClock:
    def now(self) -> float:
        return time.monotonic()

    def sleep(self, seconds: float) -> None:
        time.sleep(seconds)

    async def async_sleep(self, seconds: float) -> None:
        await asyncio.sleep(seconds)


SYSTEM_CLOCK: Clock = SystemClock()
SYSTEM_RANDOM: Callable[[], float] = random.random


class Ticket:
    __slots__ = ("trial",)

    def __init__(self, trial: bool) -> None:
        self.trial = trial


class Breaker:
    """One breaker per client; counts calls, not attempts. Thread-safe."""

    def __init__(
        self,
        threshold: int,
        cool_down: float,
        clock: Clock,
        on_change: Callable[[BreakerState, BreakerState], None],
    ) -> None:
        self._threshold = threshold
        self._cool_down = cool_down
        self._clock = clock
        self._on_change = on_change
        self._lock = threading.Lock()
        self._state: BreakerState = "closed"
        self._failures = 0
        self._opened_at = 0.0
        self._trial_in_flight = False

    def acquire(self) -> Ticket | None:
        changes: list[tuple[BreakerState, BreakerState]] = []
        with self._lock:
            ticket: Ticket | None
            if self._state == "open" and self._clock.now() - self._opened_at >= self._cool_down:
                changes.append(self._move("half_open"))
            if self._state == "open":
                ticket = None
            elif self._state == "half_open":
                if self._trial_in_flight:
                    ticket = None
                else:
                    self._trial_in_flight = True
                    ticket = Ticket(True)
            else:
                ticket = Ticket(False)
        self._emit(changes)
        return ticket

    def record(self, ticket: Ticket, outcome: Outcome) -> None:
        changes: list[tuple[BreakerState, BreakerState]] = []
        with self._lock:
            if ticket.trial:
                self._trial_in_flight = False
                if self._state == "half_open":
                    if outcome == "success":
                        self._failures = 0
                        changes.append(self._move("closed"))
                    elif outcome == "failure":
                        changes.append(self._open())
                    # neutral: stay half_open; the next call becomes the trial.
            elif self._state == "closed":
                if outcome == "success":
                    self._failures = 0
                elif outcome == "failure":
                    self._failures += 1
                    if self._failures >= self._threshold:
                        changes.append(self._open())
        self._emit(changes)

    def _move(self, to: BreakerState) -> tuple[BreakerState, BreakerState]:
        change = (self._state, to)
        self._state = to
        return change

    def _open(self) -> tuple[BreakerState, BreakerState]:
        self._failures = 0
        self._opened_at = self._clock.now()
        return self._move("open")

    def _emit(self, changes: list[tuple[BreakerState, BreakerState]]) -> None:
        # Hooks run outside the lock.
        for from_state, to_state in changes:
            self._on_change(from_state, to_state)
