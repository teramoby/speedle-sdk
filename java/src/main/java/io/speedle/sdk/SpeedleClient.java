/*
 * Copyright (c) 2026 The Speedle Authors
 * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 */
package io.speedle.sdk;

import java.time.Duration;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.OptionalInt;
import java.util.concurrent.ThreadLocalRandom;
import java.util.function.DoubleSupplier;
import java.util.function.Supplier;

/**
 * A client for {@code POST /authz-check/v1/is-allowed} on Speedle Cloud or self-hosted Speedle.
 * Create one per endpoint and share it: it is thread-safe and owns one keep-alive connection pool.
 *
 * <p>Interrupting the calling thread aborts the attempt or the retry delay and throws {@link
 * CancelledError} with the interrupt flag restored.
 */
public final class SpeedleClient implements AutoCloseable {

  private final ClientConfig config;
  private final Transport transport;
  private final CircuitBreaker breaker;
  private volatile boolean closed;

  private SpeedleClient(ClientConfig config) {
    this.config = config;
    this.transport =
        new Transport(
            config.target(),
            config.backend(),
            config.auth(),
            config.sslContext(),
            config.userAgent());
    this.breaker =
        config.circuitBreaker() == null ? null : new CircuitBreaker(config.circuitBreaker());
  }

  /**
   * Starts a client configuration.
   *
   * @return a new builder
   */
  public static Builder builder() {
    return new Builder();
  }

  // ------------------------------------------------------------------ isAllowed

  /**
   * Whether the subject may perform the action on the resource.
   *
   * @param subject the subject
   * @param action the action
   * @param resource the resource
   * @return the server's {@code allowed} value; {@code false} on any SDK error in {@link
   *     FailureMode#CLOSED}
   * @throws SpeedleException on any failure in {@link FailureMode#ERROR}
   */
  public boolean isAllowed(Subject subject, String action, String resource) {
    return allowed(() -> List.of(), subject, action, resource, null);
  }

  /**
   * Whether the subject may perform the action on the resource, with typed attributes.
   *
   * @param subject the subject
   * @param action the action
   * @param resource the resource
   * @param attributes typed attributes, sent in this order; {@code null} means none
   * @return see {@link #isAllowed(Subject, String, String)}
   */
  public boolean isAllowed(
      Subject subject, String action, String resource, List<Attribute> attributes) {
    return allowed(() -> attributes, subject, action, resource, null);
  }

  /**
   * Whether the subject may perform the action on the resource, with typed attributes and per-call
   * options.
   *
   * @param subject the subject
   * @param action the action
   * @param resource the resource
   * @param attributes typed attributes, sent in this order; {@code null} means none
   * @param options per-call overrides; {@code null} means none
   * @return see {@link #isAllowed(Subject, String, String)}
   */
  public boolean isAllowed(
      Subject subject,
      String action,
      String resource,
      List<Attribute> attributes,
      CallOptions options) {
    return allowed(() -> attributes, subject, action, resource, options);
  }

  /**
   * Inferred form: a {@code String} becomes {@code string}, a finite {@code Number} {@code
   * numeric}, a {@code Boolean} {@code bool}, an {@code Instant} {@code datetime}, and a non-empty
   * {@code List} of one of these the array form. A string is always {@code string}, even if it
   * looks like a date. Attributes are sent sorted by name.
   *
   * @param subject the subject
   * @param action the action
   * @param resource the resource
   * @param attributes name to native value; {@code null} means none
   * @return see {@link #isAllowed(Subject, String, String)}
   */
  public boolean isAllowed(
      Subject subject, String action, String resource, Map<String, ?> attributes) {
    return allowed(() -> Wire.infer(attributes), subject, action, resource, null);
  }

  /**
   * Inferred form with per-call options; see {@link #isAllowed(Subject, String, String, Map)}.
   *
   * @param subject the subject
   * @param action the action
   * @param resource the resource
   * @param attributes name to native value; {@code null} means none
   * @param options per-call overrides; {@code null} means none
   * @return see {@link #isAllowed(Subject, String, String)}
   */
  public boolean isAllowed(
      Subject subject,
      String action,
      String resource,
      Map<String, ?> attributes,
      CallOptions options) {
    return allowed(() -> Wire.infer(attributes), subject, action, resource, options);
  }

  // ------------------------------------------------------------------ check

  /**
   * The detailed form. Always throws on failure, in every failure mode, because a {@link Decision}
   * must come from the server.
   *
   * @param subject the subject
   * @param action the action
   * @param resource the resource
   * @param attributes typed attributes, sent in this order; {@code null} means none
   * @return the server's decision
   * @throws SpeedleException on any failure
   */
  public Decision check(
      Subject subject, String action, String resource, List<Attribute> attributes) {
    return run(() -> attributes, subject, action, resource, null);
  }

