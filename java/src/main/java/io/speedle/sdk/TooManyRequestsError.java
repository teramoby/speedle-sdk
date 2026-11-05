/*
 * Copyright (c) 2026 The Speedle Authors
 * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 */
package io.speedle.sdk;

import java.time.Duration;
import java.util.Optional;

/**
 * HTTP 429. Raised as is when Speedle Cloud returns an unknown code or a body that does not parse;
 * the subtypes {@link RateLimitedError} and {@link QuotaExceededError} cover the burst limit and
 * the daily quota.
 */
public class TooManyRequestsError extends SpeedleException {
  private static final long serialVersionUID = 1L;

  TooManyRequestsError(ErrorDetails details) {
    super(details);
  }

  /**
   * The {@code Retry-After} header.
   *
   * @return the delay, or empty when the header was absent or not a positive integer
   */
  public Optional<Duration> retryAfter() {
    return Optional.ofNullable(details().retryAfter());
  }
}
