/*
 * Copyright (c) 2026 The Speedle Authors
 * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 */
package io.speedle.sdk;

import java.math.BigDecimal;
import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.Objects;
import java.util.Set;

/** JSON comparison for the conformance vectors: key order ignored, arrays ordered by default. */
final class JsonMatch {
  private JsonMatch() {}

  /** Returns {@code null} when equal, otherwise a description of the first difference. */
  static String diff(Object expected, Object actual, String pointer, Set<String> unordered) {
    if (expected instanceof Map<?, ?> em) {
      if (!(actual instanceof Map<?, ?> am)) {
        return pointer + ": expected an object";
      }
      if (!em.keySet().equals(am.keySet())) {
        return pointer + ": keys " + am.keySet() + " != " + em.keySet();
      }
      for (Object k : em.keySet()) {
        String d = diff(em.get(k), am.get(k), pointer + "/" + k, unordered);
        if (d != null) {
          return d;
        }
      }
      return null;
    }
    if (expected instanceof List<?> el) {
      if (!(actual instanceof List<?> al) || al.size() != el.size()) {
        return pointer + ": array length or type differs";
      }
      if (unordered.contains(pointer)) {
        List<Object> rest = new ArrayList<>(al);
        outer:
        for (Object e : el) {
          for (int i = 0; i < rest.size(); i++) {
            if (diff(e, rest.get(i), pointer, unordered) == null) {
              rest.remove(i);
              continue outer;
            }
          }
          return pointer + ": no match for an element";
        }
        return null;
      }
      for (int i = 0; i < el.size(); i++) {
        String d = diff(el.get(i), al.get(i), pointer + "/" + i, unordered);
        if (d != null) {
          return d;
        }
      }
      return null;
    }
    if (expected instanceof BigDecimal eb) {
      return actual instanceof BigDecimal ab && eb.compareTo(ab) == 0
          ? null
          : pointer + ": " + actual + " != " + expected;
    }
    return Objects.equals(expected, actual) ? null : pointer + ": " + actual + " != " + expected;
  }

  /** Converts parsed JSON to the native values a caller would pass to the inferred overload. */
  static Object toNative(Object v) {
    if (v == Json.NULL) {
      return null;
    }
    if (v instanceof BigDecimal b) {
      try {
        return b.longValueExact();
      } catch (ArithmeticException e) {
        return b.doubleValue();
      }
    }
    if (v instanceof List<?> l) {
      List<Object> out = new ArrayList<>();
      for (Object o : l) {
        out.add(toNative(o));
      }
      return out;
    }
    if (v instanceof Map<?, ?> m) {
      Map<String, Object> out = new java.util.LinkedHashMap<>();
      for (Map.Entry<?, ?> e : m.entrySet()) {
        out.put((String) e.getKey(), toNative(e.getValue()));
      }
      return out;
    }
    return v;
  }

  static boolean isNull(Object v) {
    return v == Json.NULL;
  }
}
