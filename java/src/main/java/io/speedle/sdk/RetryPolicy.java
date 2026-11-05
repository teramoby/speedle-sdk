/*
 * Copyright (c) 2026 The Speedle Authors
 * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 */
package io.speedle.sdk;

import java.time.Duration;

/**
 * Retry, timeout and deadline settings.
 *
 * <p>Before attempt {@code n + 1} the client sleeps a uniformly random delay in {@code [0,
 * min(maxDelay, baseDelay * 2^(n-1))]}, raised to the server's {@code Retry-After} when present.
 * Each attempt gets {@code min(attemptTimeout, remaining deadline)}.
 *
 * <p>On Speedle Cloud every attempt that reaches the handler is charged against the daily quota,
 * including a retried {@code snapshot_unavailable} 503; use {@link #disabled()} to turn retries
 * off.
 *
 * @param maxAttempts attempts per call, counting the first; 1 disables retries
 * @param baseDelay backoff base, not negative
 * @param maxDelay backoff cap, not negative
 * @param attemptTimeout bound for one attempt (connect, TLS, request and response), positive
 * @param deadline bound for the whole call, attempts and delays included, positive
 */
public record RetryPolicy(
    int maxAttempts,
    Duration baseDelay,
    Duration maxDelay,
    Duration attemptTimeout,
    Duration deadline) {

  /**
   * Validates the settings.
   *
   * @throws ConfigurationError if a value is out of range
   */
  public RetryPolicy {
    if (maxAttempts < 1) {
      throw new ConfigurationError(ErrorDetails.of("maxAttempts must be at least 1"));
    }
    requireNonNegative(baseDelay, "baseDelay");
    requireNonNegative(maxDelay, "maxDelay");
    requirePositive(attemptTimeout, "attemptTimeout");
    requirePositive(deadline, "deadline");
  }

  /**
   * The defaults: 3 attempts, 100 ms base delay, 2 s cap, 2 s per attempt, 5 s deadline.
   *
   * @return the default policy
   */
  public static RetryPolicy defaults() {
    return new RetryPolicy(
        3,
        Duration.ofMillis(100),
        Duration.ofSeconds(2),
        Duration.ofSeconds(2),
        Duration.ofSeconds(5));
  }

  /**
   * One attempt per call; the other values are the defaults.
   *
   * @return a policy without retries
   */
  public static RetryPolicy disabled() {
    RetryPolicy d = defaults();
    return new RetryPolicy(1, d.baseDelay, d.maxDelay, d.attemptTimeout, d.deadline);
  }

  private static void requireNonNegative(Duration d, String name) {
    if (d == null || d.isNegative()) {
      throw new ConfigurationError(ErrorDetails.of(name + " must not be negative"));
    }
  }

  static void requirePositive(Duration d, String name) {
    if (d == null || d.isNegative() || d.isZero()) {
      throw new ConfigurationError(ErrorDetails.of(name + " must be positive"));
    }
  }
}
