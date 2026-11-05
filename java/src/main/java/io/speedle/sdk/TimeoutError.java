/*
 * Copyright (c) 2026 The Speedle Authors
 * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 */
package io.speedle.sdk;

/** The last attempt hit the per-attempt timeout. */
public class TimeoutError extends NetworkError {
  private static final long serialVersionUID = 1L;

  TimeoutError(ErrorDetails details) {
    super(details);
  }
}
