/*
 * Copyright (c) 2026 The Speedle Authors
 * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 */
package io.speedle.sdk;

import java.time.Duration;
import java.util.OptionalLong;

/** Fields shared by every SDK error. {@code status} 0 means no HTTP response. */
record ErrorDetails(
    String message,
    int status,
    String code,
    OptionalLong quotaDailyRemaining,
    Duration retryAfter,
    Throwable cause) {

  static ErrorDetails of(String message) {
    return new ErrorDetails(message, 0, null, OptionalLong.empty(), null, null);
  }

  static ErrorDetails of(String message, Throwable cause) {
    return new ErrorDetails(message, 0, null, OptionalLong.empty(), null, cause);
  }
}
