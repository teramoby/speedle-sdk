/*
 * Copyright (c) 2026 The Speedle Authors
 * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 */
package io.speedle.sdk;

import java.time.Instant;
import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.Objects;

/**
 * One Speedle+ {@code JsonAttribute}: a name, a type and a single value or a list of values of that
 * type. {@code null} names, values or list items, empty names and non-finite numbers throw {@link
 * InvalidRequestError}.
 *
 * <p>Numbers are sent as 64-bit floats; integers above 2^53 lose precision on the server.
 */
public final class Attribute {
  private final String name;
  private final AttributeType type;
  private final boolean isArray;
  private final Object value;

  private Attribute(String name, AttributeType type, boolean isArray, Object value) {
    this.name = name;
    this.type = type;
    this.isArray = isArray;
    this.value = value;
  }

  /**
   * A {@code string} attribute.
   *
   * @param name the attribute name
   * @param value the value
   * @return the attribute
   */
  public static Attribute string(String name, String value) {
    return single(name, AttributeType.STRING, value);
  }

  /**
   * A {@code string} array attribute.
   *
   * @param name the attribute name
   * @param values the values, possibly empty
   * @return the attribute
   */
  public static Attribute string(String name, List<String> values) {
    return array(name, AttributeType.STRING, values);
  }

  /**
   * A {@code numeric} attribute.
   *
   * @param name the attribute name
   * @param value a finite number
   * @return the attribute
   */
  public static Attribute numeric(String name, double value) {
    return single(name, AttributeType.NUMERIC, finite(value));
  }

  /**
   * A {@code numeric} array attribute.
   *
   * @param name the attribute name
   * @param values finite numbers, possibly empty
   * @return the attribute
   */
  public static Attribute numeric(String name, List<? extends Number> values) {
    requireName(name);
    List<Double> out = new ArrayList<>();
    for (Number n : requireList(values)) {
      if (n == null) {
        throw nullValue();
      }
      out.add(finite(n.doubleValue()));
    }
    return new Attribute(name, AttributeType.NUMERIC, true, Collections.unmodifiableList(out));
  }

  /**
   * A {@code bool} attribute.
   *
   * @param name the attribute name
   * @param value the value
   * @return the attribute
   */
  public static Attribute bool(String name, boolean value) {
    return single(name, AttributeType.BOOL, value);
  }

  /**
   * A {@code bool} array attribute.
   *
   * @param name the attribute name
   * @param values the values, possibly empty
   * @return the attribute
   */
  public static Attribute bool(String name, List<Boolean> values) {
    return array(name, AttributeType.BOOL, values);
  }

  /**
   * A {@code datetime} attribute, sent as RFC 3339 in UTC.
   *
   * @param name the attribute name
   * @param value the instant
   * @return the attribute
   */
  public static Attribute datetime(String name, Instant value) {
    return single(name, AttributeType.DATETIME, value);
  }

  /**
   * A {@code datetime} array attribute.
   *
   * @param name the attribute name
   * @param values the instants, possibly empty
   * @return the attribute
   */
  public static Attribute datetime(String name, List<Instant> values) {
    return array(name, AttributeType.DATETIME, values);
  }

  /**
   * The name.
   *
   * @return the attribute name
   */
  public String name() {
    return name;
  }

  /**
   * The type.
   *
   * @return the attribute type
   */
  public AttributeType type() {
    return type;
  }

  /**
   * Whether this is the array form.
   *
   * @return true for a list of values
   */
  public boolean isArray() {
    return isArray;
  }

  /**
   * The value: a {@code String}, {@code Double}, {@code Boolean} or {@code Instant}, or an
   * unmodifiable {@code List} of one of these.
   *
   * @return the value
   */
  public Object value() {
    return value;
  }

  @Override
  public boolean equals(Object o) {
    return o instanceof Attribute a
        && a.name.equals(name)
        && a.type == type
        && a.isArray == isArray
        && a.value.equals(value);
  }

  @Override
  public int hashCode() {
    return Objects.hash(name, type, isArray, value);
  }

  /** Omits the value, so attributes never end up in logs by accident. */
  @Override
  public String toString() {
    return "Attribute[name=" + name + ", type=" + type.wireName() + (isArray ? "[]" : "") + "]";
  }

  private static Attribute single(String name, AttributeType type, Object value) {
    requireName(name);
    if (value == null) {
      throw nullValue();
    }
    return new Attribute(name, type, false, value);
  }

  private static <T> Attribute array(String name, AttributeType type, List<T> values) {
    requireName(name);
    List<T> out = new ArrayList<>();
    for (T v : requireList(values)) {
      if (v == null) {
        throw nullValue();
      }
      out.add(v);
    }
    return new Attribute(name, type, true, Collections.unmodifiableList(out));
  }

  private static void requireName(String name) {
    if (name == null || name.isEmpty()) {
      throw new InvalidRequestError(ErrorDetails.of("attribute name must not be empty"));
    }
  }

  private static <T> List<T> requireList(List<T> values) {
    if (values == null) {
      throw nullValue();
    }
    return values;
  }

  private static InvalidRequestError nullValue() {
    return new InvalidRequestError(ErrorDetails.of("attribute values must not be null"));
  }

  private static double finite(double d) {
    if (Double.isNaN(d) || Double.isInfinite(d)) {
      throw new InvalidRequestError(ErrorDetails.of("numeric attributes must be finite"));
    }
    return d;
  }
}
