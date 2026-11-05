// Copyright (c) 2026 The Speedle Authors
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl.

//! The time and randomness seam of the retry loop and the breaker (design
//! §11). Crate-private: only the in-crate conformance runner and unit tests
//! replace it.

use std::collections::hash_map::RandomState;
use std::future::Future;
use std::hash::{BuildHasher, Hasher};
use std::pin::Pin;
use std::sync::atomic::{AtomicU64, Ordering};
#[cfg(test)]
use std::sync::Mutex;
use std::time::{Duration, Instant};

pub(crate) type SleepFuture = Pin<Box<dyn Future<Output = ()> + Send>>;

pub(crate) trait Clock: Send + Sync {
    fn now(&self) -> Instant;
    fn sleep(&self, d: Duration) -> SleepFuture;
}

pub(crate) struct SystemClock;

impl Clock for SystemClock {
    fn now(&self) -> Instant {
        Instant::now()
    }

    fn sleep(&self, d: Duration) -> SleepFuture {
        Box::pin(tokio::time::sleep(d))
    }
}

/// Sleep returns at once and adds the duration to an offset; now is real
/// monotonic time plus the offset. Every sleep is recorded.
#[cfg(test)]
#[derive(Default)]
pub(crate) struct VirtualClock {
    state: Mutex<(Duration, Vec<Duration>)>,
}

#[cfg(test)]
impl VirtualClock {
    pub(crate) fn advance(&self, d: Duration) {
        self.state.lock().unwrap().0 += d;
    }

    pub(crate) fn take_sleeps(&self) -> Vec<Duration> {
        std::mem::take(&mut self.state.lock().unwrap().1)
    }
}

#[cfg(test)]
impl Clock for VirtualClock {
    fn now(&self) -> Instant {
        Instant::now() + self.state.lock().unwrap().0
    }

    fn sleep(&self, d: Duration) -> SleepFuture {
        let mut s = self.state.lock().unwrap();
        s.0 += d;
        s.1.push(d);
        Box::pin(std::future::ready(()))
    }
}

/// A uniform source in [0, 1].
pub(crate) trait Random: Send + Sync {
    fn next(&self) -> f64;
}

/// Seeded from the standard library's per-process random hash keys; no
/// extra dependency. Jitter needs spread, not cryptographic quality.
pub(crate) struct DefaultRandom {
    keys: RandomState,
    counter: AtomicU64,
}

impl DefaultRandom {
    pub(crate) fn new() -> Self {
        DefaultRandom { keys: RandomState::new(), counter: AtomicU64::new(0) }
    }
}

impl Random for DefaultRandom {
    fn next(&self) -> f64 {
        let mut h = self.keys.build_hasher();
        h.write_u64(self.counter.fetch_add(1, Ordering::Relaxed));
        (h.finish() >> 11) as f64 / (1u64 << 53) as f64
    }
}

#[cfg(test)]
pub(crate) struct FixedRandom(pub(crate) f64);

#[cfg(test)]
impl Random for FixedRandom {
    fn next(&self) -> f64 {
        self.0
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn default_random_is_in_range_and_varies() {
        let r = DefaultRandom::new();
        let xs: Vec<f64> = (0..1000).map(|_| r.next()).collect();
        assert!(xs.iter().all(|x| (0.0..1.0).contains(x)));
        let mean = xs.iter().sum::<f64>() / xs.len() as f64;
        assert!((0.4..0.6).contains(&mean), "mean {mean}");
    }

    #[test]
    fn virtual_clock_records_sleeps() {
        let c = VirtualClock::default();
        let t0 = c.now();
        drop(c.sleep(Duration::from_millis(100)));
        c.advance(Duration::from_secs(1));
        assert!(c.now() - t0 >= Duration::from_millis(1100));
        assert_eq!(c.take_sleeps(), vec![Duration::from_millis(100)]);
        assert!(c.take_sleeps().is_empty());
    }
}
