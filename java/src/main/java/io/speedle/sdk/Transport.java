/*
 * Copyright (c) 2026 The Speedle Authors
 * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 */
package io.speedle.sdk;

import java.io.IOException;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.net.http.HttpTimeoutException;
import java.nio.charset.StandardCharsets;
import java.security.cert.CertificateException;
import java.time.Duration;
import java.util.OptionalLong;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.ExecutionException;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.TimeoutException;
import javax.net.ssl.SSLContext;
import javax.net.ssl.SSLHandshakeException;
import javax.net.ssl.SSLPeerUnverifiedException;

/** One attempt over a shared keep-alive {@link HttpClient}. Never follows redirects. */
final class Transport {

  /**
   * The result of one attempt: a decision, or an error with its retry classification.
   *
   * @param decision the decision of an HTTP 200, or {@code null}
   * @param error the attempt's error, or {@code null}
   * @param status the HTTP status, or 0
   * @param retryable whether the retry loop may try again
   * @param retryAfter a usable {@code Retry-After}, or {@code null}
   * @param deadlineCut whether the call's deadline ended the attempt
   */
  record Outcome(
      Decision decision,
      SpeedleException error,
      int status,
      boolean retryable,
      Duration retryAfter,
      boolean deadlineCut) {}

  private final HttpClient http;
  private final URI target;
  private final Backend backend;
  private final String authorization;
  private final String userAgent;

  Transport(URI target, Backend backend, Auth auth, SSLContext sslContext, String userAgent) {
    HttpClient.Builder b =
        HttpClient.newBuilder()
            .version(HttpClient.Version.HTTP_1_1)
            .followRedirects(HttpClient.Redirect.NEVER);
    if (sslContext != null) {
      b.sslContext(sslContext);
    }
    this.http = b.build();
    this.target = target;
    this.backend = backend;
    this.authorization = auth instanceof Auth.Bearer bearer ? bearer.headerValue() : null;
    this.userAgent = userAgent;
  }

  /**
   * Sends one request.
   *
   * @param body the JSON body
   * @param timeoutNanos the bound for this attempt
   * @param boundIsDeadline whether {@code timeoutNanos} is the remaining call deadline
   * @throws CancelledError if the calling thread is interrupted
   */
  Outcome send(String body, long timeoutNanos, boolean boundIsDeadline) {
    Duration timeout = Duration.ofNanos(Math.max(1, timeoutNanos));
    HttpRequest.Builder rb =
        HttpRequest.newBuilder(target)
            .timeout(timeout)
            .header("Content-Type", "application/json")
            .header("Accept", "application/json")
            .header("User-Agent", userAgent)
            .POST(HttpRequest.BodyPublishers.ofString(body, StandardCharsets.UTF_8));
    if (authorization != null) {
      rb.header("Authorization", authorization);
    }
    CompletableFuture<HttpResponse<byte[]>> f =
        http.sendAsync(rb.build(), HttpResponse.BodyHandlers.ofByteArray());
    HttpResponse<byte[]> resp;
    try {
      resp = f.get(timeout.toNanos(), TimeUnit.NANOSECONDS);
    } catch (InterruptedException e) {
      f.cancel(true);
      Thread.currentThread().interrupt();
      throw new CancelledError(ErrorDetails.of("the calling thread was interrupted", e));
    } catch (TimeoutException e) {
      f.cancel(true);
      return timedOut(boundIsDeadline, null);
    } catch (ExecutionException e) {
      return transportFailure(e.getCause(), boundIsDeadline);
    }
    return Responses.classify(
        backend,
        resp.statusCode(),
        new String(resp.body(), StandardCharsets.UTF_8),
        resp.headers().firstValue("X-Quota-Daily-Remaining"),
        resp.headers().firstValue("Retry-After"));
  }

