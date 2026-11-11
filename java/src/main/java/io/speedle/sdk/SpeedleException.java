/*
 * Copyright (c) 2026 The Speedle Authors
 * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 */
package io.speedle.sdk;

import java.util.Optional;
import java.util.OptionalInt;
import java.util.OptionalLong;

/**
 * Base type of every error the SDK raises. Unchecked.
 *
 * <p>Messages never contain the API key or other bearer credential, the request attributes or the
 * request body.
 */
public class SpeedleException extends RuntimeException {
  private static final long serialVersionUID = 1L;

  private final transient ErrorDetails details;

  SpeedleException(ErrorDetails details) {
    super(details.message(), details.cause());
    this.details = details;
  }

  /**
   * The HTTP status of the response that caused this error.
   *
   * @return the status, or empty when there was no response
   */
  public OptionalInt status() {
    return details.status() == 0 ? OptionalInt.empty() : OptionalInt.of(details.status());
  }

  /**
   * The Speedle Cloud error code, for example {@code service_mismatch} or {@code
   * service_not_found}.
   *
   * @return the code, or empty
   */
  public Optional<String> code() {
    return Optional.ofNullable(details.code());
  }

  /**
   * The value of Speedle Cloud's {@code X-Quota-Daily-Remaining} header.
   *
   * @return the remaining daily quota, or empty when the header was absent or malformed
   */
  public OptionalLong quotaDailyRemaining() {
    return details.quotaDailyRemaining();
  }

  final ErrorDetails details() {
    return details;
  }
}
