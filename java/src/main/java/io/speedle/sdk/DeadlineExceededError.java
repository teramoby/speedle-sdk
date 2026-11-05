/*
 * Copyright (c) 2026 The Speedle Authors
 * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 */
package io.speedle.sdk;

/** The call's deadline ended an attempt or left no time to start one. */
public class DeadlineExceededError extends TimeoutError {
  private static final long serialVersionUID = 1L;

  DeadlineExceededError(ErrorDetails details) {
    super(details);
  }
}
