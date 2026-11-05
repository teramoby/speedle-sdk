/*
 * Copyright (c) 2026 The Speedle Authors
 * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 */
package io.speedle.sdk;

import java.util.Arrays;
import java.util.List;

/**
 * The subject of a check: one or more principals. Subjects must come from the application's own
 * authenticated context, never from untrusted client input.
 */
public final class Subject {
  private final List<Principal> principals;

  private Subject(List<Principal> principals) {
    this.principals = principals;
  }

  /**
   * A subject with one {@code user} principal.
   *
   * @param name the user name
   * @return the subject
   * @throws InvalidRequestError if the name is {@code null} or empty
   */
  public static Subject user(String name) {
    return new Subject(List.of(Principal.user(name)));
  }

  /**
   * A subject with the given principals, in order.
   *
   * @param principals at least one principal, none {@code null}
   * @return the subject
   * @throws InvalidRequestError if the list is empty or contains {@code null}
   */
  public static Subject of(Principal... principals) {
    if (principals == null || principals.length == 0) {
      throw new InvalidRequestError(ErrorDetails.of("subject needs at least one principal"));
    }
    for (Principal p : principals) {
      if (p == null) {
        throw new InvalidRequestError(ErrorDetails.of("subject principals must not be null"));
      }
    }
    return new Subject(List.copyOf(Arrays.asList(principals)));
  }

  /**
   * The principals, in order.
   *
   * @return an unmodifiable list
   */
  public List<Principal> principals() {
    return principals;
  }

  @Override
  public boolean equals(Object o) {
    return o instanceof Subject s && s.principals.equals(principals);
  }

  @Override
  public int hashCode() {
    return principals.hashCode();
  }

  @Override
  public String toString() {
    return "Subject[" + principals.size() + " principal(s)]";
  }
}
