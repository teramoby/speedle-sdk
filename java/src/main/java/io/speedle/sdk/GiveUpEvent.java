/*
 * Copyright (c) 2026 The Speedle Authors
 * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 */
package io.speedle.sdk;

/**
 * Reported when a call ends in an error, in every failure mode.
 *
 * @param attempts the number of attempts made (0 when no request was sent)
 * @param reason why the call gave up
 * @param error the error the call raises, or would raise outside fail-closed mode
 */
public record GiveUpEvent(int attempts, GiveUpReason reason, SpeedleException error) {}
