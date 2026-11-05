/*
 * Copyright (c) 2026 The Speedle Authors
 * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 */
package io.speedle.sdk;

/** Invalid client options, raised by the builder or an {@link Auth} factory before any request. */
public class ConfigurationError extends SpeedleException {
  private static final long serialVersionUID = 1L;

  ConfigurationError(ErrorDetails details) {
    super(details);
  }
}
