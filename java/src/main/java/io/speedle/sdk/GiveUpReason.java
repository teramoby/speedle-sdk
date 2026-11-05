/*
 * Copyright (c) 2026 The Speedle Authors
 * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 */
package io.speedle.sdk;

/** Why a call ended in an error, reported by {@link SpeedleHooks#onGiveUp}. */
public enum GiveUpReason {
  /** The outcome is not retryable. */
  NON_RETRYABLE,
  /** The last allowed attempt failed with a retryable outcome. */
  ATTEMPTS_EXHAUSTED,
  /** The call's deadline ended an attempt or left no time for another one. */
  DEADLINE,
  /** The server's {@code Retry-After} does not fit the remaining deadline. */
  RETRY_AFTER_EXCEEDS_DEADLINE,
  /** The circuit breaker rejected the call. */
  CIRCUIT_OPEN
}
