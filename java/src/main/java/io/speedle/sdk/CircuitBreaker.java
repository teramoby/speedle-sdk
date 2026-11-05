/*
 * Copyright (c) 2026 The Speedle Authors
 * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 */
package io.speedle.sdk;

/** One breaker per client; counts calls, not attempts. Thread-safe. */
final class CircuitBreaker {

  /** How a finished call affects the breaker. */
  enum Outcome {
    /** A 200 or 4xx response: the server answered. */
    SUCCESS,
    /** Network error, timeout, deadline or 5xx. */
    FAILURE,
    /** Anything else (for example a 3xx); changes nothing. */
    NEUTRAL
  }

  /**
   * Result of {@link #acquire}.
   *
   * @param admitted whether the call may proceed
   * @param trial whether the call is the half-open trial
   * @param change a state change caused by the acquisition, or {@code null}
   */
  record Admission(boolean admitted, boolean trial, BreakerStateChangeEvent change) {}

  private final int threshold;
  private final long coolDownNanos;

  private BreakerState state = BreakerState.CLOSED;
  private int failures;
  private long openedAt;
  private boolean trialInFlight;

  CircuitBreaker(CircuitBreakerPolicy policy) {
    this.threshold = policy.failureThreshold();
    this.coolDownNanos = policy.coolDown().toNanos();
  }

  synchronized Admission acquire(long now) {
    switch (state) {
      case CLOSED:
        return new Admission(true, false, null);
      case OPEN:
        if (now - openedAt < coolDownNanos) {
          return new Admission(false, false, null);
        }
        trialInFlight = true;
        return new Admission(true, true, move(BreakerState.HALF_OPEN));
      case HALF_OPEN:
      default:
        if (trialInFlight) {
          return new Admission(false, false, null);
        }
        trialInFlight = true;
        return new Admission(true, true, null);
    }
  }

  /** Records a finished call; returns the resulting state change or {@code null}. */
  synchronized BreakerStateChangeEvent record(Outcome outcome, boolean trial, long now) {
    if (trial) {
      trialInFlight = false;
      if (state != BreakerState.HALF_OPEN) {
        return null;
      }
      switch (outcome) {
        case SUCCESS:
          failures = 0;
          return move(BreakerState.CLOSED);
        case FAILURE:
          openedAt = now;
          return move(BreakerState.OPEN);
        default:
          return null;
      }
    }
    if (state != BreakerState.CLOSED) {
      return null;
    }
    if (outcome == Outcome.SUCCESS) {
      failures = 0;
    } else if (outcome == Outcome.FAILURE && ++failures >= threshold) {
      failures = 0;
      openedAt = now;
      return move(BreakerState.OPEN);
    }
    return null;
  }

  synchronized BreakerState state() {
    return state;
  }

  private BreakerStateChangeEvent move(BreakerState to) {
    BreakerStateChangeEvent e = new BreakerStateChangeEvent(state, to);
    state = to;
    return e;
  }
}
