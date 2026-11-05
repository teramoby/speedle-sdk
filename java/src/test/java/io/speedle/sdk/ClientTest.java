/*
 * Copyright (c) 2026 The Speedle Authors
 * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 */
package io.speedle.sdk;

import static io.speedle.sdk.MockServer.Reply.fault;
import static io.speedle.sdk.MockServer.Reply.json;
import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertInstanceOf;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.time.Duration;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.Test;

class ClientTest {
  private static final String ALLOW = "{\"allowed\":true,\"reason\":0}";
  private static final Subject ALICE = Subject.user("alice");

  private final TestClock clock = new TestClock();
  private final VectorCalls.Recorder recorder = new VectorCalls.Recorder();
  private MockServer mock;

  @AfterEach
  void stop() throws Exception {
    if (mock != null) {
      mock.close();
    }
  }

  private SpeedleClient.Builder client(Backend backend, MockServer.Reply... replies)
      throws Exception {
    mock = new MockServer(TestTls.get().serverContext(false)).script(replies);
    return SpeedleClient.builder()
        .backend(backend)
        .endpoint(mock.endpoint())
        .auth(backend == Backend.SPEEDLE_CLOUD ? Auth.bearer("spdl_t") : Auth.none())
        .serviceName("books")
        .trustStore(TestTls.get().serverTrust)
        .hooks(recorder)
        .clock(clock)
        .random(() -> 1.0);
  }

  private static <T extends SpeedleException> T fails(Class<T> type, SpeedleClient c) {
    return assertThrows(type, () -> c.isAllowed(ALICE, "get", "/r"));
  }

  @Test
  void plaintextLoopbackWithPathPrefixAndConnectionReuse() throws Exception {
    mock = new MockServer(null).script(json(200, ALLOW), json(200, ALLOW));
    try (SpeedleClient c =
        SpeedleClient.builder()
            .backend(Backend.SPEEDLE_PLUS)
            .endpoint(java.net.URI.create(mock.endpoint() + "/prefix/"))
            .auth(Auth.none())
            .serviceName("books")
            .userAgentSuffix("app/1")
            .build()) {
      assertTrue(c.isAllowed(ALICE, "get", "/r"));
      assertTrue(c.isAllowed(ALICE, "get", "/r"));
    }
    MockServer.Request r = mock.requests().get(0);
    assertEquals("/prefix/authz-check/v1/is-allowed", r.path());
    assertNull(r.headers().get("authorization"));
    assertTrue(r.headers().get("user-agent").startsWith("speedle-sdk-java/"));
    assertTrue(r.headers().get("user-agent").endsWith(" app/1"));
    assertEquals("application/json", r.headers().get("content-type"));
    assertEquals(1, mock.connections(), "keep-alive connection reused");
  }

  @Test
  void statusMapping() throws Exception {
    record Case(int status, String body, Class<? extends SpeedleException> type, int requests) {}
    List<Case> cases =
        List.of(
            new Case(404, "{\"error\":\"nf\"}", HttpError.class, 1),
            new Case(302, "", HttpError.class, 1),
            new Case(201, ALLOW, HttpError.class, 1),
            new Case(413, "{\"error\":\"request_too_large\"}", RequestTooLargeError.class, 1),
            new Case(415, "", HttpError.class, 1),
            new Case(500, "{\"error\":\"internal_error\"}", ServerError.class, 1),
            new Case(501, "", ServerError.class, 1),
            new Case(502, "<html>", ServerError.class, 3),
            new Case(504, "", ServerError.class, 3),
            new Case(503, "", ServiceUnavailableError.class, 3),
            new Case(429, "{\"error\":\"other\"}", TooManyRequestsError.class, 1),
            new Case(429, "nope", TooManyRequestsError.class, 1),
            new Case(200, "{\"allowed\":\"true\",\"reason\":0}", UnexpectedResponseError.class, 1),
            new Case(200, "not json", UnexpectedResponseError.class, 1));
    for (Case k : cases) {
      MockServer.Reply r =
          k.status() == 302
              ? json(302, "", Map.of("Location", "https://evil.example/"))
              : json(k.status(), k.body());
      try (SpeedleClient c = client(Backend.SPEEDLE_CLOUD, r, r, r).build()) {
        SpeedleException e = fails(k.type(), c);
        assertEquals(k.type(), e.getClass(), "status " + k.status());
        assertEquals(k.status(), e.status().getAsInt());
        assertEquals(k.requests(), mock.requests().size(), "requests for " + k.status());
        assertFalse(e.getMessage().contains("spdl_t"));
      }
      mock.close();
    }
  }

