/*
 * Copyright (c) 2026 The Speedle Authors
 * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 */
package io.speedle.sdk;

import java.io.BufferedReader;
import java.io.ByteArrayInputStream;
import java.io.IOException;
import java.io.InputStreamReader;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.security.KeyStore;
import java.security.cert.CertificateFactory;
import java.time.Duration;
import java.util.List;
import java.util.Map;

/**
 * The shared conformance mock in {@code conformance/mock} (a Go program), driven through its
 * control API: {@code POST /load} a vector, run the calls, then {@code GET /verify}.
 */
final class SharedMock implements AutoCloseable {
  private final Process process;
  private final URI endpoint;
  private final URI control;
  private final KeyStore trust;
  private final HttpClient http =
      HttpClient.newBuilder().connectTimeout(Duration.ofSeconds(5)).build();

  private SharedMock(List<String> command, Path dir) throws Exception {
    ProcessBuilder pb = new ProcessBuilder(command).redirectError(ProcessBuilder.Redirect.INHERIT);
    if (dir != null) {
      pb.directory(dir.toFile());
    }
    process = pb.start();
    BufferedReader out =
        new BufferedReader(new InputStreamReader(process.getInputStream(), StandardCharsets.UTF_8));
    String line = out.readLine();
    if (line == null) {
      throw new IOException("the conformance mock exited before printing its addresses");
    }
    Map<String, Object> info = VectorCalls.obj(Json.parse(line));
    endpoint = URI.create((String) info.get("endpoint"));
    control = URI.create((String) info.get("control"));
    KeyStore ks = KeyStore.getInstance("PKCS12");
    ks.load(null, null);
    ks.setCertificateEntry(
        "speedle-mock-ca",
        CertificateFactory.getInstance("X.509")
            .generateCertificate(
                new ByteArrayInputStream(
                    ((String) info.get("caPem")).getBytes(StandardCharsets.US_ASCII))));
    trust = ks;
  }

  /**
   * Starts the mock: the binary named by {@code -Dspeedle.mock}, otherwise {@code go run} in {@code
   * conformance/mock}. Returns {@code null} when neither is available.
   */
  static SharedMock start() throws Exception {
    String bin = System.getProperty("speedle.mock", "");
    if (!bin.isEmpty()) {
      return new SharedMock(List.of(bin, "-exit-on-stdin-close"), null);
    }
    Path dir = Path.of("..", "conformance", "mock");
    if (Files.isRegularFile(dir.resolve("go.mod"))) {
      return new SharedMock(List.of("go", "run", ".", "-exit-on-stdin-close"), dir);
    }
    return null;
  }

  URI endpoint() {
    return endpoint;
  }

  KeyStore trust() {
    return trust;
  }

  void load(String vector) throws Exception {
    HttpResponse<String> r = post("/load", vector);
    if (r.statusCode() != 204) {
      throw new AssertionError("mock rejected the vector: " + r.statusCode() + " " + r.body());
    }
  }

  /** Returns {@code null} when every exchange was consumed and matched, else the report. */
  String verify() throws Exception {
    HttpResponse<String> r =
        http.send(
            HttpRequest.newBuilder(control.resolve("/verify")).GET().build(),
            HttpResponse.BodyHandlers.ofString());
    Map<String, Object> m = VectorCalls.obj(Json.parse(r.body()));
    return Boolean.TRUE.equals(m.get("ok")) ? null : r.body();
  }

  private HttpResponse<String> post(String path, String body) throws Exception {
    return http.send(
        HttpRequest.newBuilder(control.resolve(path))
            .header("Content-Type", "application/json")
            .POST(HttpRequest.BodyPublishers.ofString(body))
            .build(),
        HttpResponse.BodyHandlers.ofString());
  }

  @Override
  public void close() throws IOException {
    try {
      post("/shutdown", "");
    } catch (Exception e) {
      // already gone
    }
    process.getOutputStream().close();
    try {
      if (!process.waitFor(5, java.util.concurrent.TimeUnit.SECONDS)) {
        process.destroyForcibly();
      }
    } catch (InterruptedException e) {
      process.destroyForcibly();
      Thread.currentThread().interrupt();
    }
  }
}
