/*
 * Copyright (c) 2026 The Speedle Authors
 * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 */
package io.speedle.sdk;

/**
 * A burst HTTP 429 ({@code rate_limited} on Speedle Cloud, any 429 from a gateway in front of
 * self-hosted Speedle). Retried only when {@code Retry-After} fits the remaining deadline.
 */
public class RateLimitedError extends TooManyRequestsError {
  private static final long serialVersionUID = 1L;

  RateLimitedError(ErrorDetails details) {
    super(details);
  }
}