  private static Outcome transportFailure(Throwable cause, boolean boundIsDeadline) {
    for (Throwable t = cause; t != null; t = t.getCause()) {
      if (t instanceof HttpTimeoutException) {
        return timedOut(boundIsDeadline, cause);
      }
      if (t instanceof SSLHandshakeException
          || t instanceof SSLPeerUnverifiedException
          || t instanceof CertificateException) {
        return failure(
            new NetworkError(ErrorDetails.of("TLS handshake or certificate failure", cause)),
            false);
      }
    }
    boolean io = cause instanceof IOException;
    String what = cause == null ? "unknown" : cause.getClass().getSimpleName();
    return failure(
        new NetworkError(ErrorDetails.of("transport failure (" + what + ")", cause)), io);
  }

  private static Outcome timedOut(boolean boundIsDeadline, Throwable cause) {
    if (boundIsDeadline) {
      return new Outcome(
          null,
          new DeadlineExceededError(ErrorDetails.of("the call deadline was exceeded", cause)),
          0,
          false,
          null,
          true);
    }
    return failure(new TimeoutError(ErrorDetails.of("the attempt timed out", cause)), true);
  }

  private static Outcome failure(SpeedleException e, boolean retryable) {
    return new Outcome(null, e, 0, retryable, null, false);
  }

  void close() {
    Object client = http;
    if (client instanceof AutoCloseable c) { // HttpClient is AutoCloseable from Java 21
      try {
        c.close();
      } catch (Exception e) {
        // nothing to report; the pool is released either way
      }
    }
  }

  /** Maps HTTP responses to decisions and typed errors (design §3.4). */
  static final class Responses {
    private Responses() {}

    static Outcome classify(
        Backend backend,
        int status,
        String body,
        java.util.Optional<String> quotaHeader,
        java.util.Optional<String> retryAfterHeader) {
      OptionalLong quota = Wire.parseQuota(quotaHeader);
      if (status == 200) {
        Decision d = Wire.decodeDecision(body, quota);
        if (d != null) {
          return new Outcome(d, null, 200, false, null, false);
        }
        return new Outcome(
            null,
            new UnexpectedResponseError(
                details("the response is not a valid decision", 200, null, null, quota, null)),
            200,
            false,
            null,
            false);
      }
      String error = Wire.errorField(body);
      String code = null;
      String message = null;
      if (error != null) {
        if (backend == Backend.SPEEDLE_CLOUD && Wire.CLOUD_CODES.contains(error)) {
          code = error;
        } else {
          message = Wire.sanitize(error);
        }
      }
      Duration retryAfter = Wire.parseRetryAfter(retryAfterHeader);
      ErrorDetails d = details(null, status, code, message, quota, retryAfter);
      SpeedleException e;
      boolean retryable = false;
      switch (status) {
        case 400 -> e = new InvalidRequestError(d);
        case 401 -> e = new UnauthorizedError(d);
        case 403 -> e = new ForbiddenError(d);
        case 413 -> e = new RequestTooLargeError(d);
        case 429 -> {
          if (backend == Backend.SPEEDLE || "rate_limited".equals(code)) {
            e = new RateLimitedError(d);
            retryable = retryAfter != null;
          } else if ("daily_quota_exceeded".equals(code)) {
            e = new QuotaExceededError(d);
          } else {
            e = new TooManyRequestsError(d);
          }
        }
        case 503 -> {
          e = new ServiceUnavailableError(d);
          retryable = true;
        }
        case 502, 504 -> {
          e = new ServerError(d);
          retryable = true;
        }
        default -> e = status >= 500 && status <= 599 ? new ServerError(d) : new HttpError(d);
      }
      return new Outcome(null, e, status, retryable, retryable ? retryAfter : null, false);
    }

    private static ErrorDetails details(
        String fixed,
        int status,
        String code,
        String serverMessage,
        OptionalLong quota,
        Duration retryAfter) {
      StringBuilder m = new StringBuilder("is-allowed failed with HTTP ").append(status);
      if (fixed != null) {
        m.append(": ").append(fixed);
      } else if (code != null) {
        m.append(": ").append(code);
      } else if (serverMessage != null) {
        m.append(": ").append(serverMessage);
      }
      return new ErrorDetails(m.toString(), status, code, quota, retryAfter, null);
    }
  }
}
