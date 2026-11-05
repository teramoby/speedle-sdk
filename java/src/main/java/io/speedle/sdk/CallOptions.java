/*
 * Copyright (c) 2026 The Speedle Authors
 * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 */
package io.speedle.sdk;

import java.time.Duration;
import java.util.Optional;

/**
 * Per-call settings. Each value, when present, overrides the client setting for this call only.
 *
 * @param deadline replaces the client's {@link RetryPolicy#deadline()} for this call
 * @param serviceName replaces the client's service name for this call
 */
public record CallOptions(Optional<Duration> deadline, Optional<String> serviceName) {

  private static final CallOptions NONE = new CallOptions(Optional.empty(), Optional.empty());

  /** Normalises {@code null} to empty. */
  public CallOptions {
    deadline = deadline == null ? Optional.empty() : deadline;
    serviceName = serviceName == null ? Optional.empty() : serviceName;
  }

  /**
   * Options with only a deadline.
   *
   * @param deadline the deadline for this call, positive
   * @return the options
   */
  public static CallOptions withDeadline(Duration deadline) {
    return NONE.deadline(deadline);
  }

  /**
   * Options with only a service name.
   *
   * @param serviceName the service name for this call, not empty
   * @return the options
   */
  public static CallOptions withServiceName(String serviceName) {
    return NONE.serviceName(serviceName);
  }

  /**
   * A copy with the deadline set.
   *
   * @param deadline the deadline for this call, positive
   * @return the new options
   */
  public CallOptions deadline(Duration deadline) {
    return new CallOptions(Optional.ofNullable(deadline), serviceName);
  }

  /**
   * A copy with the service name set.
   *
   * @param serviceName the service name for this call, not empty
   * @return the new options
   */
  public CallOptions serviceName(String serviceName) {
    return new CallOptions(deadline, Optional.ofNullable(serviceName));
  }

  static CallOptions none() {
    return NONE;
  }
}
