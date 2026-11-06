/*
 * Copyright (c) 2026 The Speedle Authors
 * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 */
package io.speedle.sdk;

import java.security.GeneralSecurityException;
import java.security.KeyStore;
import javax.net.ssl.KeyManager;
import javax.net.ssl.KeyManagerFactory;

/**
 * How the client authenticates. A closed set of built-in implementations; more may be added in
 * later versions without breaking this interface.
 */
public sealed interface Auth permits Auth.None, Auth.Bearer, Auth.MutualTls {

  /**
   * No credential: self-hosted Speedle on a trusted network or loopback.
   *
   * @return the auth
   */
  static Auth none() {
    return None.INSTANCE;
  }

  /**
   * {@code Authorization: Bearer <token>}: Speedle Cloud, or self-hosted Speedle behind an
   * authenticating gateway. Requires an {@code https://} endpoint. The token is a server-side
   * secret: never ship it to browsers, mobile apps or desktop clients.
   *
   * @param token the token; not empty, without whitespace or control characters
   * @return the auth
   * @throws ConfigurationError if the token is empty or contains whitespace or control characters
   */
  static Auth bearer(String token) {
    if (token == null || token.isEmpty()) {
      throw new ConfigurationError(ErrorDetails.of("bearer token must not be empty"));
    }
    for (int i = 0; i < token.length(); i++) {
      char c = token.charAt(i);
      if (c <= 0x20
          || Character.isWhitespace(c)
          || Character.isSpaceChar(c)
          || Character.isISOControl(c)) {
        throw new ConfigurationError(
            ErrorDetails.of("bearer token must not contain whitespace or control characters"));
      }
    }
    return new Bearer(token);
  }

  /**
   * Mutual TLS: presents the client certificate from the key store on the TLS handshake. Server
   * verification stays on.
   *
   * @param clientKeyStore a key store holding the client key and certificate chain
   * @param keyPassword the key password
   * @return the auth
   * @throws ConfigurationError if the key store cannot be used
   */
  static Auth mutualTls(KeyStore clientKeyStore, char[] keyPassword) {
    if (clientKeyStore == null) {
      throw new ConfigurationError(ErrorDetails.of("client key store must not be null"));
    }
    try {
      KeyManagerFactory kmf =
          KeyManagerFactory.getInstance(KeyManagerFactory.getDefaultAlgorithm());
      kmf.init(clientKeyStore, keyPassword);
      return new MutualTls(kmf.getKeyManagers());
    } catch (GeneralSecurityException e) {
      throw new ConfigurationError(ErrorDetails.of("client key store cannot be used", e));
    }
  }

  /** {@link Auth#none()}. */
  final class None implements Auth {
    static final None INSTANCE = new None();

    private None() {}

    @Override
    public String toString() {
      return "Auth.none()";
    }
  }

  /** {@link Auth#bearer(String)}. The token is never printed. */
  final class Bearer implements Auth {
    private final String token;

    private Bearer(String token) {
      this.token = token;
    }

    String headerValue() {
      return "Bearer " + token;
    }

    @Override
    public String toString() {
      return "Auth.bearer(<redacted>)";
    }
  }

  /** {@link Auth#mutualTls(KeyStore, char[])}. */
  final class MutualTls implements Auth {
    private final KeyManager[] keyManagers;

    private MutualTls(KeyManager[] keyManagers) {
      this.keyManagers = keyManagers.clone();
    }

    KeyManager[] keyManagers() {
      return keyManagers.clone();
    }

    @Override
    public String toString() {
      return "Auth.mutualTls(<redacted>)";
    }
  }
}