  @Test
  void speedlePlusErrorsAreMessagesAndGateway429IsBurst() throws Exception {
    try (SpeedleClient c =
        client(Backend.SPEEDLE_PLUS, json(400, "{\"error\":\"invalid_request\"}")).build()) {
      InvalidRequestError e = fails(InvalidRequestError.class, c);
      assertEquals(Optional.empty(), e.code());
      assertTrue(e.getMessage().contains("invalid_request"));
    }
    try (SpeedleClient c = client(Backend.SPEEDLE_PLUS, json(429, "slow down")).build()) {
      RateLimitedError e = fails(RateLimitedError.class, c);
      assertEquals(Optional.empty(), e.retryAfter());
      assertEquals(1, mock.requests().size(), "no Retry-After: not retried");
    }
    assertEquals("non_retryable", recorder.events.get(recorder.events.size() - 1).get("reason"));
  }

  @Test
  void decisionDetails() throws Exception {
    try (SpeedleClient c =
        client(
                Backend.SPEEDLE_PLUS,
                json(200, "{\"allowed\":false,\"reason\":4,\"errorMessage\":\"bad\",\"new\":1}"),
                json(200, "{\"allowed\":true,\"reason\":99}"))
            .build()) {
      Decision d = c.check(ALICE, "get", "/r", List.of());
      assertFalse(d.allowed());
      assertEquals(Reason.ERROR_IN_EVALUATION, d.reason());
      assertEquals(Optional.of("bad"), d.errorMessage());
      Decision u = c.check(ALICE, "get", "/r", Map.of("a", 1));
      assertEquals(Reason.UNKNOWN, u.reason());
      assertEquals(99, u.reasonCode());
    }
  }

  @Test
  void perCallServiceNameAndDeadline() throws Exception {
    try (SpeedleClient c =
        client(Backend.SPEEDLE_PLUS, json(503, ""), json(503, ""), json(200, ALLOW))
            .retry(
                new RetryPolicy(
                    3,
                    Duration.ofMillis(1500),
                    Duration.ofSeconds(10),
                    Duration.ofSeconds(2),
                    Duration.ofSeconds(5)))
            .build()) {
      CallOptions o = CallOptions.withServiceName("orders").deadline(Duration.ofMillis(4000));
      // Virtual delays of 1.5 s then 3 s fit the 5 s client deadline but not the 4 s per-call
      // one, so the call gives up after 2 attempts.
      assertThrows(
          ServiceUnavailableError.class, () -> c.isAllowed(ALICE, "get", "/r", List.of(), o));
      assertEquals(2, mock.requests().size());
      assertTrue(mock.requests().get(0).body().contains("\"serviceName\":\"orders\""));
      assertThrows(
          InvalidRequestError.class,
          () -> c.isAllowed(ALICE, "get", "/r", List.of(), CallOptions.withServiceName("")));
    }
  }

  @Test
  void failClosedNeverReturnsTrueAndCheckStillThrows() throws Exception {
    try (SpeedleClient c =
        client(Backend.SPEEDLE_CLOUD, json(401, "{\"error\":\"unauthorized\"}"), json(500, ""))
            .failureMode(FailureMode.CLOSED)
            .build()) {
      assertFalse(c.isAllowed(ALICE, "get", "/r"));
      assertThrows(ServerError.class, () -> c.check(ALICE, "get", "/r", List.of()));
      Map<String, Object> bad = new HashMap<>();
      bad.put("x", null);
      assertFalse(c.isAllowed(ALICE, "get", "/r", bad), "validation error becomes false");
      assertEquals(2, mock.requests().size());
      Map<String, Object> last = recorder.events.get(recorder.events.size() - 1);
      assertEquals("giveUp", last.get("type"));
      assertEquals(0, last.get("attempts"));
      assertEquals("InvalidRequestError", last.get("errorType"));
    }
  }

  @Test
  void throwingHooksAreIgnored() throws Exception {
    SpeedleHooks bad =
        new SpeedleHooks() {
          @Override
          public void onAttempt(AttemptEvent e) {
            throw new IllegalStateException("hook");
          }

          @Override
          public void onRetry(RetryEvent e) {
            throw new IllegalStateException("hook");
          }
        };
    try (SpeedleClient c =
        client(Backend.SPEEDLE_PLUS, json(503, ""), json(200, ALLOW)).hooks(bad).build()) {
      assertTrue(c.isAllowed(ALICE, "get", "/r"));
    }
  }

