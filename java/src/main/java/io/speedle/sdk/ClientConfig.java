/*
 * Copyright (c) 2026 The Speedle Authors
 * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 */
package io.speedle.sdk;

import java.io.IOException;
import java.io.InputStream;
import java.net.Socket;
import java.net.URI;
import java.security.GeneralSecurityException;
import java.security.KeyStore;
import java.security.cert.CertificateException;
import java.security.cert.X509Certificate;
import java.util.ArrayList;
import java.util.List;
import java.util.Locale;
import java.util.Properties;
import java.util.function.DoubleSupplier;
import javax.net.ssl.KeyManager;
import javax.net.ssl.SSLContext;
import javax.net.ssl.SSLEngine;
import javax.net.ssl.TrustManager;
import javax.net.ssl.TrustManagerFactory;
import javax.net.ssl.X509ExtendedTrustManager;
import javax.net.ssl.X509TrustManager;

/** A validated client configuration. */
record ClientConfig(
    Backend backend,
    URI target,
    Auth auth,
    String serviceName,
    RetryPolicy retry,
    FailureMode failureMode,
    CircuitBreakerPolicy circuitBreaker,
    SpeedleHooks hooks,
    SSLContext sslContext,
    String userAgent,
    Clock clock,
    DoubleSupplier random) {

  static final String VERSION = loadVersion();

  /** Mutable builder state. */
  static final class Draft {
    Backend backend;
    URI endpoint;
    Auth auth;
    String serviceName;
    RetryPolicy retry;
    FailureMode failureMode;
    CircuitBreakerPolicy circuitBreaker;
    SpeedleHooks hooks;
    KeyStore trustStore;
    boolean allowInsecureHttp;
    String userAgentSuffix;
    Clock clock;
    DoubleSupplier random;

    ClientConfig validate() {
      if (backend == null) {
        throw config("backend is required");
      }
      if (auth == null) {
        throw config("auth is required");
      }
      if (serviceName == null || serviceName.isEmpty()) {
        throw config("serviceName is required and must not be empty");
      }
      URI target = target();
      boolean https = target.getScheme().equals("https");
      if (!https) {
        if (backend != Backend.SPEEDLE_PLUS || !(auth instanceof Auth.None)) {
          throw config("http:// is allowed only for SPEEDLE_PLUS with Auth.none(); use https://");
        }
        if (!allowInsecureHttp && !isLoopback(target.getHost())) {
          throw config(
              "http:// is allowed only for loopback hosts unless allowInsecureHttp is set");
        }
      }
      if (userAgentSuffix != null) {
        for (int i = 0; i < userAgentSuffix.length(); i++) {
          char c = userAgentSuffix.charAt(i);
          if (c < 0x20 || c > 0x7e) {
            throw config("userAgentSuffix must be printable ASCII");
          }
        }
      }
      String ua = userAgent(userAgentSuffix);
      return new ClientConfig(
          backend,
          target,
          auth,
          serviceName,
          retry == null ? RetryPolicy.defaults() : retry,
          failureMode == null ? FailureMode.ERROR : failureMode,
          circuitBreaker,
          hooks,
          https ? sslContext() : null,
          ua,
          clock == null ? Clock.SYSTEM : clock,
          random == null ? SpeedleClient.defaultRandom() : random);
    }

    private URI target() {
      if (endpoint == null) {
        throw config("endpoint is required");
      }
      String scheme = endpoint.getScheme();
      if (scheme == null
          || !(scheme.equalsIgnoreCase("https") || scheme.equalsIgnoreCase("http"))) {
        throw config("endpoint must be an https:// URL");
      }
      if (endpoint.isOpaque() || endpoint.getHost() == null || endpoint.getHost().isEmpty()) {
        throw config("endpoint must have a host");
      }
      if (endpoint.getRawQuery() != null || endpoint.getRawFragment() != null) {
        throw config("endpoint must not have a query or fragment");
      }
      if (endpoint.getRawUserInfo() != null) {
        throw config("endpoint must not contain user info");
      }
      String path = endpoint.getRawPath() == null ? "" : endpoint.getRawPath();
      while (path.endsWith("/")) {
        path = path.substring(0, path.length() - 1);
      }
      String authority = endpoint.getRawAuthority();
      return URI.create(scheme.toLowerCase(Locale.ROOT) + "://" + authority + path + Wire.PATH);
    }

    private SSLContext sslContext() {
      try {
        KeyManager[] kms = auth instanceof Auth.MutualTls m ? m.keyManagers() : null;
        TrustManager[] tms = null;
        if (trustStore != null) {
          tms =
              new TrustManager[] {
                new CompositeTrustManager(trustManager(null), trustManager(trustStore))
              };
        }
        SSLContext ctx = SSLContext.getInstance("TLS");
        ctx.init(kms, tms, null);
        return ctx;
      } catch (GeneralSecurityException e) {
        throw new ConfigurationError(ErrorDetails.of("cannot set up TLS", e));
      }
    }
  }

  static boolean isLoopback(String host) {
    String h = host.toLowerCase(Locale.ROOT);
    if (h.startsWith("[") && h.endsWith("]")) {
      h = h.substring(1, h.length() - 1);
    }
    if (h.equals("localhost") || h.equals("::1") || h.equals("0:0:0:0:0:0:0:1")) {
      return true;
    }
    String[] parts = h.split("\\.", -1);
    if (parts.length != 4 || !parts[0].equals("127")) {
      return false;
    }
    for (String p : parts) {
      if (p.isEmpty()
          || p.length() > 3
          || !p.chars().allMatch(Character::isDigit)
          || Integer.parseInt(p) > 255) {
        return false;
      }
    }
    return true;
  }

  static String userAgent(String suffix) {
    String os = System.getProperty("os.name", "unknown").toLowerCase(Locale.ROOT);
    if (os.startsWith("mac")) {
      os = "darwin";
    } else if (os.startsWith("windows")) {
      os = "windows";
    } else if (os.startsWith("linux")) {
      os = "linux";
    } else {
      os = os.replaceAll("[^a-z0-9]", "");
    }
    String java = System.getProperty("java.version", "unknown").replaceAll("[^A-Za-z0-9._+-]", "");
    String ua = "speedle-sdk-java/" + VERSION + " (java/" + java + "; " + os + ")";
    return suffix == null || suffix.isEmpty() ? ua : ua + " " + suffix;
  }

  private static String loadVersion() {
    try (InputStream in = ClientConfig.class.getResourceAsStream("version.properties")) {
      if (in != null) {
        Properties p = new Properties();
        p.load(in);
        String v = p.getProperty("version");
        if (v != null && !v.isEmpty() && !v.startsWith("$")) {
          return v;
        }
      }
    } catch (IOException e) {
      // fall through
    }
    return "0.0.0";
  }

  private static ConfigurationError config(String message) {
    return new ConfigurationError(ErrorDetails.of(message));
  }

  private static X509ExtendedTrustManager trustManager(KeyStore ks)
      throws GeneralSecurityException {
    TrustManagerFactory tmf =
        TrustManagerFactory.getInstance(TrustManagerFactory.getDefaultAlgorithm());
    tmf.init(ks);
    for (TrustManager tm : tmf.getTrustManagers()) {
      if (tm instanceof X509ExtendedTrustManager x) {
        return x;
      }
    }
    throw new GeneralSecurityException("no X509 trust manager available");
  }

  /** Trusts a server when either the system roots or the extra trust store do. */
  private static final class CompositeTrustManager extends X509ExtendedTrustManager {
    private final List<X509ExtendedTrustManager> delegates;

    CompositeTrustManager(X509ExtendedTrustManager... delegates) {
      this.delegates = List.of(delegates);
    }

    private interface Check {
      void run(X509ExtendedTrustManager tm) throws CertificateException;
    }

    private void any(Check check) throws CertificateException {
      CertificateException last = null;
      for (X509ExtendedTrustManager tm : delegates) {
        try {
          check.run(tm);
          return;
        } catch (CertificateException e) {
          last = e;
        }
      }
      throw last != null ? last : new CertificateException("untrusted server certificate");
    }

    @Override
    public void checkServerTrusted(X509Certificate[] chain, String authType, SSLEngine engine)
        throws CertificateException {
      any(tm -> tm.checkServerTrusted(chain, authType, engine));
    }

    @Override
    public void checkServerTrusted(X509Certificate[] chain, String authType, Socket socket)
        throws CertificateException {
      any(tm -> tm.checkServerTrusted(chain, authType, socket));
    }

    @Override
    public void checkServerTrusted(X509Certificate[] chain, String authType)
        throws CertificateException {
      any(tm -> tm.checkServerTrusted(chain, authType));
    }

    @Override
    public void checkClientTrusted(X509Certificate[] chain, String authType, SSLEngine engine)
        throws CertificateException {
      throw new CertificateException("client certificates are not checked by this SDK");
    }

    @Override
    public void checkClientTrusted(X509Certificate[] chain, String authType, Socket socket)
        throws CertificateException {
      throw new CertificateException("client certificates are not checked by this SDK");
    }

    @Override
    public void checkClientTrusted(X509Certificate[] chain, String authType)
        throws CertificateException {
      throw new CertificateException("client certificates are not checked by this SDK");
    }

    @Override
    public X509Certificate[] getAcceptedIssuers() {
      List<X509Certificate> all = new ArrayList<>();
      for (X509TrustManager tm : delegates) {
        all.addAll(List.of(tm.getAcceptedIssuers()));
      }
      return all.toArray(new X509Certificate[0]);
    }
  }
}
