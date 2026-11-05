/*
 * Copyright (c) 2026 The Speedle Authors
 * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 */
package io.speedle.sdk;

import java.time.Duration;
import java.util.Optional;
import java.util.OptionalInt;

/**
 * Reported after every attempt.
 *
 * @param attempt the attempt number, starting at 1
 * @param duration how long the attempt took
 * @param status the HTTP status, or empty when there was no response
 * @param errorType the simple class name of the attempt's error, or empty on success
 */
public record AttemptEvent(
    int attempt, Duration duration, OptionalInt status, Optional<String> errorType) {}
