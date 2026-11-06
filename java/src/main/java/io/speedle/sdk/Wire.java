/*
 * Copyright (c) 2026 The Speedle Authors
 * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 */
package io.speedle.sdk;

import java.math.BigDecimal;
import java.time.Duration;
import java.time.Instant;
import java.time.format.DateTimeFormatter;
import java.util.ArrayList;
import java.util.HashSet;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.OptionalLong;
import java.util.Set;

/** Request encoding and response decoding for {@code POST /authz-check/v1/is-allowed}. */
final class Wire {

  static final String PATH = "/authz-check/v1/is-allowed";

  /** The error codes of the Speedle Cloud contract; other {@code error} values are messages. */
  static final Set<String> CLOUD_CODES =
      Set.of(
          "invalid_request",
          "unsupported_token_assertion",
          "unauthorized",
          "service_mismatch",
          "request_too_large",
          "rate_limited",
          "daily_quota_exceeded",
          "internal_error",
          "quota_unavailable",
          "snapshot_unavailable");

  private static final int MAX_MESSAGE = 512;
  private static final Instant MIN_INSTANT = Instant.parse("0000-01-01T00:00:00Z");
  private static final Instant MAX_INSTANT = Instant.parse("9999-12-31T23:59:59.999999999Z");

  private Wire() {}

  // ------------------------------------------------------------ request

  /** Builds the Speedle {@code JsonContext}; throws {@link InvalidRequestError}. */
  static String encodeRequest(
      Subject subject,
      String serviceName,
      String resource,
      String action,
      List<Attribute> attributes) {
    if (subject == null) {
      throw invalid("subject must not be null");
    }
    if (resource == null) {
      throw invalid("resource must not be null");
    }
    if (action == null) {
      throw invalid("action must not be null");
    }
    StringBuilder b = new StringBuilder(256);
    b.append("{\"subject\":{\"principals\":[");
    boolean first = true;
    for (Principal p : subject.principals()) {
      if (!first) {
        b.append(',');
      }
      first = false;
      b.append("{\"type\":");
      Json.writeString(b, p.type());
      b.append(",\"name\":");
      Json.writeString(b, p.name());
      if (p.idd() != null) {
        b.append(",\"idd\":");
        Json.writeString(b, p.idd());
      }
      b.append('}');
    }
    b.append("]},\"serviceName\":");
    Json.writeString(b, serviceName);
    b.append(",\"resource\":");
    Json.writeString(b, resource);
    b.append(",\"action\":");
    Json.writeString(b, action);
    if (attributes != null && !attributes.isEmpty()) {
      Set<String> names = new HashSet<>();
      b.append(",\"attributes\":[");
      first = true;
      for (Attribute a : attributes) {
        if (a == null) {
          throw invalid("attributes must not contain null");
        }
        if (!names.add(a.name())) {
          throw invalid("duplicate attribute name");
        }
        if (!first) {
          b.append(',');
        }
        first = false;
        b.append("{\"name\":");
        Json.writeString(b, a.name());
        b.append(",\"type\":\"").append(a.type().wireName()).append("\",\"value\":");
        if (a.isArray()) {
          b.append('[');
          boolean f = true;
          for (Object v : (List<?>) a.value()) {
            if (!f) {
              b.append(',');
            }
            f = false;
            writeValue(b, v);
          }
          b.append(']');
        } else {
          writeValue(b, a.value());
        }
        b.append('}');
      }
      b.append(']');
    }
    b.append('}');
    return b.toString();
  }

  private static void writeValue(StringBuilder b, Object v) {
    if (v instanceof String s) {
      Json.writeString(b, s);
    } else if (v instanceof Double d) {
      Json.writeNumber(b, d);
    } else if (v instanceof Boolean bool) {
      b.append(bool.booleanValue());
    } else if (v instanceof Instant i) {
      if (i.isBefore(MIN_INSTANT) || i.isAfter(MAX_INSTANT)) {
        throw invalid("datetime attributes must be within the years 0000 to 9999");
      }
      Json.writeString(b, formatInstant(i));
    } else {
      throw new IllegalStateException("unexpected attribute value type");
    }
  }

  /** RFC 3339 in UTC; fractional seconds only when not zero. */
  static String formatInstant(Instant i) {
    return DateTimeFormatter.ISO_INSTANT.format(i);
  }

  /** The inferred form: builds typed attributes, sorted by name in code point order. */
  static List<Attribute> infer(Map<String, ?> map) {
    if (map == null || map.isEmpty()) {
      return List.of();
    }
    List<String> names = new ArrayList<>();
    for (String k : map.keySet()) {
      if (k == null || k.isEmpty()) {
        throw invalid("attribute name must not be empty");
      }
      names.add(k);
    }
    names.sort(Wire::compareCodePoints);
    List<Attribute> out = new ArrayList<>(names.size());
    for (String name : names) {
      out.add(inferOne(name, map.get(name)));
    }
    return out;
  }

