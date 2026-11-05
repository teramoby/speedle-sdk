/*
 * Copyright (c) 2026 The Speedle Authors
 * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 */
package io.speedle.sdk;

import static io.speedle.sdk.VectorCalls.integer;
import static io.speedle.sdk.VectorCalls.list;
import static io.speedle.sdk.VectorCalls.obj;
import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.junit.jupiter.api.Assertions.fail;

import java.io.IOException;
import java.math.BigDecimal;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.HashSet;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.regex.Pattern;
import java.util.stream.Stream;
import org.junit.jupiter.api.AfterAll;
import org.junit.jupiter.api.BeforeAll;
import org.junit.jupiter.api.DynamicTest;
import org.junit.jupiter.api.Tag;
import org.junit.jupiter.api.TestFactory;

/** Runs every vector in {@code conformance/vectors} (design §11). */
@Tag("conformance")
class ConformanceTest {

  static Path vectorDir() {
    return Path.of(System.getProperty("speedle.conformance.dir", "../conformance/vectors"));
  }

  private static SharedMock shared;

  @BeforeAll
  static void startMock() throws Exception {
    shared = SharedMock.start();
    if (shared == null) {
      if (Boolean.getBoolean("speedle.requireSharedMock")) {
        throw new IllegalStateException("conformance/mock is required but not available");
      }
      System.err.println(
          "WARNING: conformance/mock not found; running the vectors against the in-process mock");
    }
  }

  @AfterAll
  static void stopMock() throws Exception {
    if (shared != null) {
      shared.close();
    }
  }

  @TestFactory
  Stream<DynamicTest> vectors() throws IOException {
    List<Path> files;
    try (Stream<Path> s = Files.list(vectorDir())) {
      files = s.filter(p -> p.toString().endsWith(".json")).sorted().toList();
    }
    assertTrue(!files.isEmpty(), "no vectors found in " + vectorDir());
    return files.stream()
        .map(f -> DynamicTest.dynamicTest(f.getFileName().toString(), () -> run(f)));
  }

  private void run(Path file) throws Exception {
    String text = Files.readString(file);
    Map<String, Object> v = obj(Json.parse(text));
    assertEquals(
        file.getFileName().toString().replace(".json", ""), v.get("id"), "id matches file name");
    if (shared != null) {
      shared.load(text);
      runCalls(v, shared.endpoint(), shared.trust());
      assertNull(shared.verify(), "mock verify");
      return;
    }
    try (MockServer mock = new MockServer(TestTls.get().serverContext(false))) {
      for (Object x : list(v.get("exchanges"))) {
        mock.script(reply(obj(obj(x).get("response"))));
      }
      runCalls(v, mock.endpoint(), TestTls.get().serverTrust);
      checkRequests(list(v.get("exchanges")), mock.requests());
    }
  }

  private void runCalls(Map<String, Object> v, java.net.URI endpoint, java.security.KeyStore trust)
      throws Exception {
    TestClock clock = new TestClock();
    double jitter =
        v.containsKey("jitterFraction") ? ((BigDecimal) v.get("jitterFraction")).doubleValue() : 1;
    VectorCalls.Recorder recorder = new VectorCalls.Recorder();
    {
      SpeedleClient.Builder b =
          VectorCalls.configure(
                  SpeedleClient.builder(), (String) v.get("backend"), obj(v.get("config")))
              .endpoint(endpoint)
              .trustStore(trust)
              .hooks(recorder)
              .clock(clock)
              .random(() -> jitter);
      List<Object> steps =
          v.containsKey("steps")
              ? list(v.get("steps"))
              : List.of(Map.of("call", v.get("call"), "expect", v.get("expect")));
      try (SpeedleClient client = b.build()) {
        int n = 0;
        for (Object s : steps) {
          Map<String, Object> step = obj(s);
          n++;
          if (step.containsKey("advanceMs")) {
            clock.advanceMillis(integer(step.get("advanceMs")));
          }
          clock.takeSleepsMillis();
          Object result;
          try {
            result = VectorCalls.call(client, obj(step.get("call")));
          } catch (SpeedleException e) {
            result = e;
          }
          checkOutcome("step " + n, obj(step.get("expect")), result, clock.takeSleepsMillis());
        }
      }
    }
    if (v.containsKey("events")) {
      checkEvents(list(v.get("events")), recorder.events);
    }
  }

  private static MockServer.Reply reply(Map<String, Object> r) {
    if (r.containsKey("fault")) {
      return MockServer.Reply.fault((String) r.get("fault"));
    }
    Map<String, String> headers = new java.util.LinkedHashMap<>();
    Map<String, Object> h = obj(r.get("headers"));
    if (h != null) {
      h.forEach((k, val) -> headers.put(k, (String) val));
    }
    String body = null;
    if (r.containsKey("rawBody")) {
      body = (String) r.get("rawBody");
      headers.putIfAbsent("Content-Type", "text/plain");
    } else if (r.containsKey("body")) {
      body = Json.write(r.get("body"));
    }
    return new MockServer.Reply(integer(r.get("status")), headers, body, null);
  }

