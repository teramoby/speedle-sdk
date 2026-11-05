/*
 * Copyright (c) 2026 The Speedle Authors
 * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 */
package io.speedle.sdk;

import java.math.BigDecimal;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;

/**
 * Minimal JSON codec for the one schema this SDK speaks. Parsing yields {@link Map} (insertion
 * ordered), {@link List}, {@link String}, {@link BigDecimal}, {@link Boolean} and {@code null}
 * (represented by {@link #NULL}).
 */
final class Json {

  /** Marker for a JSON {@code null}, so that a missing key and a null value differ. */
  static final Object NULL =
      new Object() {
        @Override
        public String toString() {
          return "null";
        }
      };

  private static final int MAX_DEPTH = 64;

  private Json() {}

  // ---------------------------------------------------------------- writing

  static void writeString(StringBuilder out, String s) {
    out.append('"');
    for (int i = 0; i < s.length(); i++) {
      char c = s.charAt(i);
      switch (c) {
        case '"' -> out.append("\\\"");
        case '\\' -> out.append("\\\\");
        case '\n' -> out.append("\\n");
        case '\r' -> out.append("\\r");
        case '\t' -> out.append("\\t");
        case '\b' -> out.append("\\b");
        case '\f' -> out.append("\\f");
        default -> {
          if (c < 0x20 || c == 0x2028 || c == 0x2029) {
            out.append(String.format("\\u%04x", (int) c));
          } else {
            out.append(c);
          }
        }
      }
    }
    out.append('"');
  }

  /** Writes a finite double; integral values without a fraction or exponent where exact. */
  static void writeNumber(StringBuilder out, double d) {
    if (Double.isNaN(d) || Double.isInfinite(d)) {
      throw new IllegalArgumentException("non-finite number");
    }
    if (d == Math.rint(d) && Math.abs(d) < 1e15) {
      out.append((long) d);
    } else {
      out.append(Double.toString(d));
    }
  }

  /** Writes a parsed JSON value back to text. */
  static String write(Object v) {
    StringBuilder out = new StringBuilder();
    writeValue(out, v);
    return out.toString();
  }

  private static void writeValue(StringBuilder out, Object v) {
    if (v == NULL || v == null) {
      out.append("null");
    } else if (v instanceof String s) {
      writeString(out, s);
    } else if (v instanceof BigDecimal || v instanceof Boolean) {
      out.append(v);
    } else if (v instanceof Map<?, ?> m) {
      out.append('{');
      boolean first = true;
      for (Map.Entry<?, ?> e : m.entrySet()) {
        if (!first) {
          out.append(',');
        }
        first = false;
        writeString(out, (String) e.getKey());
        out.append(':');
        writeValue(out, e.getValue());
      }
      out.append('}');
    } else if (v instanceof List<?> l) {
      out.append('[');
      for (int i = 0; i < l.size(); i++) {
        if (i > 0) {
          out.append(',');
        }
        writeValue(out, l.get(i));
      }
      out.append(']');
    } else {
      throw new IllegalArgumentException("not a JSON value");
    }
  }

  // ---------------------------------------------------------------- parsing

  /** Parses one JSON document. Throws {@link IllegalArgumentException} on malformed input. */
  static Object parse(String text) {
    Parser p = new Parser(text);
    p.skipWs();
    Object v = p.value(0);
    p.skipWs();
    if (p.pos != text.length()) {
      throw p.error("trailing characters");
    }
    return v;
  }

  private static final class Parser {
    private final String s;
    private int pos;

    Parser(String s) {
      this.s = s;
    }

    IllegalArgumentException error(String what) {
      return new IllegalArgumentException("invalid JSON at offset " + pos + ": " + what);
    }

    void skipWs() {
      while (pos < s.length()) {
        char c = s.charAt(pos);
        if (c == ' ' || c == '\t' || c == '\n' || c == '\r') {
          pos++;
        } else {
          break;
        }
      }
    }

