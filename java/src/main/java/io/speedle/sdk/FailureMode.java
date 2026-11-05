/*
 * Copyright (c) 2026 The Speedle Authors
 * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 */
package io.speedle.sdk;

/** What {@link SpeedleClient#isAllowed} does when a call gives up. */
public enum FailureMode {
  /** Default: throw the typed error. */
  ERROR,
  /**
   * Opt-in fail-closed: {@code isAllowed} returns {@code false} instead of throwing any SDK error.
   * {@code check} still throws, and {@link CancelledError} is never converted. There is no
   * fail-open mode.
   */
  CLOSED
}
