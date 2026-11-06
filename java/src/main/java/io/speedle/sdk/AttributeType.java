/*
 * Copyright (c) 2026 The Speedle Authors
 * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 */
package io.speedle.sdk;

/** The four Speedle attribute types. */
public enum AttributeType {
  /** A JSON string. */
  STRING("string"),
  /** A JSON number, decoded by self-hosted Speedle as a 64-bit float. */
  NUMERIC("numeric"),
  /** A JSON boolean. */
  BOOL("bool"),
  /** A point in time, sent as RFC 3339 in UTC and stored by self-hosted Speedle as Unix seconds. */
  DATETIME("datetime");

  private final String wireName;

  AttributeType(String wireName) {
    this.wireName = wireName;
  }

  /**
   * The name used on the wire.
   *
   * @return {@code "string"}, {@code "numeric"}, {@code "bool"} or {@code "datetime"}
   */
  public String wireName() {
    return wireName;
  }
}
