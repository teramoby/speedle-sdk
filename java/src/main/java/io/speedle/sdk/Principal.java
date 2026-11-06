/*
 * Copyright (c) 2026 The Speedle Authors
 * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 */
package io.speedle.sdk;

/**
 * One principal of a subject. The type is an open string; the constants name the four Speedle
 * types.
 *
 * @param type the principal type; {@code null} or empty means {@link #USER}
 * @param name the principal name, required
 * @param idd the identity domain, sent only when not empty
 */
public record Principal(String type, String name, String idd) {

  /** Principal type {@code user}. */
  public static final String USER = "user";

  /** Principal type {@code group}. */
  public static final String GROUP = "group";

  /** Principal type {@code role}. */
  public static final String ROLE = "role";

  /** Principal type {@code entity}. */
  public static final String ENTITY = "entity";

  /**
   * Validates and normalises the principal.
   *
   * @throws InvalidRequestError if the name is {@code null} or empty
   */
  public Principal {
    if (name == null || name.isEmpty()) {
      throw new InvalidRequestError(ErrorDetails.of("principal name must not be empty"));
    }
    if (type == null || type.isEmpty()) {
      type = USER;
    }
    if (idd != null && idd.isEmpty()) {
      idd = null;
    }
  }

  /**
   * A {@code user} principal.
   *
   * @param name the user name
   * @return the principal
   */
  public static Principal user(String name) {
    return new Principal(USER, name, null);
  }

  /**
   * A {@code group} principal.
   *
   * @param name the group name
   * @return the principal
   */
  public static Principal group(String name) {
    return new Principal(GROUP, name, null);
  }

  @Override
  public String toString() {
    return "Principal[type=" + type + "]";
  }
}
