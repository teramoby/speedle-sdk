/*
 * Copyright (c) 2026 The Speedle Authors
 * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 */
package io.speedle.sdk;

/** The circuit breaker rejected the call; no request was sent. */
public class CircuitOpenError extends SpeedleException {
  private static final long serialVersionUID = 1L;

  CircuitOpenError(ErrorDetails details) {
    super(details);
  }
}
