/*
 * Copyright (c) 2026 The Speedle Authors
 * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 */
package io.speedle.sdk;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.math.BigDecimal;
import java.time.Duration;
import java.time.Instant;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.HashMap;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.OptionalLong;
import org.junit.jupiter.api.Test;

class WireTest {

  private static String body(Subject s, List<Attribute> attrs) {
    return Wire.encodeRequest(s, "books", "/books/1", "get", attrs);
  }

  @Test
  void encodesFieldsInDesignOrder() {
    String b =
        body(
            Subject.of(Principal.user("alice"), new Principal("group", "eng", "corp")),
            List.of(Attribute.string("team", "eng")));
    assertEquals(
        "{\"subject\":{\"principals\":[{\"type\":\"user\",\"name\":\"alice\"},"
            + "{\"type\":\"group\",\"name\":\"eng\",\"idd\":\"corp\"}]},"
            + "\"serviceName\":\"books\",\"resource\":\"/books/1\",\"action\":\"get\","
            + "\"attributes\":[{\"name\":\"team\",\"type\":\"string\",\"value\":\"eng\"}]}",
        b);
  }

  @Test
  void omitsAttributesWhenNone() {
    assertFalse(body(Subject.user("a"), List.of()).contains("attributes"));
    assertFalse(body(Subject.user("a"), null).contains("attributes"));
  }

  @Test
  void principalDefaultsToUserAndDropsEmptyIdd() {
    Principal p = new Principal(null, "bob", "");
    assertEquals("user", p.type());
    assertNull(p.idd());
    assertThrows(InvalidRequestError.class, () -> new Principal("user", "", null));
    assertThrows(InvalidRequestError.class, () -> Subject.of());
    assertThrows(InvalidRequestError.class, () -> Subject.of((Principal) null));
  }

  @Test
  void writesEveryTypeAndForm() {
    String b =
        body(
            Subject.user("a"),
            List.of(
                Attribute.numeric("n", 4),
                Attribute.numeric("f", 1.5),
                Attribute.numeric("ns", List.of(1, 2.25, 3L)),
                Attribute.bool("b", true),
                Attribute.bool("bs", List.of(false)),
                Attribute.datetime("t", Instant.parse("2026-11-05T03:00:00Z")),
                Attribute.datetime("tf", Instant.parse("2026-11-05T03:00:00.5Z")),
                Attribute.datetime("ts", List.of()),
                Attribute.string("s", "q\"\n\u0001")));
    assertTrue(b.contains("{\"name\":\"n\",\"type\":\"numeric\",\"value\":4}"), b);
    assertTrue(b.contains("\"value\":1.5}"), b);
    assertTrue(b.contains("\"value\":[1,2.25,3]}"), b);
    assertTrue(b.contains("{\"name\":\"b\",\"type\":\"bool\",\"value\":true}"), b);
    assertTrue(b.contains("\"value\":[false]}"), b);
    assertTrue(b.contains("\"value\":\"2026-11-05T03:00:00Z\"}"), b);
    assertTrue(b.contains("\"value\":\"2026-11-05T03:00:00.500Z\"}"), b);
    assertTrue(b.contains("{\"name\":\"ts\",\"type\":\"datetime\",\"value\":[]}"), b);
    assertTrue(b.contains("\"value\":\"q\\\"\\n\\u0001\"}"), b);
    assertTrue(Json.parse(b) instanceof Map<?, ?>, "valid JSON");
  }

  @Test
  void rejectsInvalidAttributesBeforeSending() {
    assertThrows(InvalidRequestError.class, () -> Attribute.string("", "x"));
    assertThrows(InvalidRequestError.class, () -> Attribute.string(null, "x"));
    assertThrows(InvalidRequestError.class, () -> Attribute.string("a", (String) null));
    assertThrows(InvalidRequestError.class, () -> Attribute.string("a", Arrays.asList("x", null)));
    assertThrows(InvalidRequestError.class, () -> Attribute.numeric("a", Double.NaN));
    assertThrows(
        InvalidRequestError.class,
        () -> Attribute.numeric("a", List.of(1.0, Double.POSITIVE_INFINITY)));
    assertThrows(InvalidRequestError.class, () -> Attribute.datetime("a", (Instant) null));
    List<Attribute> dup = List.of(Attribute.string("a", "x"), Attribute.bool("a", true));
    assertThrows(InvalidRequestError.class, () -> body(Subject.user("u"), dup));
    List<Attribute> withNull = new ArrayList<>();
    withNull.add(null);
    assertThrows(InvalidRequestError.class, () -> body(Subject.user("u"), withNull));
    assertThrows(
        InvalidRequestError.class,
        () -> body(Subject.user("u"), List.of(Attribute.datetime("t", Instant.MAX))));
    assertThrows(
        InvalidRequestError.class, () -> Wire.encodeRequest(null, "s", "r", "a", List.of()));
  }

  @Test
  void attributeToStringOmitsTheValue() {
    assertFalse(Attribute.string("team", "secret-value").toString().contains("secret"));
  }

