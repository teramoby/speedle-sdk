/*
 * Copyright (c) 2026 The Speedle Authors
 * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 */
package io.speedle.sdk;

/**
 * Reported when the circuit breaker changes state.
 *
 * @param from the previous state
 * @param to the new state
 */
public record BreakerStateChangeEvent(BreakerState from, BreakerState to) {}
