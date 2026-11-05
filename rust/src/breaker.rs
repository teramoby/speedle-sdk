// Copyright (c) 2026 The Speedle Authors
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl.

use std::sync::Mutex;
use std::time::{Duration, Instant};

use crate::{BreakerState, BreakerStateChangeEvent, CircuitBreaker};

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Outcome {
    /// Validation error or cancellation: no change.
    Neutral,
    /// A 200 or any 4xx response.
    Success,
    /// Network, timeout, deadline or 5xx.
    Failure,
}

/// Counts calls, not attempts (design §6.6).
pub(crate) struct Breaker {
    threshold: u32,
    cool_down: Duration,
    state: Mutex<State>,
}

struct State {
    state: BreakerState,
    failures: u32,
    opened_at: Option<Instant>,
    trial: bool,
}

pub(crate) type Changes = Vec<BreakerStateChangeEvent>;

impl Breaker {
    pub(crate) fn new(cfg: &CircuitBreaker) -> Self {
        Breaker {
            threshold: cfg.failure_threshold.max(1),
            cool_down: cfg.cool_down,
            state: Mutex::new(State { state: BreakerState::Closed, failures: 0, opened_at: None, trial: false }),
        }
    }

    fn lock(&self) -> std::sync::MutexGuard<'_, State> {
        self.state.lock().unwrap_or_else(|e| e.into_inner())
    }

    /// Whether a call may proceed, and whether it is the half-open trial.
    pub(crate) fn allow(&self, now: Instant) -> (bool, bool, Changes) {
        let mut s = self.lock();
        match s.state {
            BreakerState::Open => {
                let cooled = s.opened_at.map_or(true, |at| now.saturating_duration_since(at) >= self.cool_down);
                if !cooled {
                    return (false, false, vec![]);
                }
                let change = s.move_to(BreakerState::HalfOpen);
                s.trial = true;
                (true, true, vec![change])
            }
            BreakerState::HalfOpen => {
                if s.trial {
                    return (false, false, vec![]);
                }
                s.trial = true;
                (true, true, vec![])
            }
            BreakerState::Closed => (true, false, vec![]),
        }
    }

    /// Applies the outcome of a call that `allow` let through.
    pub(crate) fn record(&self, trial: bool, outcome: Outcome, now: Instant) -> Changes {
        let mut s = self.lock();
        if trial {
            s.trial = false;
            if s.state != BreakerState::HalfOpen {
                return vec![];
            }
            return match outcome {
                Outcome::Success => {
                    s.failures = 0;
                    vec![s.move_to(BreakerState::Closed)]
                }
                Outcome::Failure => {
                    s.opened_at = Some(now);
                    vec![s.move_to(BreakerState::Open)]
                }
                Outcome::Neutral => vec![],
            };
        }
        if s.state != BreakerState::Closed {
            return vec![];
        }
        match outcome {
            Outcome::Success => s.failures = 0,
            Outcome::Failure => {
                s.failures += 1;
                if s.failures >= self.threshold {
                    s.opened_at = Some(now);
                    return vec![s.move_to(BreakerState::Open)];
                }
            }
            Outcome::Neutral => {}
        }
        vec![]
    }

    #[cfg(test)]
    pub(crate) fn state(&self) -> BreakerState {
        self.lock().state
    }
}

impl State {
    fn move_to(&mut self, to: BreakerState) -> BreakerStateChangeEvent {
        let ev = BreakerStateChangeEvent { from: self.state, to };
        self.state = to;
        ev
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn one_half_open_trial_and_neutral_release() {
        let b = Breaker::new(&CircuitBreaker { failure_threshold: 1, cool_down: Duration::from_secs(1) });
        let t0 = Instant::now();
        assert_eq!(b.record(false, Outcome::Failure, t0).len(), 1);
        assert!(!b.allow(t0).0);
        let (ok, trial, changes) = b.allow(t0 + Duration::from_secs(1));
        assert!(ok && trial && changes.len() == 1);
        assert!(!b.allow(t0 + Duration::from_secs(1)).0, "second call during the trial");
        assert!(b.record(true, Outcome::Neutral, t0).is_empty());
        let (ok, trial, _) = b.allow(t0 + Duration::from_secs(1));
        assert!(ok && trial, "slot freed after a neutral trial");
        b.record(true, Outcome::Failure, t0 + Duration::from_secs(1));
        assert_eq!(b.state(), BreakerState::Open);
    }

    #[test]
    fn success_resets_the_count() {
        let b = Breaker::new(&CircuitBreaker { failure_threshold: 2, cool_down: Duration::from_secs(1) });
        let t = Instant::now();
        b.record(false, Outcome::Failure, t);
        b.record(false, Outcome::Success, t);
        b.record(false, Outcome::Failure, t);
        assert_eq!(b.state(), BreakerState::Closed);
        b.record(false, Outcome::Neutral, t);
        b.record(false, Outcome::Failure, t);
        assert_eq!(b.state(), BreakerState::Open);
    }
}