    Object value(int depth) {
      if (depth > MAX_DEPTH) {
        throw error("nesting too deep");
      }
      if (pos >= s.length()) {
        throw error("unexpected end");
      }
      char c = s.charAt(pos);
      return switch (c) {
        case '{' -> object(depth);
        case '[' -> array(depth);
        case '"' -> string();
        case 't' -> literal("true", Boolean.TRUE);
        case 'f' -> literal("false", Boolean.FALSE);
        case 'n' -> literal("null", NULL);
        default -> {
          if (c == '-' || (c >= '0' && c <= '9')) {
            yield number();
          }
          throw error("unexpected character");
        }
      };
    }

    Object literal(String word, Object v) {
      if (!s.startsWith(word, pos)) {
        throw error("invalid literal");
      }
      pos += word.length();
      return v;
    }

    Map<String, Object> object(int depth) {
      pos++; // {
      Map<String, Object> m = new LinkedHashMap<>();
      skipWs();
      if (peek() == '}') {
        pos++;
        return m;
      }
      while (true) {
        skipWs();
        if (peek() != '"') {
          throw error("expected key");
        }
        String k = string();
        skipWs();
        expect(':');
        skipWs();
        m.put(k, value(depth + 1));
        skipWs();
        char c = next();
        if (c == '}') {
          return m;
        }
        if (c != ',') {
          throw error("expected , or }");
        }
      }
    }

    List<Object> array(int depth) {
      pos++; // [
      List<Object> l = new ArrayList<>();
      skipWs();
      if (peek() == ']') {
        pos++;
        return l;
      }
      while (true) {
        skipWs();
        l.add(value(depth + 1));
        skipWs();
        char c = next();
        if (c == ']') {
          return l;
        }
        if (c != ',') {
          throw error("expected , or ]");
        }
      }
    }

    String string() {
      expect('"');
      StringBuilder b = new StringBuilder();
      while (true) {
        char c = next();
        if (c == '"') {
          return b.toString();
        }
        if (c < 0x20) {
          throw error("control character in string");
        }
        if (c != '\\') {
          b.append(c);
          continue;
        }
        char e = next();
        switch (e) {
          case '"' -> b.append('"');
          case '\\' -> b.append('\\');
          case '/' -> b.append('/');
          case 'b' -> b.append('\b');
          case 'f' -> b.append('\f');
          case 'n' -> b.append('\n');
          case 'r' -> b.append('\r');
          case 't' -> b.append('\t');
          case 'u' -> {
            if (pos + 4 > s.length()) {
              throw error("bad unicode escape");
            }
            try {
              b.append((char) Integer.parseInt(s.substring(pos, pos + 4), 16));
            } catch (NumberFormatException ex) {
              throw error("bad unicode escape");
            }
            pos += 4;
          }
          default -> throw error("bad escape");
        }
      }
    }

    BigDecimal number() {
      int start = pos;
      if (peek() == '-') {
        pos++;
      }
      if (peek() == '0') {
        pos++;
      } else if (isDigit(peek())) {
        while (isDigit(peek())) {
          pos++;
        }
      } else {
        throw error("bad number");
      }
      if (peek() == '.') {
        pos++;
        if (!isDigit(peek())) {
          throw error("bad number");
        }
        while (isDigit(peek())) {
          pos++;
        }
      }
      if (peek() == 'e' || peek() == 'E') {
        pos++;
        if (peek() == '+' || peek() == '-') {
          pos++;
        }
        if (!isDigit(peek())) {
          throw error("bad number");
        }
        while (isDigit(peek())) {
          pos++;
        }
      }
      try {
        return new BigDecimal(s.substring(start, pos));
      } catch (NumberFormatException | ArithmeticException ex) {
        throw error("bad number");
      }
    }

    private static boolean isDigit(char c) {
      return c >= '0' && c <= '9';
    }

    private char peek() {
      return pos < s.length() ? s.charAt(pos) : '\0';
    }

    private char next() {
      if (pos >= s.length()) {
        throw error("unexpected end");
      }
      return s.charAt(pos++);
    }

    private void expect(char c) {
      if (next() != c) {
        pos--;
        throw error("expected " + c);
      }
    }
  }
}
