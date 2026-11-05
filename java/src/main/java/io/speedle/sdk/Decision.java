/*
 * Copyright (c) 2026 The Speedle Authors
 * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 */
package io.speedle.sdk;

import java.util.Optional;
import java.util.OptionalLong;

/**
 * The server's answer to one {@code is-allowed} call.
 *
 * @param allowed the server's {@code allowed} value
 * @param reason the reason, {@link Reason#UNKNOWN} for codes this SDK does not know
 * @param reasonCode the reason as sent by the server
 * @param errorMessage the evaluator error (Speedle+), in which case {@code allowed} is false
 * @param quotaDailyRemaining Speedle Cloud's {@code X-Quota-Daily-Remaining} header, when present
 */
public record Decision(
    boolean allowed,
    Reason reason,
    int reasonCode,
    Optional<String> errorMessage,
    OptionalLong quotaDailyRemaining) {}
