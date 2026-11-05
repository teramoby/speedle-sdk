/*
 * Copyright (c) 2026 The Speedle Authors
 * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 */
package io.speedle.sdk;

/**
 * Optional observability callbacks. They run synchronously on the calling thread and should be
 * cheap. An exception thrown by a hook is caught and ignored; it never changes the call's result.
 * Payloads never contain the token, headers, subject, action, resource, attributes or body.
 */
public interface SpeedleHooks {
  /**
   * Called after every attempt.
   *
   * @param event the attempt details
   */
  default void onAttempt(AttemptEvent event) {}

  /**
   * Called before every retry delay.
   *
   * @param event the retry details
   */
  default void onRetry(RetryEvent event) {}

  /**
   * Called when a call ends in an error.
   *
   * @param event the give-up details
   */
  default void onGiveUp(GiveUpEvent event) {}

  /**
   * Called when the circuit breaker changes state.
   *
   * @param event the transition
   */
  default void onBreakerStateChange(BreakerStateChangeEvent event) {}
}
