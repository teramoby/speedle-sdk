/*
 * Copyright (c) 2026 The Speedle Authors
 * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 */
package io.speedle.sdk;

/**
 * HTTP 400, or a client-side check of the request (attributes, subject, call options) failed and
 * nothing was sent.
 */
public class InvalidRequestError extends SpeedleException {
  private static final long serialVersionUID = 1L;

  InvalidRequestError(ErrorDetails details) {
    super(details);
  }
}
