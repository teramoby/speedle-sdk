/*
 * Copyright (c) 2026 The Speedle Authors
 * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 */
package io.speedle.sdk;

/** Any other HTTP status, including 3xx (redirects are never followed) and 404. */
public class HttpError extends SpeedleException {
  private static final long serialVersionUID = 1L;

  HttpError(ErrorDetails details) {
    super(details);
  }
}
