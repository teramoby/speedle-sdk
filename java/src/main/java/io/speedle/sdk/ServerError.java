/*
 * Copyright (c) 2026 The Speedle Authors
 * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 */
package io.speedle.sdk;

/** HTTP 500 and other 5xx responses; 502 and 504 are retried, the others are not. */
public class ServerError extends SpeedleException {
  private static final long serialVersionUID = 1L;

  ServerError(ErrorDetails details) {
    super(details);
  }
}