  @Test
  void attemptTimeoutIsRetriedAndDistinctFromDeadline() throws Exception {
    RetryPolicy p =
        new RetryPolicy(
            2, Duration.ZERO, Duration.ZERO, Duration.ofMillis(150), Duration.ofSeconds(5));
    try (SpeedleClient c =
        client(Backend.SPEEDLE_PLUS, fault("no_response"), fault("no_response")).retry(p).build()) {
      TimeoutError e = fails(TimeoutError.class, c);
      assertEquals(TimeoutError.class, e.getClass());
      assertEquals(2, mock.requests().size());
      assertEquals(
          "attempts_exhausted", recorder.events.get(recorder.events.size() - 1).get("reason"));
    }
  }

  @Test
  void interruptionCancelsWithoutGivingUp() throws Exception {
    try (SpeedleClient c =
        client(Backend.SPEEDLE_PLUS, fault("no_response"))
            .failureMode(FailureMode.CLOSED)
            .circuitBreaker(new CircuitBreakerPolicy(1, Duration.ofSeconds(30)))
            .build()) {
      Thread caller = Thread.currentThread();
      Thread t =
          new Thread(() -> {
            try {
              Thread.sleep(200);
            } catch (InterruptedException e) {
              return;
            }
            caller.interrupt();
          });
      t.start();
      assertThrows(CancelledError.class, () -> c.isAllowed(ALICE, "get", "/r"));
      assertTrue(Thread.interrupted(), "interrupt flag restored (and cleared here)");
      t.join();
      assertTrue(recorder.events.stream().noneMatch(e -> e.get("type").equals("giveUp")));
      assertTrue(
          recorder.events.stream().noneMatch(e -> e.get("type").equals("breaker")),
          "cancellation does not count for the breaker");
    }
  }

  @Test
  void interruptionDuringRetryDelay() throws Exception {
    RetryPolicy p =
        new RetryPolicy(
            2,
            Duration.ofSeconds(3),
            Duration.ofSeconds(3),
            Duration.ofSeconds(1),
            Duration.ofSeconds(10));
    try (SpeedleClient c =
        client(Backend.SPEEDLE_PLUS, json(503, "")).clock(Clock.SYSTEM).retry(p).build()) {
      Thread caller = Thread.currentThread();
      Thread t =
          new Thread(() -> {
            try {
              Thread.sleep(300);
            } catch (InterruptedException e) {
              return;
            }
            caller.interrupt();
          });
      t.start();
      long start = System.nanoTime();
      assertThrows(CancelledError.class, () -> c.isAllowed(ALICE, "get", "/r"));
      assertTrue(Thread.interrupted());
      assertTrue(System.nanoTime() - start < 2_000_000_000L, "the delay was cut short");
      t.join();
    }
  }

  @Test
  void tlsFailuresAreNotRetried() throws Exception {
    try (SpeedleClient c =
        client(Backend.SPEEDLE_CLOUD, json(200, ALLOW), json(200, ALLOW))
            .trustStore(null)
            .build()) {
      NetworkError e = fails(NetworkError.class, c);
      assertEquals(NetworkError.class, e.getClass());
      assertEquals(
          1, recorder.events.stream().filter(m -> m.get("type").equals("attempt")).count());
    }
  }

  @Test
  void mutualTls() throws Exception {
    TestTls tls = TestTls.get();
    mock = new MockServer(tls.serverContext(true), true).script(json(200, ALLOW));
    try (SpeedleClient c =
        SpeedleClient.builder()
            .backend(Backend.SPEEDLE_PLUS)
            .endpoint(mock.endpoint("localhost"))
            .auth(Auth.mutualTls(tls.clientKeys, TestTls.PASSWORD))
            .serviceName("books")
            .trustStore(tls.serverTrust)
            .build()) {
      assertTrue(c.isAllowed(ALICE, "get", "/r"));
    }
    assertNull(mock.requests().get(0).headers().get("authorization"));
    mock.close();
    mock = new MockServer(tls.serverContext(true), true).script(json(200, ALLOW));
    try (SpeedleClient c =
        SpeedleClient.builder()
            .backend(Backend.SPEEDLE_PLUS)
            .endpoint(mock.endpoint())
            .auth(Auth.none())
            .serviceName("books")
            .trustStore(tls.serverTrust)
            .build()) {
      SpeedleException e =
          assertThrows(SpeedleException.class, () -> c.isAllowed(ALICE, "g", "/r"));
      assertInstanceOf(NetworkError.class, e, "no client certificate: handshake fails");
    }
  }

  @Test
  void closedClientRejectsCalls() throws Exception {
    SpeedleClient c = client(Backend.SPEEDLE_PLUS).build();
    c.close();
    c.close();
    assertThrows(IllegalStateException.class, () -> c.isAllowed(ALICE, "get", "/r"));
  }
}
