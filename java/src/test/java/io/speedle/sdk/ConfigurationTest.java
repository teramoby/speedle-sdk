/*
 * Copyright (c) 2026 The Speedle Authors
 * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 */
package io.speedle.sdk;

import static org.junit.jupiter.api.Assertions.assertDoesNotThrow;
import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.net.URI;
import java.time.Duration;
import org.junit.jupiter.api.Test;

class ConfigurationTest {

  private static SpeedleClient.Builder selfHosted(String endpoint) {
    return SpeedleClient.builder()
        .backend(Backend.SPEEDLE)
        .endpoint(URI.create(endpoint))
        .auth(Auth.none())
        .serviceName("books");
  }

  private static SpeedleClient.Builder cloud(String endpoint) {
    return SpeedleClient.builder()
        .backend(Backend.SPEEDLE_CLOUD)
        .endpoint(URI.create(endpoint))
        .auth(Auth.bearer("spdl_token"))
        .serviceName("books");
  }

  @Test
  void requiresBackendEndpointAuthAndServiceName() {
    assertThrows(ConfigurationError.class, () -> cloud("https://x").backend(null).build());
    assertThrows(ConfigurationError.class, () -> cloud("https://x").endpoint(null).build());
    assertThrows(ConfigurationError.class, () -> cloud("https://x").auth(null).build());
    assertThrows(ConfigurationError.class, () -> cloud("https://x").serviceName(null).build());
    assertThrows(ConfigurationError.class, () -> cloud("https://x").serviceName("").build());
  }

  @Test
  void endpointRules() {
    assertDoesNotThrow(() -> cloud("https://cloud.speedle.io").build().close());
    assertThrows(ConfigurationError.class, () -> cloud("https://x/?a=1").build());
    assertThrows(ConfigurationError.class, () -> cloud("https://x/#f").build());
    assertThrows(ConfigurationError.class, () -> cloud("https://u:p@x/").build());
    assertThrows(ConfigurationError.class, () -> cloud("ftp://x").build());
    assertThrows(ConfigurationError.class, () -> cloud("x.example").build());
  }

  @Test
  void plaintextOnlyForSpeedleWithoutCredentials() {
    for (String ok :
        new String[] {"http://127.0.0.1:6734", "http://localhost:6734", "http://[::1]:6734"}) {
      assertDoesNotThrow(() -> selfHosted(ok).build().close(), ok);
    }
    assertThrows(ConfigurationError.class, () -> selfHosted("http://10.0.0.5:6734").build());
    assertThrows(ConfigurationError.class, () -> selfHosted("http://127.evil.example").build());
    assertDoesNotThrow(
        () -> selfHosted("http://10.0.0.5:6734").allowInsecureHttp(true).build().close());
    // A bearer token never goes over plaintext, not even with allowInsecureHttp.
    assertThrows(
        ConfigurationError.class,
        () ->
            selfHosted("http://127.0.0.1").auth(Auth.bearer("t")).allowInsecureHttp(true).build());
    assertThrows(ConfigurationError.class, () -> cloud("http://127.0.0.1").build());
    assertThrows(
        ConfigurationError.class,
        () ->
            selfHosted("http://127.0.0.1")
                .auth(Auth.mutualTls(TestTls.get().clientKeys, TestTls.PASSWORD))
                .build());
  }

  @Test
  void bearerTokenValidationAndRedaction() {
    assertThrows(ConfigurationError.class, () -> Auth.bearer(""));
    assertThrows(ConfigurationError.class, () -> Auth.bearer(null));
    assertThrows(ConfigurationError.class, () -> Auth.bearer("a b"));
    assertThrows(ConfigurationError.class, () -> Auth.bearer("a\tb"));
    assertThrows(ConfigurationError.class, () -> Auth.bearer("ab\n"));
    assertThrows(ConfigurationError.class, () -> Auth.bearer("a\u00a0b"));
    Auth a = Auth.bearer("future-format.token");
    assertFalse(a.toString().contains("future"));
    try (SpeedleClient c = cloud("https://x").build()) {
      assertFalse(c.toString().contains("spdl_token"));
    }
  }

  @Test
  void policyValidation() {
    assertThrows(
        ConfigurationError.class,
        () ->
            new RetryPolicy(
                0, Duration.ZERO, Duration.ZERO, Duration.ofSeconds(1), Duration.ofSeconds(1)));
    assertThrows(
        ConfigurationError.class,
        () ->
            new RetryPolicy(1, Duration.ZERO, Duration.ZERO, Duration.ZERO, Duration.ofSeconds(1)));
    assertThrows(
        ConfigurationError.class, () -> new CircuitBreakerPolicy(0, Duration.ofSeconds(1)));
    assertThrows(ConfigurationError.class, () -> new CircuitBreakerPolicy(1, Duration.ZERO));
    RetryPolicy d = RetryPolicy.defaults();
    assertEquals(3, d.maxAttempts());
    assertEquals(Duration.ofMillis(100), d.baseDelay());
    assertEquals(Duration.ofSeconds(2), d.maxDelay());
    assertEquals(Duration.ofSeconds(2), d.attemptTimeout());
    assertEquals(Duration.ofSeconds(5), d.deadline());
    assertEquals(1, RetryPolicy.disabled().maxAttempts());
    assertEquals(5, CircuitBreakerPolicy.defaults().failureThreshold());
    assertEquals(Duration.ofSeconds(30), CircuitBreakerPolicy.defaults().coolDown());
  }

  @Test
  void userAgent() {
    String ua = ClientConfig.userAgent("myapp/1.2");
    assertTrue(
        ua.matches(
            "speedle-sdk-java/[0-9]+\\.[0-9]+\\.[0-9]+.* \\(java/[^;]+; [a-z0-9]+\\) myapp/1\\.2"),
        ua);
    assertFalse(ClientConfig.VERSION.startsWith("0.0.0"), "version resource is filtered");
    assertThrows(
        ConfigurationError.class, () -> cloud("https://x").userAgentSuffix("bad\nsuffix").build());
  }

  @Test
  void loopbackDetection() {
    assertTrue(ClientConfig.isLoopback("127.0.0.1"));
    assertTrue(ClientConfig.isLoopback("127.1.2.3"));
    assertTrue(ClientConfig.isLoopback("LOCALHOST"));
    assertTrue(ClientConfig.isLoopback("[::1]"));
    assertFalse(ClientConfig.isLoopback("127.0.0.1.example"));
    assertFalse(ClientConfig.isLoopback("128.0.0.1"));
    assertFalse(ClientConfig.isLoopback("localhost.example"));
  }
}
