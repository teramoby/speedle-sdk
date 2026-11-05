/*
 * Copyright (c) 2026 The Speedle Authors
 * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 */
package io.speedle.sdk;

import java.time.Duration;

/**
 * Settings of the optional circuit breaker. The breaker counts calls, not attempts.
 *
 * @param failureThreshold consecutive failed calls that open the breaker, at least 1
 * @param coolDown how long the breaker stays open before a half-open trial, positive
 */
public record CircuitBreakerPolicy(int failureThreshold, Duration coolDown) {

  /**
   * Validates the settings.
   *
   * @throws ConfigurationError if a value is out of range
   */
  public CircuitBreakerPolicy {
    if (failureThreshold < 1) {
      throw new ConfigurationError(ErrorDetails.of("failureThreshold must be at least 1"));
    }
    if (coolDown == null || coolDown.isNegative() || coolDown.isZero()) {
      throw new ConfigurationError(ErrorDetails.of("coolDown must be positive"));
    }
  }

  /**
   * The defaults: 5 consecutive failures, 30 s cool-down.
   *
   * @return the default policy
   */
  public static CircuitBreakerPolicy defaults() {
    return new CircuitBreakerPolicy(5, Duration.ofSeconds(30));
  }
}
