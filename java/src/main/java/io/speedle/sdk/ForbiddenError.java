/*
 * Copyright (c) 2026 The Speedle Authors
 * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 */
package io.speedle.sdk;

/**
 * HTTP 403, including Speedle Cloud {@code service_mismatch} when {@code serviceName} is not the
 * token's service. Never retried.
 */
public class ForbiddenError extends SpeedleException {
  private static final long serialVersionUID = 1L;

  ForbiddenError(ErrorDetails details) {
    super(details);
  }
}
