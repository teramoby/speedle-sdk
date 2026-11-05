/*
 * Copyright (c) 2026 The Speedle Authors
 * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 */
package io.speedle.sdk;

import java.io.IOException;
import java.io.InputStream;
import java.nio.file.Files;
import java.nio.file.Path;
import java.security.KeyStore;
import java.util.List;
import javax.net.ssl.KeyManagerFactory;
import javax.net.ssl.SSLContext;
import javax.net.ssl.TrustManagerFactory;

/** Throwaway key pairs created with the JDK's keytool, once per test JVM. */
final class TestTls {
  static final char[] PASSWORD = "changeit".toCharArray();

  private static TestTls instance;

  final KeyStore serverKeys;
  final KeyStore clientKeys;

  /** Trusts the server certificate only. */
  final KeyStore serverTrust;

  /** Trusts the client certificate only. */
  final KeyStore clientTrust;

  private TestTls() throws Exception {
    Path dir = Files.createTempDirectory("speedle-java-tls");
    serverKeys = generate(dir.resolve("server.p12"), "CN=localhost", "dns:localhost,ip:127.0.0.1");
    clientKeys = generate(dir.resolve("client.p12"), "CN=speedle-test-client", null);
    serverTrust = trustOnly(serverKeys);
    clientTrust = trustOnly(clientKeys);
  }

  static synchronized TestTls get() {
    if (instance == null) {
      try {
        instance = new TestTls();
      } catch (Exception e) {
        throw new IllegalStateException("cannot create test TLS material", e);
      }
    }
    return instance;
  }

  /**
   * Server context; with {@code requireClientCert} it needs a certificate from {@link #clientKeys}.
   */
  SSLContext serverContext(boolean requireClientCert) throws Exception {
    KeyManagerFactory kmf = KeyManagerFactory.getInstance(KeyManagerFactory.getDefaultAlgorithm());
    kmf.init(serverKeys, PASSWORD);
    TrustManagerFactory tmf =
        TrustManagerFactory.getInstance(TrustManagerFactory.getDefaultAlgorithm());
    tmf.init(clientTrust);
    SSLContext ctx = SSLContext.getInstance("TLS");
    ctx.init(kmf.getKeyManagers(), requireClientCert ? tmf.getTrustManagers() : null, null);
    return ctx;
  }

  private static KeyStore generate(Path file, String dname, String san) throws Exception {
    String keytool = Path.of(System.getProperty("java.home"), "bin", "keytool").toString();
    List<String> cmd =
        new java.util.ArrayList<>(
            List.of(
                keytool,
                "-genkeypair",
                "-alias",
                "key",
                "-keyalg",
                "EC",
                "-groupname",
                "secp256r1",
                "-dname",
                dname,
                "-validity",
                "2",
                "-storetype",
                "PKCS12",
                "-keystore",
                file.toString(),
                "-storepass",
                new String(PASSWORD),
                "-keypass",
                new String(PASSWORD)));
    if (san != null) {
      cmd.add("-ext");
      cmd.add("SAN=" + san);
    }
    Process p = new ProcessBuilder(cmd).redirectErrorStream(true).start();
    byte[] out = p.getInputStream().readAllBytes();
    if (p.waitFor() != 0) {
      throw new IOException("keytool failed: " + new String(out));
    }
    KeyStore ks = KeyStore.getInstance("PKCS12");
    try (InputStream in = Files.newInputStream(file)) {
      ks.load(in, PASSWORD);
    }
    return ks;
  }

  private static KeyStore trustOnly(KeyStore keys) throws Exception {
    KeyStore ks = KeyStore.getInstance("PKCS12");
    ks.load(null, null);
    ks.setCertificateEntry("trusted", keys.getCertificate("key"));
    return ks;
  }
}
