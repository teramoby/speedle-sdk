/*
 * Copyright (c) 2026 The Speedle Authors
 * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 */
package io.speedle.sdk;

import java.math.BigDecimal;
import java.time.Duration;
import java.time.Instant;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

/** Turns vector JSON into client settings and SDK calls. */
final class VectorCalls {
  private VectorCalls() {}

  @SuppressWarnings("unchecked")
  static Map<String, Object> obj(Object o) {
    return o instanceof Map<?, ?> ? (Map<String, Object>) o : null;
  }

  @SuppressWarnings("unchecked")
  static List<Object> list(Object o) {
    return (List<Object>) o;
  }

  static int integer(Object o) {
    return ((BigDecimal) o).intValueExact();
  }

  /** Applies {@code config} to the builder. */
  static SpeedleClient.Builder configure(
      SpeedleClient.Builder b, String backend, Map<String, Object> config) {
    b.backend(backend.equals("speedle-cloud") ? Backend.SPEEDLE_CLOUD : Backend.SPEEDLE);
    Map<String, Object> auth = obj(config.get("auth"));
    b.auth(
        "bearer".equals(auth.get("type")) ? Auth.bearer((String) auth.get("token")) : Auth.none());
    b.serviceName((String) config.get("serviceName"));
    Map<String, Object> retry = obj(config.get("retry"));
    if (retry != null) {
      RetryPolicy d = RetryPolicy.defaults();
      b.retry(
          new RetryPolicy(
              retry.containsKey("maxAttempts")
                  ? integer(retry.get("maxAttempts"))
                  : d.maxAttempts(),
              ms(retry, "baseDelayMs", d.baseDelay()),
              ms(retry, "maxDelayMs", d.maxDelay()),
              ms(retry, "attemptTimeoutMs", d.attemptTimeout()),
              ms(retry, "deadlineMs", d.deadline())));
    }
    if ("closed".equals(config.get("failureMode"))) {
      b.failureMode(FailureMode.CLOSED);
    }
    Map<String, Object> cb = obj(config.get("circuitBreaker"));
    if (cb != null) {
      CircuitBreakerPolicy d = CircuitBreakerPolicy.defaults();
      b.circuitBreaker(
          new CircuitBreakerPolicy(
              cb.containsKey("failureThreshold")
                  ? integer(cb.get("failureThreshold"))
                  : d.failureThreshold(),
              ms(cb, "coolDownMs", d.coolDown())));
    }
    return b;
  }

  private static Duration ms(Map<String, Object> m, String key, Duration dflt) {
    return m.containsKey(key) ? Duration.ofMillis(integer(m.get(key))) : dflt;
  }

  /** Makes the call; returns a Boolean, a Decision or throws the SDK error. */
  static Object call(SpeedleClient client, Map<String, Object> call) {
    Subject subject = subject(call.get("subject"));
    String action = (String) call.get("action");
    String resource = (String) call.get("resource");
    CallOptions opts =
        call.containsKey("serviceName")
            ? CallOptions.withServiceName((String) call.get("serviceName"))
            : null;
    boolean isAllowed = "isAllowed".equals(call.get("method"));
    if (call.containsKey("inferredAttributes")) {
      @SuppressWarnings("unchecked")
      Map<String, ?> attrs = (Map<String, ?>) JsonMatch.toNative(call.get("inferredAttributes"));
      return isAllowed
          ? (Object) client.isAllowed(subject, action, resource, attrs, opts)
          : client.check(subject, action, resource, attrs, opts);
    }
    List<Attribute> attrs = new ArrayList<>();
    if (call.containsKey("attributes")) {
      for (Object a : list(call.get("attributes"))) {
        attrs.add(attribute(obj(a)));
      }
    }
    return isAllowed
        ? (Object) client.isAllowed(subject, action, resource, attrs, opts)
        : client.check(subject, action, resource, attrs, opts);
  }

  private static Subject subject(Object s) {
    if (s instanceof String name) {
      return Subject.user(name);
    }
    List<Principal> ps = new ArrayList<>();
    for (Object o : list(s)) {
      Map<String, Object> p = obj(o);
      ps.add(new Principal((String) p.get("type"), (String) p.get("name"), (String) p.get("idd")));
    }
    return Subject.of(ps.toArray(new Principal[0]));
  }

  private static Attribute attribute(Map<String, Object> a) {
    String name = (String) a.get("name");
    Object v = a.get("value");
    boolean array = v instanceof List<?>;
    switch ((String) a.get("type")) {
      case "string":
        if (array) {
          List<String> l = new ArrayList<>();
          list(v).forEach(x -> l.add((String) x));
          return Attribute.string(name, l);
        }
        return Attribute.string(name, (String) v);
      case "numeric":
        if (array) {
          List<Double> l = new ArrayList<>();
          list(v).forEach(x -> l.add(((BigDecimal) x).doubleValue()));
          return Attribute.numeric(name, l);
        }
        return Attribute.numeric(name, ((BigDecimal) v).doubleValue());
      case "bool":
        if (array) {
          List<Boolean> l = new ArrayList<>();
          list(v).forEach(x -> l.add((Boolean) x));
          return Attribute.bool(name, l);
        }
        return Attribute.bool(name, (Boolean) v);
      case "datetime":
        if (array) {
          List<Instant> l = new ArrayList<>();
          list(v).forEach(x -> l.add(Instant.parse((String) x)));
          return Attribute.datetime(name, l);
        }
        return Attribute.datetime(name, Instant.parse((String) v));
      default:
        throw new IllegalArgumentException("unknown attribute type in vector");
    }
  }

  /** Hook recorder producing the vector's event shape. */
  static final class Recorder implements SpeedleHooks {
    final List<Map<String, Object>> events = new ArrayList<>();

    @Override
    public void onAttempt(AttemptEvent e) {
      Map<String, Object> m = event("attempt");
      m.put("attempt", e.attempt());
      m.put("status", e.status().isPresent() ? e.status().getAsInt() : null);
      m.put("errorType", e.errorType().orElse(null));
    }

    @Override
    public void onRetry(RetryEvent e) {
      Map<String, Object> m = event("retry");
      m.put("attempt", e.attempt());
      m.put("delayMs", e.delay().toMillis());
      m.put("errorType", e.errorType());
    }

    @Override
    public void onGiveUp(GiveUpEvent e) {
      Map<String, Object> m = event("giveUp");
      m.put("attempts", e.attempts());
      m.put("reason", e.reason().name().toLowerCase(java.util.Locale.ROOT));
      m.put("errorType", e.error().getClass().getSimpleName());
    }

    @Override
    public void onBreakerStateChange(BreakerStateChangeEvent e) {
      Map<String, Object> m = event("breaker");
      m.put("from", e.from().name().toLowerCase(java.util.Locale.ROOT));
      m.put("to", e.to().name().toLowerCase(java.util.Locale.ROOT));
    }

    private Map<String, Object> event(String type) {
      Map<String, Object> m = new LinkedHashMap<>();
      m.put("type", type);
      events.add(m);
      return m;
    }
  }
}
