/*
 * Copyright (c) 2026 The Speedle Authors
 * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 */
package io.speedle.sdk;

/**
 * HTTP 429 {@code daily_quota_exceeded} from Speedle Cloud. Never retried; {@link #retryAfter()}
 * points at the next 00:00 UTC.
 */
public class QuotaExceededError extends TooManyRequestsError {
  private static final long serialVersionUID = 1L;

  QuotaExceededError(ErrorDetails details) {
    super(details);
  }
}