  /**
   * The detailed form with per-call options.
   *
   * @param subject the subject
   * @param action the action
   * @param resource the resource
   * @param attributes typed attributes, sent in this order; {@code null} means none
   * @param options per-call overrides; {@code null} means none
   * @return the server's decision
   * @throws SpeedleException on any failure
   */
  public Decision check(
      Subject subject,
      String action,
      String resource,
      List<Attribute> attributes,
      CallOptions options) {
    return run(() -> attributes, subject, action, resource, options);
  }

  /**
   * The detailed form with inferred attributes; see {@link #isAllowed(Subject, String, String,
   * Map)}.
   *
   * @param subject the subject
   * @param action the action
   * @param resource the resource
   * @param attributes name to native value; {@code null} means none
   * @return the server's decision
   * @throws SpeedleException on any failure
   */
  public Decision check(
      Subject subject, String action, String resource, Map<String, ?> attributes) {
    return run(() -> Wire.infer(attributes), subject, action, resource, null);
  }

  /**
   * The detailed form with inferred attributes and per-call options.
   *
   * @param subject the subject
   * @param action the action
   * @param resource the resource
   * @param attributes name to native value; {@code null} means none
   * @param options per-call overrides; {@code null} means none
   * @return the server's decision
   * @throws SpeedleException on any failure
   */
  public Decision check(
      Subject subject,
      String action,
      String resource,
      Map<String, ?> attributes,
      CallOptions options) {
    return run(() -> Wire.infer(attributes), subject, action, resource, options);
  }

  /** Releases the connection pool (on Java 21 or later; earlier the pool is left to the GC). */
  @Override
  public void close() {
    if (!closed) {
      closed = true;
      transport.close();
    }
  }

  @Override
  public String toString() {
    return "SpeedleClient[" + config.backend() + ", " + config.target() + "]";
  }

  // ------------------------------------------------------------------ the call

  private boolean allowed(
      Supplier<List<Attribute>> attrs,
      Subject subject,
      String action,
      String resource,
      CallOptions options) {
    try {
      return run(attrs, subject, action, resource, options).allowed();
    } catch (CancelledError e) {
      throw e;
    } catch (SpeedleException e) {
      if (config.failureMode() == FailureMode.CLOSED) {
        return false;
      }
      throw e;
    }
  }

  private Decision run(
      Supplier<List<Attribute>> attrs,
      Subject subject,
      String action,
      String resource,
      CallOptions options) {
    if (closed) {
      throw new IllegalStateException("the client is closed");
    }
    CallOptions opts = options == null ? CallOptions.none() : options;
    String body;
    Duration deadline;
    try {
      String service = opts.serviceName().orElse(config.serviceName());
      if (service.isEmpty()) {
        throw Wire.invalid("serviceName must not be empty");
      }
      deadline = opts.deadline().orElse(config.retry().deadline());
      if (deadline.isNegative() || deadline.isZero()) {
        throw Wire.invalid("deadline must be positive");
      }
      body = Wire.encodeRequest(subject, service, resource, action, attrs.get());
    } catch (InvalidRequestError e) {
      giveUp(0, GiveUpReason.NON_RETRYABLE, e);
      throw e;
    }

    CircuitBreaker.Admission admission = null;
    if (breaker != null) {
      admission = breaker.acquire(config.clock().nanoTime());
      breakerChanged(admission.change());
      if (!admission.admitted()) {
        CircuitOpenError e =
            new CircuitOpenError(ErrorDetails.of("the circuit breaker is open; no request sent"));
        giveUp(0, GiveUpReason.CIRCUIT_OPEN, e);
        throw e;
      }
    }
    boolean trial = admission != null && admission.trial();
    boolean recorded = false;
    try {
      Attempts result = attempts(body, deadline);
      if (result.decision() != null) {
        recorded = true;
        recordBreaker(CircuitBreaker.Outcome.SUCCESS, trial);
        return result.decision();
      }
      giveUp(result.attempts(), result.reason(), result.error());
      recorded = true;
      recordBreaker(breakerOutcome(result.error()), trial);
      throw result.error();
    } finally {
      if (!recorded) { // cancellation or an unexpected exception
        recordBreaker(CircuitBreaker.Outcome.NEUTRAL, trial);
      }
    }
  }

