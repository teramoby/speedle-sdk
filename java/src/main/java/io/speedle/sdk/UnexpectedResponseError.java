/*
 * Copyright (c) 2026 The Speedle Authors
 * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 */
package io.speedle.sdk;

/** The response body is not the expected JSON. */
public class UnexpectedResponseError extends SpeedleException {
  private static final long serialVersionUID = 1L;

  UnexpectedResponseError(ErrorDetails details) {
    super(details);
  }
}
