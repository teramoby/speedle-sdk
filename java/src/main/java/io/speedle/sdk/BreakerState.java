/*
 * Copyright (c) 2026 The Speedle Authors
 * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 */
package io.speedle.sdk;

/** Circuit breaker state. */
public enum BreakerState {
  /** Calls go through; consecutive failures are counted. */
  CLOSED,
  /** Calls fail at once with {@link CircuitOpenError} until the cool-down has passed. */
  OPEN,
  /** One trial call is let through; concurrent calls get {@link CircuitOpenError}. */
  HALF_OPEN
}