  @Test
  void infersTypesAndSortsByCodePoint() {
    Map<String, Object> m = new HashMap<>();
    m.put("zeta", "2026-11-05T03:00:00Z"); // a string stays a string
    m.put("b", true);
    m.put("n", 4);
    m.put("big", new BigDecimal("1.25"));
    m.put("t", Instant.parse("2026-11-05T03:00:00Z"));
    m.put("list", List.of(1, 2.5));
    m.put("\uD83D\uDE00", "emoji"); // U+1F600 sorts after U+FFFD
    m.put("\uFFFD", "replacement");
    List<Attribute> attrs = Wire.infer(m);
    List<String> names = attrs.stream().map(Attribute::name).toList();
    assertEquals(List.of("b", "big", "list", "n", "t", "zeta", "\uFFFD", "\uD83D\uDE00"), names);
    assertEquals(AttributeType.STRING, attrs.get(5).type());
    assertEquals(AttributeType.BOOL, attrs.get(0).type());
    assertEquals(AttributeType.NUMERIC, attrs.get(1).type());
    assertEquals(AttributeType.NUMERIC, attrs.get(2).type());
    assertTrue(attrs.get(2).isArray());
    assertEquals(List.of(1.0, 2.5), attrs.get(2).value());
    assertEquals(AttributeType.DATETIME, attrs.get(4).type());
  }

  @Test
  void inferenceRejectsUnsupportedValues() {
    List<Object> bad =
        Arrays.asList(
            null,
            List.of(),
            List.of(1, "a"),
            Map.of("k", "v"),
            'c',
            Double.NaN,
            Arrays.asList("a", null),
            List.of(List.of("a")));
    for (Object v : bad) {
      Map<String, Object> m = new LinkedHashMap<>();
      m.put("a", v);
      assertThrows(InvalidRequestError.class, () -> Wire.infer(m), String.valueOf(v));
    }
    Map<String, Object> nullKey = new HashMap<>();
    nullKey.put(null, "x");
    assertThrows(InvalidRequestError.class, () -> Wire.infer(nullKey));
    assertEquals(List.of(), Wire.infer(null));
  }

  @Test
  void decodesDecisions() {
    Decision d =
        Wire.decodeDecision("{\"allowed\":true,\"reason\":0,\"extra\":[1]}", OptionalLong.of(7));
    assertTrue(d.allowed());
    assertEquals(Reason.GRANT_POLICY_FOUND, d.reason());
    assertEquals(7, d.quotaDailyRemaining().getAsLong());
    Decision e =
        Wire.decodeDecision(
            "{\"allowed\":false,\"reason\":4,\"errorMessage\":\"boom\"}", OptionalLong.empty());
    assertEquals(Reason.ERROR_IN_EVALUATION, e.reason());
    assertEquals(Optional.of("boom"), e.errorMessage());
    Decision u = Wire.decodeDecision("{\"allowed\":false,\"reason\":42}", OptionalLong.empty());
    assertEquals(Reason.UNKNOWN, u.reason());
    assertEquals(42, u.reasonCode());
    for (String bad :
        List.of(
            "",
            "null",
            "[]",
            "{\"allowed\":\"true\",\"reason\":0}",
            "{\"allowed\":1,\"reason\":0}",
            "{\"reason\":0}",
            "{\"allowed\":true}",
            "{\"allowed\":true,\"reason\":0.5}",
            "{\"allowed\":true,\"reason\":0} x",
            "<html>")) {
      assertNull(Wire.decodeDecision(bad, OptionalLong.empty()), bad);
    }
  }

  @Test
  void parsesHeaders() {
    assertEquals(Duration.ofSeconds(60), Wire.parseRetryAfter(Optional.of("60")));
    assertNull(Wire.parseRetryAfter(Optional.of("0")));
    assertNull(Wire.parseRetryAfter(Optional.of("-1")));
    assertNull(Wire.parseRetryAfter(Optional.of("1.5")));
    assertNull(Wire.parseRetryAfter(Optional.of("Wed, 21 Oct 2026 07:28:00 GMT")));
    assertNull(Wire.parseRetryAfter(Optional.empty()));
    assertEquals(OptionalLong.of(0), Wire.parseQuota(Optional.of("0")));
    assertEquals(OptionalLong.empty(), Wire.parseQuota(Optional.of("-3")));
    assertEquals(OptionalLong.empty(), Wire.parseQuota(Optional.of("abc")));
  }

  @Test
  void jsonRoundTrip() {
    String s = "{\"a\":[1,2.5,-3e2,true,false,null,\"x\\u00e9\\n\"],\"b\":{}}";
    assertEquals(
        "{\"a\":[1,2.5,-3E+2,true,false,null,\"x\u00e9\\n\"],\"b\":{}}", Json.write(Json.parse(s)));
    for (String bad : List.of("{", "[1,]", "{\"a\" 1}", "01", "\"\\x\"", "tru", "1 2")) {
      assertThrows(IllegalArgumentException.class, () -> Json.parse(bad), bad);
    }
  }
}
