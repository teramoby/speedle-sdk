/*
 * Copyright (c) 2026 The Speedle Authors
 * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 */
package io.speedle.sdk;

/** HTTP 413: the request body exceeds the server limit. Never retried. */
public class RequestTooLargeError extends SpeedleException {
  private static final long serialVersionUID = 1L;

  RequestTooLargeError(ErrorDetails details) {
    super(details);
  }
}
