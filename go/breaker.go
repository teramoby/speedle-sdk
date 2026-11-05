// Copyright (c) 2026 The Speedle Authors
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl.

package speedle

import (
	"sync"
	"time"
)

type outcome int

const (
	outcomeNeutral outcome = iota // validation error or cancellation: no change
	outcomeSuccess                // a 200 or any 4xx response
	outcomeFailure                // network, timeout, deadline or 5xx
)

// breaker counts calls, not attempts (design §6.6).
type breaker struct {
	threshold int
	coolDown  time.Duration

	mu       sync.Mutex
	state    BreakerState
	failures int
	openedAt time.Time
	trial    bool // a half-open trial call is in flight
}

func newBreaker(cfg CircuitBreaker) *breaker {
	b := &breaker{threshold: cfg.FailureThreshold, coolDown: cfg.CoolDown, state: BreakerClosed}
	if b.threshold == 0 {
		b.threshold = 5
	}
	if b.coolDown == 0 {
		b.coolDown = 30 * time.Second
	}
	return b
}

// allow decides whether a call may proceed. trial is true when the call is
// the single half-open trial.
func (b *breaker) allow(now time.Time) (ok, trial bool, changes []BreakerStateChangeEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case BreakerOpen:
		if now.Sub(b.openedAt) < b.coolDown {
			return false, false, nil
		}
		changes = append(changes, b.move(BreakerHalfOpen))
		b.trial = true
		return true, true, changes
	case BreakerHalfOpen:
		if b.trial {
			return false, false, nil
		}
		b.trial = true
		return true, true, nil
	}
	return true, false, nil
}

// record applies the outcome of a call that allow let through.
func (b *breaker) record(trial bool, o outcome, now time.Time) []BreakerStateChangeEvent {
	b.mu.Lock()
	defer b.mu.Unlock()
	if trial {
		b.trial = false
		if b.state != BreakerHalfOpen {
			return nil
		}
		switch o {
		case outcomeSuccess:
			b.failures = 0
			return []BreakerStateChangeEvent{b.move(BreakerClosed)}
		case outcomeFailure:
			b.openedAt = now
			return []BreakerStateChangeEvent{b.move(BreakerOpen)}
		}
		return nil
	}
	if b.state != BreakerClosed {
		return nil // a call that started before the breaker opened
	}
	switch o {
	case outcomeSuccess:
		b.failures = 0
	case outcomeFailure:
		b.failures++
		if b.failures >= b.threshold {
			b.openedAt = now
			return []BreakerStateChangeEvent{b.move(BreakerOpen)}
		}
	}
	return nil
}

func (b *breaker) move(to BreakerState) BreakerStateChangeEvent {
	ev := BreakerStateChangeEvent{From: b.state, To: to}
	b.state = to
	return ev
}
