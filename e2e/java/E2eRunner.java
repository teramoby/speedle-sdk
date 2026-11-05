/*
 * Copyright (c) 2026 The Speedle Authors
 * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 */

import io.speedle.sdk.Attribute;
import io.speedle.sdk.Auth;
import io.speedle.sdk.Backend;
import io.speedle.sdk.Decision;
import io.speedle.sdk.Principal;
import io.speedle.sdk.SpeedleClient;
import io.speedle.sdk.SpeedleException;
import io.speedle.sdk.Subject;
import java.io.IOException;
import java.net.URI;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Instant;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

/**
 * Speedle SDK end-to-end runner for Java. Runs every vector in e2e/vectors.json with {@code
 * SpeedleClient.check} and fails on any mismatch. The contract is in e2e/README.md.
 */
public final class E2eRunner {

  private E2eRunner() {}

  public static void main(String[] args) throws IOException {
    String endpoint = env("SPEEDLE_E2E_ENDPOINT", true);
    String service = env("SPEEDLE_E2E_SERVICE", true);
    String backendName = env("SPEEDLE_E2E_BACKEND", false);
    String token = env("SPEEDLE_E2E_TOKEN", false);
    Backend backend;
    if (backendName == null || backendName.equals("speedle-cloud")) {
      backend = Backend.SPEEDLE_CLOUD;
    } else if (backendName.equals("speedle-plus")) {
      backend = Backend.SPEEDLE_PLUS;
    } else {
      throw usage("SPEEDLE_E2E_BACKEND must be speedle-cloud or speedle-plus");
    }
    if (backend == Backend.SPEEDLE_CLOUD && token == null) {
      throw usage("SPEEDLE_E2E_TOKEN is required for speedle-cloud");
    }
    if (args.length != 1) {
      throw usage("usage: E2eRunner VECTORS_JSON");
    }

    Map<?, ?> file = (Map<?, ?>) new JsonReader(Files.readString(Path.of(args[0]))).read();
    List<?> vectors = (List<?>) file.get("vectors");
    int failed = 0;
    SpeedleClient built;
    try {
      built =
          SpeedleClient.builder()
              .backend(backend)
              .endpoint(URI.create(endpoint))
              .auth(token == null ? Auth.none() : Auth.bearer(token))
              .serviceName(service)
              .userAgentSuffix("speedle-sdk-e2e")
              .build();
    } catch (SpeedleException | IllegalArgumentException e) {
      throw usage(e.getClass().getSimpleName() + ": " + e.getMessage());
    }
    try (SpeedleClient client = built) {
      for (Object item : vectors) {
        Map<?, ?> vector = (Map<?, ?>) item;
        String name = (String) vector.get("name");
        String want = expected((Map<?, ?>) vector.get("expect"));
        String got;
        try {
          got = run(client, (Map<?, ?>) vector.get("request"));
        } catch (SpeedleException e) {
          got = e.getClass().getSimpleName() + ": " + e.getMessage();
        }
        if (want.equals(got)) {
          System.out.println("PASS " + name);
        } else {
          failed++;
          System.out.println("FAIL " + name + ": want " + want + ", got " + got);
        }
      }
    }
    System.out.printf(
        "java: %d passed, %d failed against %s%n", vectors.size() - failed, failed, endpoint);
    System.exit(failed == 0 && !vectors.isEmpty() ? 0 : 1);
  }

  private static String run(SpeedleClient client, Map<?, ?> request) {
    List<Principal> principals = new ArrayList<>();
    for (Object p : (List<?>) ((Map<?, ?>) request.get("subject")).get("principals")) {
      Map<?, ?> principal = (Map<?, ?>) p;
      principals.add(
          new Principal(
              (String) principal.get("type"),
              (String) principal.get("name"),
              (String) principal.get("idd")));
    }
    List<Attribute> attributes = new ArrayList<>();
    Object raw = request.get("attributes");
    if (raw != null) {
      for (Object a : (List<?>) raw) {
        attributes.add(attribute((Map<?, ?>) a));
      }
    }
    Decision d =
        client.check(
            Subject.of(principals.toArray(Principal[]::new)),
            (String) request.get("action"),
            (String) request.get("resource"),
            attributes);
    return format(d.allowed(), d.reasonCode(), d.errorMessage().orElse(null));
  }

