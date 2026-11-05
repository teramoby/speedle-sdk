/*
 * Copyright (c) 2026 The Speedle Authors
 * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 */
package io.speedle.sdk;

/** HTTP 401: the bearer token was missing, malformed, revoked or unknown. Never retried. */
public class UnauthorizedError extends SpeedleException {
  private static final long serialVersionUID = 1L;

  UnauthorizedError(ErrorDetails details) {
    super(details);
  }
}
