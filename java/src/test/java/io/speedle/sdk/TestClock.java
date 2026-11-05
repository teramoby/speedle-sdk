/*
 * Copyright (c) 2026 The Speedle Authors
 * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 */
package io.speedle.sdk;

import java.util.ArrayList;
import java.util.List;

/** Virtual clock: {@code now} is real monotonic time plus an offset; {@code sleep} adds to it. */
final class TestClock implements Clock {
  private long offset;
  private final List<Long> sleeps = new ArrayList<>();

  @Override
  public synchronized long nanoTime() {
    return System.nanoTime() + offset;
  }

  @Override
  public synchronized void sleep(long nanos) throws InterruptedException {
    if (Thread.interrupted()) {
      throw new InterruptedException();
    }
    sleeps.add(nanos);
    offset += nanos;
  }

  synchronized void advanceMillis(long ms) {
    offset += ms * 1_000_000L;
  }

  /** The sleeps since the last call, in whole milliseconds. */
  synchronized List<Long> takeSleepsMillis() {
    List<Long> out = new ArrayList<>();
    for (long n : sleeps) {
      out.add(n / 1_000_000L);
    }
    sleeps.clear();
    return out;
  }
}
