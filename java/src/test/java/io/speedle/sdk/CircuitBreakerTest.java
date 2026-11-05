/*
 * Copyright (c) 2026 The Speedle Authors
 * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 */
package io.speedle.sdk;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.time.Duration;
import org.junit.jupiter.api.Test;

class CircuitBreakerTest {
  private static final long SEC = 1_000_000_000L;

  private final CircuitBreaker b =
      new CircuitBreaker(new CircuitBreakerPolicy(2, Duration.ofSeconds(10)));

  @Test
  void opensAfterConsecutiveFailuresAndResetsOnSuccess() {
    b.record(CircuitBreaker.Outcome.FAILURE, false, 0);
    b.record(CircuitBreaker.Outcome.SUCCESS, false, 0); // a 4xx or 200 resets the count
    assertNull(b.record(CircuitBreaker.Outcome.FAILURE, false, 0));
    b.record(CircuitBreaker.Outcome.NEUTRAL, false, 0); // neutral changes nothing
    BreakerStateChangeEvent e = b.record(CircuitBreaker.Outcome.FAILURE, false, 0);
    assertEquals(new BreakerStateChangeEvent(BreakerState.CLOSED, BreakerState.OPEN), e);
    assertFalse(b.acquire(9 * SEC).admitted());
  }

  @Test
  void halfOpenAdmitsExactlyOneTrial() {
    b.record(CircuitBreaker.Outcome.FAILURE, false, 0);
    b.record(CircuitBreaker.Outcome.FAILURE, false, 0);
    CircuitBreaker.Admission trial = b.acquire(10 * SEC);
    assertTrue(trial.admitted() && trial.trial());
    assertEquals(BreakerState.HALF_OPEN, trial.change().to());
    assertFalse(b.acquire(10 * SEC).admitted(), "concurrent call rejected");
    // A cancelled trial frees the slot without a state change.
    assertNull(b.record(CircuitBreaker.Outcome.NEUTRAL, true, 10 * SEC));
    CircuitBreaker.Admission again = b.acquire(11 * SEC);
    assertTrue(again.trial());
    BreakerStateChangeEvent reopen = b.record(CircuitBreaker.Outcome.FAILURE, true, 11 * SEC);
    assertEquals(BreakerState.OPEN, reopen.to());
    assertFalse(b.acquire(20 * SEC).admitted(), "new cool-down");
    assertTrue(b.acquire(21 * SEC).trial());
    assertEquals(
        BreakerState.CLOSED, b.record(CircuitBreaker.Outcome.SUCCESS, true, 21 * SEC).to());
    assertTrue(b.acquire(21 * SEC).admitted());
  }
}