  private record Attempts(
      Decision decision, int attempts, GiveUpReason reason, SpeedleException error) {
    static Attempts fail(int attempts, GiveUpReason reason, SpeedleException error) {
      return new Attempts(null, attempts, reason, error);
    }
  }

  /** The retry loop of design §6.3. */
  private Attempts attempts(String body, Duration deadline) {
    Clock clock = config.clock();
    RetryPolicy retry = config.retry();
    long attemptTimeout = retry.attemptTimeout().toNanos();
    long deadlineAt = clock.nanoTime() + deadline.toNanos();
    for (int attempt = 1; ; attempt++) {
      long remaining = deadlineAt - clock.nanoTime();
      if (remaining <= 0) {
        return Attempts.fail(
            attempt - 1,
            GiveUpReason.DEADLINE,
            new DeadlineExceededError(ErrorDetails.of("the call deadline was exceeded")));
      }
      boolean boundIsDeadline = remaining <= attemptTimeout;
      long started = clock.nanoTime();
      Transport.Outcome o =
          transport.send(body, Math.min(attemptTimeout, remaining), boundIsDeadline);
      attempted(attempt, clock.nanoTime() - started, o);
      if (o.decision() != null) {
        return new Attempts(o.decision(), attempt, null, null);
      }
      if (o.deadlineCut()) {
        return Attempts.fail(attempt, GiveUpReason.DEADLINE, o.error());
      }
      if (!o.retryable()) {
        return Attempts.fail(attempt, GiveUpReason.NON_RETRYABLE, o.error());
      }
      if (attempt >= retry.maxAttempts()) {
        return Attempts.fail(attempt, GiveUpReason.ATTEMPTS_EXHAUSTED, o.error());
      }
      long delay = backoff(retry, attempt);
      if (o.retryAfter() != null) {
        delay = Math.max(delay, o.retryAfter().toNanos());
      }
      if (delay >= deadlineAt - clock.nanoTime()) {
        return Attempts.fail(
            attempt,
            o.retryAfter() != null
                ? GiveUpReason.RETRY_AFTER_EXCEEDS_DEADLINE
                : GiveUpReason.DEADLINE,
            o.error());
      }
      SpeedleException err = o.error();
      Duration d = Duration.ofNanos(delay);
      int next = attempt + 1;
      hook(h -> h.onRetry(new RetryEvent(next, d, errorType(err))));
      try {
        clock.sleep(delay);
      } catch (InterruptedException e) {
        Thread.currentThread().interrupt();
        throw new CancelledError(ErrorDetails.of("the calling thread was interrupted", e));
      }
    }
  }

  /** Full jitter: {@code random(0, min(maxDelay, baseDelay * 2^(n-1)))}. */
  private long backoff(RetryPolicy retry, int failedAttempt) {
    long base = retry.baseDelay().toNanos();
    long cap = retry.maxDelay().toNanos();
    int shift = Math.min(failedAttempt - 1, 62);
    long exp = base > (Long.MAX_VALUE >> shift) ? Long.MAX_VALUE : base << shift;
    long bound = Math.min(cap, exp);
    double fraction = config.random().getAsDouble();
    return (long) (Math.max(0.0, Math.min(1.0, fraction)) * bound);
  }

  private static CircuitBreaker.Outcome breakerOutcome(SpeedleException e) {
    if (e instanceof NetworkError) {
      return CircuitBreaker.Outcome.FAILURE;
    }
    int status = e.status().orElse(0);
    if (status >= 500) {
      return CircuitBreaker.Outcome.FAILURE;
    }
    if (status == 200 || (status >= 400 && status < 500)) {
      return CircuitBreaker.Outcome.SUCCESS;
    }
    return CircuitBreaker.Outcome.NEUTRAL;
  }

  private void recordBreaker(CircuitBreaker.Outcome outcome, boolean trial) {
    if (breaker != null) {
      breakerChanged(breaker.record(outcome, trial, config.clock().nanoTime()));
    }
  }

  // ------------------------------------------------------------------ hooks

  private void attempted(int attempt, long nanos, Transport.Outcome o) {
    OptionalInt status = o.status() == 0 ? OptionalInt.empty() : OptionalInt.of(o.status());
    Optional<String> type =
        o.error() == null ? Optional.empty() : Optional.of(errorType(o.error()));
    AttemptEvent e = new AttemptEvent(attempt, Duration.ofNanos(nanos), status, type);
    hook(h -> h.onAttempt(e));
  }

  private void giveUp(int attempts, GiveUpReason reason, SpeedleException error) {
    GiveUpEvent e = new GiveUpEvent(attempts, reason, error);
    hook(h -> h.onGiveUp(e));
  }

