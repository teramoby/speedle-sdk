/*
 * Copyright (c) 2026 The Speedle Authors
 * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 */
package io.speedle.sdk;

/**
 * A transport failure: DNS, connection refused or reset, a connection closed before a full
 * response, or a TLS failure. TLS failures are not retried; the others are.
 */
public class NetworkError extends SpeedleException {
  private static final long serialVersionUID = 1L;

  NetworkError(ErrorDetails details) {
    super(details);
  }
}