  private static Attribute attribute(Map<?, ?> a) {
    String name = (String) a.get("name");
    Object value = a.get("value");
    boolean array = value instanceof List<?>;
    List<?> items = array ? (List<?>) value : List.of(value);
    switch ((String) a.get("type")) {
      case "string" -> {
        List<String> v = items.stream().map(String.class::cast).toList();
        return array ? Attribute.string(name, v) : Attribute.string(name, v.get(0));
      }
      case "numeric" -> {
        List<Double> v = items.stream().map(Double.class::cast).toList();
        return array ? Attribute.numeric(name, v) : Attribute.numeric(name, v.get(0));
      }
      case "bool" -> {
        List<Boolean> v = items.stream().map(Boolean.class::cast).toList();
        return array ? Attribute.bool(name, v) : Attribute.bool(name, v.get(0));
      }
      case "datetime" -> {
        List<Instant> v = items.stream().map(s -> Instant.parse((String) s)).toList();
        return array ? Attribute.datetime(name, v) : Attribute.datetime(name, v.get(0));
      }
      default -> throw new IllegalArgumentException("unknown attribute type " + a.get("type"));
    }
  }

  private static String expected(Map<?, ?> expect) {
    if (expect == null) {
      return "no expectation";
    }
    return format(
        (Boolean) expect.get("allowed"),
        ((Double) expect.get("reason")).intValue(),
        (String) expect.get("errorMessage"));
  }

  private static String format(boolean allowed, int reason, String errorMessage) {
    String s = "{allowed: " + allowed + ", reason: " + reason;
    return (errorMessage == null ? s : s + ", errorMessage: " + JsonReader.quote(errorMessage))
        + "}";
  }

  private static String env(String name, boolean required) {
    String value = System.getenv(name);
    if (value == null || value.isEmpty()) {
      if (required) {
        throw usage(name + " is required");
      }
      return null;
    }
    return value;
  }

  private static RuntimeException usage(String message) {
    System.err.println("java e2e: " + message);
    System.exit(2);
    return new IllegalStateException(message);
  }

  /** A small JSON reader for the vector file: objects, arrays, strings, doubles, booleans. */
  static final class JsonReader {
    private final String s;
    private int i;

    JsonReader(String s) {
      this.s = s;
    }

    Object read() {
      Object v = value();
      ws();
      if (i != s.length()) {
        throw error("trailing data");
      }
      return v;
    }

    private Object value() {
      ws();
      if (i >= s.length()) {
        throw error("unexpected end");
      }
      char c = s.charAt(i);
      switch (c) {
        case '{' -> {
          Map<String, Object> m = new LinkedHashMap<>();
          i++;
          ws();
          if (peek('}')) {
            return m;
          }
          do {
            ws();
            String key = string();
            ws();
            expect(':');
            m.put(key, value());
            ws();
          } while (peek(','));
          expect('}');
          return m;
        }
        case '[' -> {
          List<Object> l = new ArrayList<>();
          i++;
          ws();
          if (peek(']')) {
            return l;
          }
          do {
            l.add(value());
            ws();
          } while (peek(','));
          expect(']');
          return l;
        }
        case '"' -> {
          return string();
        }
        default -> {
          if (s.startsWith("true", i)) {
            i += 4;
            return Boolean.TRUE;
          }
          if (s.startsWith("false", i)) {
            i += 5;
            return Boolean.FALSE;
          }
          if (s.startsWith("null", i)) {
            i += 4;
            return null;
          }
          int start = i;
          while (i < s.length() && "+-0123456789.eE".indexOf(s.charAt(i)) >= 0) {
            i++;
          }
          if (start == i) {
            throw error("unexpected character");
          }
          return Double.valueOf(s.substring(start, i));
        }
      }
    }

    private String string() {
      expect('"');
      StringBuilder b = new StringBuilder();
      while (true) {
        if (i >= s.length()) {
          throw error("unterminated string");
        }
        char c = s.charAt(i++);
        if (c == '"') {
          return b.toString();
        }
        if (c != '\\') {
          b.append(c);
          continue;
        }
        char e = s.charAt(i++);
        switch (e) {
          case 'b' -> b.append('\b');
          case 'f' -> b.append('\f');
          case 'n' -> b.append('\n');
          case 'r' -> b.append('\r');
          case 't' -> b.append('\t');
          case 'u' -> {
            b.append((char) Integer.parseInt(s.substring(i, i + 4), 16));
            i += 4;
          }
          default -> b.append(e);
        }
      }
    }

    static String quote(String v) {
      StringBuilder b = new StringBuilder("\"");
      for (char c : v.toCharArray()) {
        if (c == '"' || c == '\\') {
          b.append('\\').append(c);
        } else if (c < 0x20) {
          b.append(String.format("\\u%04x", (int) c));
        } else {
          b.append(c);
        }
      }
      return b.append('"').toString();
    }

    private void ws() {
      while (i < s.length() && Character.isWhitespace(s.charAt(i))) {
        i++;
      }
    }

    private boolean peek(char c) {
      if (i < s.length() && s.charAt(i) == c) {
        i++;
        return true;
      }
      return false;
    }

    private void expect(char c) {
      if (!peek(c)) {
        throw error("expected '" + c + "'");
      }
    }

    private IllegalArgumentException error(String message) {
      return new IllegalArgumentException("vectors: " + message + " at offset " + i);
    }
  }
}
