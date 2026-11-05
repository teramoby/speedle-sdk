/*
 * Copyright (c) 2026 The Speedle Authors
 * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 */
package io.speedle.sdk;

/**
 * The reason code of a decision. Codes are the same on both backends. Unknown codes map to {@link
 * #UNKNOWN}; {@link Decision#reasonCode()} keeps the integer.
 */
public enum Reason {
  /** 0: a grant policy matched. */
  GRANT_POLICY_FOUND,
  /** 1: a deny policy matched. */
  DENY_POLICY_FOUND,
  /** 2: the service name is unknown to the server. */
  SERVICE_NOT_FOUND,
  /** 3: no policy applied. */
  NO_APPLICABLE_POLICIES,
  /** 4: evaluation failed; see {@link Decision#errorMessage()}. */
  ERROR_IN_EVALUATION,
  /** 5: the server runs in discover mode and allows everything. This is not a production grant. */
  DISCOVER_MODE,
  /** 6: no reason available. */
  REASON_NOT_AVAILABLE,
  /** A code this SDK version does not know. */
  UNKNOWN;

  private static final Reason[] KNOWN = {
    GRANT_POLICY_FOUND,
    DENY_POLICY_FOUND,
    SERVICE_NOT_FOUND,
    NO_APPLICABLE_POLICIES,
    ERROR_IN_EVALUATION,
    DISCOVER_MODE,
    REASON_NOT_AVAILABLE
  };

  /**
   * Maps a wire code to a reason.
   *
   * @param code the integer from the response
   * @return the matching reason, or {@link #UNKNOWN}
   */
  public static Reason fromCode(int code) {
    return code >= 0 && code < KNOWN.length ? KNOWN[code] : UNKNOWN;
  }
}
