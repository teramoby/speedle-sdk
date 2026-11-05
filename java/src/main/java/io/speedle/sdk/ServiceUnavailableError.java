/*
 * Copyright (c) 2026 The Speedle Authors
 * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 */
package io.speedle.sdk;

/** HTTP 503. Retried while attempts and the deadline remain. */
public class ServiceUnavailableError extends SpeedleException {
  private static final long serialVersionUID = 1L;

  ServiceUnavailableError(ErrorDetails details) {
    super(details);
  }
}