  private static void checkOutcome(
      String where, Map<String, Object> expect, Object result, List<Long> sleeps) {
    if (expect.containsKey("delaysMs")) {
      List<Long> want = new ArrayList<>();
      list(expect.get("delaysMs")).forEach(d -> want.add(((BigDecimal) d).longValueExact()));
      assertEquals(want, sleeps, where + ": delaysMs");
    }
    if (expect.containsKey("result")) {
      if (result instanceof SpeedleException e) {
        fail(where + ": expected a result, got " + e.getClass().getSimpleName(), e);
      }
      assertEquals(expect.get("result"), result, where + ": result");
    } else if (expect.containsKey("decision")) {
      if (!(result instanceof Decision d)) {
        fail(where + ": expected a decision, got " + describe(result), asThrowable(result));
        return;
      }
      Map<String, Object> want = obj(expect.get("decision"));
      assertEquals(want.get("allowed"), d.allowed(), where + ": allowed");
      assertEquals(integer(want.get("reason")), d.reasonCode(), where + ": reason");
      if (want.containsKey("errorMessage")) {
        Object m = want.get("errorMessage");
        assertEquals(JsonMatch.isNull(m) ? null : m, d.errorMessage().orElse(null), where);
      }
      if (want.containsKey("quotaDailyRemaining")) {
        Object q = want.get("quotaDailyRemaining");
        Long got = d.quotaDailyRemaining().isPresent() ? d.quotaDailyRemaining().getAsLong() : null;
        assertEquals(
            JsonMatch.isNull(q) ? null : ((BigDecimal) q).longValueExact(), got, where + ": quota");
      }
    } else {
      Map<String, Object> want = obj(expect.get("error"));
      if (!(result instanceof SpeedleException e)) {
        fail(where + ": expected " + want.get("type") + ", got " + describe(result));
        return;
      }
      assertEquals(want.get("type"), e.getClass().getSimpleName(), where + ": error type");
      checkField(where, want, "status", e.status().isPresent() ? e.status().getAsInt() : null);
      checkField(where, want, "code", e.code().orElse(null));
      Object retryAfter =
          e instanceof TooManyRequestsError t && t.retryAfter().isPresent()
              ? t.retryAfter().get().toSeconds()
              : null;
      checkField(where, want, "retryAfterSeconds", retryAfter);
      checkField(
          where,
          want,
          "quotaDailyRemaining",
          e.quotaDailyRemaining().isPresent() ? e.quotaDailyRemaining().getAsLong() : null);
    }
  }

  private static void checkField(String where, Map<String, Object> want, String key, Object got) {
    if (!want.containsKey(key)) {
      return;
    }
    Object w = want.get(key);
    if (JsonMatch.isNull(w)) {
      assertNull(got, where + ": " + key + " must be empty");
    } else if (w instanceof BigDecimal bd) {
      assertTrue(
          got instanceof Number n && new BigDecimal(n.toString()).compareTo(bd) == 0,
          where + ": " + key + " " + got + " != " + bd);
    } else {
      assertEquals(w, got, where + ": " + key);
    }
  }

  private static void checkRequests(List<Object> exchanges, List<MockServer.Request> got) {
    assertEquals(exchanges.size(), got.size(), "number of requests");
    for (int i = 0; i < exchanges.size(); i++) {
      Map<String, Object> want = obj(obj(exchanges.get(i)).get("request"));
      MockServer.Request r = got.get(i);
      String where = "request " + (i + 1);
      assertEquals(want.get("method"), r.method(), where + ": method");
      assertEquals(want.get("path"), r.path(), where + ": path");
      Map<String, Object> headers = obj(want.get("headers"));
      if (headers != null) {
        for (Map.Entry<String, Object> h : headers.entrySet()) {
          String actual = r.headers().get(h.getKey().toLowerCase(java.util.Locale.ROOT));
          Object m = h.getValue();
          if (m instanceof String exact) {
            assertEquals(exact, actual, where + ": header " + h.getKey());
          } else if (obj(m).containsKey("absent")) {
            assertNull(actual, where + ": header " + h.getKey() + " must be absent");
          } else {
            String p = (String) obj(m).get("pattern");
            assertTrue(
                actual != null && Pattern.compile(p).matcher(actual).matches(),
                where + ": header " + h.getKey() + " = " + actual);
          }
        }
      }
      Set<String> unordered = new HashSet<>();
      if (want.containsKey("unorderedArrays")) {
        list(want.get("unorderedArrays")).forEach(p -> unordered.add((String) p));
      }
      String d = JsonMatch.diff(want.get("body"), Json.parse(r.body()), "", unordered);
      assertNull(d, where + ": body " + r.body());
    }
  }

  private static void checkEvents(List<Object> want, List<Map<String, Object>> got) {
    assertEquals(want.size(), got.size(), "number of events; got " + got);
    for (int i = 0; i < want.size(); i++) {
      Map<String, Object> w = obj(want.get(i));
      Map<String, Object> g = got.get(i);
      for (Map.Entry<String, Object> f : w.entrySet()) {
        checkField("event " + (i + 1) + " " + g, w, f.getKey(), g.get(f.getKey()));
      }
    }
  }

  private static String describe(Object result) {
    return result instanceof Throwable t ? t.getClass().getSimpleName() : String.valueOf(result);
  }

  private static Throwable asThrowable(Object result) {
    return result instanceof Throwable t ? t : null;
  }
}
