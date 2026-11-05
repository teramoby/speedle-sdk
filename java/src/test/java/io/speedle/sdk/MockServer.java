/*
 * Copyright (c) 2026 The Speedle Authors
 * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 */
package io.speedle.sdk;

import java.io.BufferedInputStream;
import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.InetAddress;
import java.net.ServerSocket;
import java.net.Socket;
import java.net.URI;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.concurrent.CopyOnWriteArrayList;
import java.util.concurrent.atomic.AtomicInteger;
import javax.net.ssl.SSLContext;
import javax.net.ssl.SSLServerSocket;

/**
 * A small HTTP/1.1 server on 127.0.0.1 that answers scripted responses in order, injects faults and
 * records every request. HTTPS when given an {@link SSLContext}.
 */
final class MockServer implements AutoCloseable {

  /** A scripted answer. */
  record Reply(int status, Map<String, String> headers, String body, String fault) {
    static Reply json(int status, String body) {
      return new Reply(status, Map.of(), body, null);
    }

    static Reply json(int status, String body, Map<String, String> headers) {
      return new Reply(status, headers, body, null);
    }

    static Reply fault(String fault) {
      return new Reply(0, Map.of(), null, fault);
    }
  }

  /** A received request; header names are lower case. */
  record Request(String method, String path, Map<String, String> headers, String body) {}

  private final ServerSocket server;
  private final List<Reply> script = new CopyOnWriteArrayList<>();
  private final List<Request> requests = new CopyOnWriteArrayList<>();
  private final List<Socket> sockets = Collections.synchronizedList(new ArrayList<>());
  private final AtomicInteger next = new AtomicInteger();
  private final AtomicInteger connections = new AtomicInteger();
  private final boolean tls;
  private volatile boolean closed;

  MockServer(SSLContext tls) throws IOException {
    this(tls, false);
  }

  MockServer(SSLContext tls, boolean needClientAuth) throws IOException {
    InetAddress lo = InetAddress.getByName("127.0.0.1");
    if (tls != null) {
      SSLServerSocket s =
          (SSLServerSocket) tls.getServerSocketFactory().createServerSocket(0, 50, lo);
      s.setNeedClientAuth(needClientAuth);
      server = s;
    } else {
      server = new ServerSocket(0, 50, lo);
    }
    this.tls = tls != null;
    Thread t = new Thread(this::acceptLoop, "mock-accept");
    t.setDaemon(true);
    t.start();
  }

  MockServer script(Reply... replies) {
    script.addAll(List.of(replies));
    return this;
  }

  URI endpoint() {
    return URI.create((tls ? "https" : "http") + "://127.0.0.1:" + server.getLocalPort());
  }

  URI endpoint(String host) {
    return URI.create((tls ? "https" : "http") + "://" + host + ":" + server.getLocalPort());
  }

  List<Request> requests() {
    return List.copyOf(requests);
  }

  int connections() {
    return connections.get();
  }

  @Override
  public void close() throws IOException {
    closed = true;
    server.close();
    synchronized (sockets) {
      for (Socket s : sockets) {
        try {
          s.close();
        } catch (IOException e) {
          // ignore
        }
      }
    }
  }

  private void acceptLoop() {
    while (!closed) {
      try {
        Socket s = server.accept();
        sockets.add(s);
        connections.incrementAndGet();
        Thread t = new Thread(() -> serve(s), "mock-conn");
        t.setDaemon(true);
        t.start();
      } catch (IOException e) {
        return;
      }
    }
  }

  private void serve(Socket s) {
    try (s) {
      InputStream in = new BufferedInputStream(s.getInputStream());
      OutputStream out = s.getOutputStream();
      while (!closed) {
        Request req = read(in);
        if (req == null) {
          return;
        }
        requests.add(req);
        int i = next.getAndIncrement();
        Reply r =
            i < script.size()
                ? script.get(i)
                : Reply.json(599, "{\"error\":\"mock: unexpected request\"}");
        if ("connection_reset".equals(r.fault())) {
          s.setSoLinger(true, 0);
          return;
        }
        if ("no_response".equals(r.fault())) {
          while (in.read() >= 0) {
            // wait until the client gives up and closes the connection
          }
          return;
        }
        write(out, r);
      }
    } catch (IOException e) {
      // connection closed
    }
  }

  private static Request read(InputStream in) throws IOException {
    String line = readLine(in);
    if (line == null || line.isEmpty()) {
      return null;
    }
    String[] parts = line.split(" ");
    Map<String, String> headers = new LinkedHashMap<>();
    String h;
    while ((h = readLine(in)) != null && !h.isEmpty()) {
      int c = h.indexOf(':');
      headers.put(h.substring(0, c).trim().toLowerCase(Locale.ROOT), h.substring(c + 1).trim());
    }
    int len = Integer.parseInt(headers.getOrDefault("content-length", "0"));
    byte[] body = in.readNBytes(len);
    return new Request(parts[0], parts[1], headers, new String(body, StandardCharsets.UTF_8));
  }

  private static String readLine(InputStream in) throws IOException {
    ByteArrayOutputStream b = new ByteArrayOutputStream();
    int c;
    while ((c = in.read()) >= 0) {
      if (c == '\n') {
        String s = b.toString(StandardCharsets.ISO_8859_1);
        return s.endsWith("\r") ? s.substring(0, s.length() - 1) : s;
      }
      b.write(c);
    }
    return b.size() == 0 ? null : b.toString(StandardCharsets.ISO_8859_1);
  }

  private static void write(OutputStream out, Reply r) throws IOException {
    byte[] body = r.body() == null ? new byte[0] : r.body().getBytes(StandardCharsets.UTF_8);
    StringBuilder head = new StringBuilder();
    head.append("HTTP/1.1 ").append(r.status()).append(" Mock\r\n");
    boolean hasType = false;
    for (Map.Entry<String, String> e : r.headers().entrySet()) {
      hasType |= e.getKey().equalsIgnoreCase("Content-Type");
      head.append(e.getKey()).append(": ").append(e.getValue()).append("\r\n");
    }
    if (!hasType && r.body() != null) {
      head.append("Content-Type: application/json\r\n");
    }
    head.append("Content-Length: ").append(body.length).append("\r\n\r\n");
    out.write(head.toString().getBytes(StandardCharsets.ISO_8859_1));
    out.write(body);
    out.flush();
  }
}