  private static Attribute inferOne(String name, Object v) {
    if (v instanceof String s) {
      return Attribute.string(name, s);
    }
    if (v instanceof Boolean bool) {
      return Attribute.bool(name, bool);
    }
    if (v instanceof Number n) {
      return Attribute.numeric(name, n.doubleValue());
    }
    if (v instanceof Instant i) {
      return Attribute.datetime(name, i);
    }
    if (v instanceof List<?> list && !list.isEmpty()) {
      AttributeType t = null;
      for (Object item : list) {
        AttributeType it = scalarType(item);
        if (it == null || (t != null && t != it)) {
          throw invalid("an inferred list must hold values of one supported type");
        }
        t = it;
      }
      List<Object> items = new ArrayList<>(list);
      return switch (t) {
        case STRING -> Attribute.string(name, castList(items, String.class));
        case BOOL -> Attribute.bool(name, castList(items, Boolean.class));
        case NUMERIC -> Attribute.numeric(name, castList(items, Number.class));
        case DATETIME -> Attribute.datetime(name, castList(items, Instant.class));
      };
    }
    throw invalid(
        "an inferred attribute must be a String, finite Number, Boolean, Instant or a non-empty"
            + " List of one of these; use the typed form otherwise");
  }

  private static AttributeType scalarType(Object v) {
    if (v instanceof String) {
      return AttributeType.STRING;
    }
    if (v instanceof Boolean) {
      return AttributeType.BOOL;
    }
    if (v instanceof Number) {
      return AttributeType.NUMERIC;
    }
    if (v instanceof Instant) {
      return AttributeType.DATETIME;
    }
    return null;
  }

  private static <T> List<T> castList(List<Object> items, Class<T> type) {
    List<T> out = new ArrayList<>(items.size());
    for (Object o : items) {
      out.add(type.cast(o));
    }
    return out;
  }

  static int compareCodePoints(String a, String b) {
    int i = 0;
    int j = 0;
    while (i < a.length() && j < b.length()) {
      int ca = a.codePointAt(i);
      int cb = b.codePointAt(j);
      if (ca != cb) {
        return Integer.compare(ca, cb);
      }
      i += Character.charCount(ca);
      j += Character.charCount(cb);
    }
    return Integer.compare(a.length() - i, b.length() - j);
  }

  // ------------------------------------------------------------ response

  /** Parses an HTTP 200 body; returns {@code null} when it is not a valid decision. */
  static Decision decodeDecision(String body, OptionalLong quota) {
    Object parsed;
    try {
      parsed = Json.parse(body);
    } catch (IllegalArgumentException e) {
      return null;
    }
    if (!(parsed instanceof Map<?, ?> m)) {
      return null;
    }
    if (!(m.get("allowed") instanceof Boolean allowed)) {
      return null;
    }
    if (!(m.get("reason") instanceof BigDecimal r)) {
      return null;
    }
    int code;
    try {
      code = r.intValueExact();
    } catch (ArithmeticException e) {
      return null;
    }
    Optional<String> errorMessage =
        m.get("errorMessage") instanceof String s ? Optional.of(s) : Optional.empty();
    return new Decision(allowed, Reason.fromCode(code), code, errorMessage, quota);
  }

  /** The {@code error} string of an error body, or {@code null}. */
  static String errorField(String body) {
    try {
      if (Json.parse(body) instanceof Map<?, ?> m && m.get("error") instanceof String s) {
        return s;
      }
    } catch (IllegalArgumentException e) {
      // not JSON
    }
    return null;
  }

  /** A non-negative integer header value, or empty. */
  static OptionalLong parseQuota(Optional<String> header) {
    if (header.isEmpty() || !isDigits(header.get().trim())) {
      return OptionalLong.empty();
    }
    try {
      return OptionalLong.of(Long.parseLong(header.get().trim()));
    } catch (NumberFormatException e) {
      return OptionalLong.empty();
    }
  }

  /** A positive integer number of seconds, or {@code null}. */
  static Duration parseRetryAfter(Optional<String> header) {
    if (header.isEmpty() || !isDigits(header.get().trim())) {
      return null;
    }
    try {
      long s = Long.parseLong(header.get().trim());
      return s > 0 ? Duration.ofSeconds(s) : null;
    } catch (NumberFormatException | ArithmeticException e) {
      return null;
    }
  }

  private static boolean isDigits(String s) {
    if (s.isEmpty() || s.length() > 18) {
      return false;
    }
    for (int i = 0; i < s.length(); i++) {
      if (s.charAt(i) < '0' || s.charAt(i) > '9') {
        return false;
      }
    }
    return true;
  }

  /** Shortens server text and strips control characters before it goes into a message. */
  static String sanitize(String s) {
    StringBuilder b = new StringBuilder(Math.min(s.length(), MAX_MESSAGE));
    for (int i = 0; i < s.length() && b.length() < MAX_MESSAGE; i++) {
      char c = s.charAt(i);
      b.append(Character.isISOControl(c) ? ' ' : c);
    }
    if (s.length() > MAX_MESSAGE) {
      b.append("...");
    }
    return b.toString();
  }

  static InvalidRequestError invalid(String message) {
    return new InvalidRequestError(ErrorDetails.of(message));
  }
}