  private void breakerChanged(BreakerStateChangeEvent e) {
    if (e != null) {
      hook(h -> h.onBreakerStateChange(e));
    }
  }

  private void hook(java.util.function.Consumer<SpeedleHooks> call) {
    SpeedleHooks hooks = config.hooks();
    if (hooks == null) {
      return;
    }
    try {
      call.accept(hooks);
    } catch (RuntimeException | LinkageError | AssertionError ignored) {
      // A hook never changes the call's result.
    }
  }

  private static String errorType(SpeedleException e) {
    return e.getClass().getSimpleName();
  }

  // ------------------------------------------------------------------ builder

  /**
   * Configures a {@link SpeedleClient}. {@code backend}, {@code endpoint}, {@code auth} and {@code
   * serviceName} are required.
   */
  public static final class Builder {
    private final ClientConfig.Draft draft = new ClientConfig.Draft();

    private Builder() {}

    /**
     * The backend; required.
     *
     * @param backend the backend
     * @return this builder
     */
    public Builder backend(Backend backend) {
      draft.backend = backend;
      return this;
    }

    /**
     * The base URL (scheme, host, optional port and path prefix); required. {@code https://} is
     * required, except for {@link Backend#SPEEDLE_PLUS} with {@link Auth#none()} on a loopback
     * host, or anywhere with {@link #allowInsecureHttp(boolean)}.
     *
     * @param endpoint the base URL, without query or fragment
     * @return this builder
     */
    public Builder endpoint(java.net.URI endpoint) {
      draft.endpoint = endpoint;
      return this;
    }

    /**
     * The authentication; required.
     *
     * @param auth the auth
     * @return this builder
     */
    public Builder auth(Auth auth) {
      draft.auth = auth;
      return this;
    }

    /**
     * The service name sent on every call; required and not empty. On Speedle Cloud it must be the
     * token's service.
     *
     * @param serviceName the service name
     * @return this builder
     */
    public Builder serviceName(String serviceName) {
      draft.serviceName = serviceName;
      return this;
    }

    /**
     * Retry, timeout and deadline settings; default {@link RetryPolicy#defaults()}.
     *
     * @param retry the policy
     * @return this builder
     */
    public Builder retry(RetryPolicy retry) {
      draft.retry = retry;
      return this;
    }

    /**
     * The failure mode; default {@link FailureMode#ERROR}.
     *
     * @param mode the mode
     * @return this builder
     */
    public Builder failureMode(FailureMode mode) {
      draft.failureMode = mode;
      return this;
    }

    /**
     * Turns the circuit breaker on; default off.
     *
     * @param policy the breaker settings, or {@code null} for none
     * @return this builder
     */
    public Builder circuitBreaker(CircuitBreakerPolicy policy) {
      draft.circuitBreaker = policy;
      return this;
    }

    /**
     * Observability hooks; default none.
     *
     * @param hooks the hooks, or {@code null}
     * @return this builder
     */
    public Builder hooks(SpeedleHooks hooks) {
      draft.hooks = hooks;
      return this;
    }

    /**
     * Extra trust anchors for self-hosted servers, used in addition to the system roots.
     * Certificate verification cannot be disabled.
     *
     * @param trustStore a key store with trusted certificates
     * @return this builder
     */
    public Builder trustStore(java.security.KeyStore trustStore) {
      draft.trustStore = trustStore;
      return this;
    }

    /**
     * Allows {@code http://} to non-loopback hosts for {@link Backend#SPEEDLE_PLUS} with {@link
     * Auth#none()}. Default false. Never allows a bearer token over plaintext.
     *
     * @param allow whether to allow it
     * @return this builder
     */
    public Builder allowInsecureHttp(boolean allow) {
      draft.allowInsecureHttp = allow;
      return this;
    }

    /**
     * Appended to the {@code User-Agent} after a space.
     *
     * @param suffix printable ASCII, or {@code null}
     * @return this builder
     */
    public Builder userAgentSuffix(String suffix) {
      draft.userAgentSuffix = suffix;
      return this;
    }

    Builder clock(Clock clock) {
      draft.clock = clock;
      return this;
    }

    Builder random(DoubleSupplier random) {
      draft.random = random;
      return this;
    }

    /**
     * Validates the configuration and creates the client.
     *
     * @return the client
     * @throws ConfigurationError if the configuration is invalid
     */
    public SpeedleClient build() {
      return new SpeedleClient(draft.validate());
    }
  }

  static DoubleSupplier defaultRandom() {
    return () -> ThreadLocalRandom.current().nextDouble();
  }
}
