/*
 * Copyright (c) 2026 The Speedle Authors
 * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 */
package io.speedle.sdk;

/**
 * The calling thread was interrupted during the call. The interrupt flag is restored. Never
 * converted to {@code false} by the fail-closed mode.
 */
public class CancelledError extends SpeedleException {
  private static final long serialVersionUID = 1L;

  CancelledError(ErrorDetails details) {
    super(details);
  }
}
