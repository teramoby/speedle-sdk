/*
 * Copyright (c) 2026 The Speedle Authors
 * Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl/
 */
package io.speedle.sdk;

/**
 * Time source of the retry loop and the breaker. Package-private: the conformance runner replaces
 * it with a virtual clock.
 */
interface Clock {

  /** Monotonic time in nanoseconds. */
  long nanoTime();

  /** Sleeps for the given number of nanoseconds; interruptible. */
  void sleep(long nanos) throws InterruptedException;

  Clock SYSTEM =
      new Clock() {
        @Override
        public long nanoTime() {
          return System.nanoTime();
        }

        @Override
        public void sleep(long nanos) throws InterruptedException {
          if (nanos > 0) {
            Thread.sleep(nanos / 1_000_000L, (int) (nanos % 1_000_000L));
          } else if (Thread.interrupted()) {
            throw new InterruptedException();
          }
        }
      };
}
