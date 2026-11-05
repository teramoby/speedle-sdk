/*
 * Copyright (c) 2026 The Speedle Authors
 * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 */
package io.speedle.sdk;

import java.time.Duration;

/**
 * Reported before every retry delay.
 *
 * @param attempt the number of the next attempt
 * @param delay the delay before it
 * @param errorType the simple class name of the error that caused the retry
 */
public record RetryEvent(int attempt, Duration delay, String errorType) {}
