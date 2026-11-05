// Copyright (c) 2026 The Speedle Authors
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl.

package speedle

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestBackoffIsFullJitterCapped(t *testing.T) {
	c := &Client{retry: RetryPolicy{BaseDelay: 100 * time.Millisecond, MaxDelay: 2 * time.Second}, random: func() float64 { return 1 }}
	var got []time.Duration
	for n := 1; n <= 7; n++ {
		got = append(got, c.backoff(n))
	}
	want := []time.Duration{100e6, 200e6, 400e6, 800e6, 1600e6, 2e9, 2e9}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("backoff %v, want %v", got, want)
	}
	c.random = func() float64 { return 0.25 }
	if d := c.backoff(3); d != 100*time.Millisecond {
		t.Errorf("jitter 0.25 of 400ms = %v", d)
	}
	if d := c.backoff(200); d != 500*time.Millisecond {
		t.Errorf("large attempt overflowed: %v", d)
	}
}

func TestRetryAfterRaisesDelayOn503(t *testing.T) {
	s := newScript(t, reply{status: 503, headers: map[string]string{"Retry-After": "2"}}, reply{status: 200, body: okBody})
	c, v := testClient(t, s, nil)
	if ok, err := c.IsAllowed(bg, User("a"), "get", "/r", nil); !ok || err != nil {
		t.Fatalf("%v %v", ok, err)
	}
	if got := v.TakeSleeps(); !reflect.DeepEqual(got, []time.Duration{2 * time.Second}) {
		t.Errorf("sleeps %v", got)
	}
}

func TestBackoffThatDoesNotFitGivesUpWithDeadline(t *testing.T) {
	s := newScript(t, reply{status: 503}, reply{status: 503})
	rec := &recorder{}
	c, _ := testClient(t, s, func(c *Config) {
		c.Retry = RetryPolicy{BaseDelay: time.Second, Deadline: time.Second}
		c.Hooks = rec.hooks()
	})
	_, err := c.IsAllowed(bg, User("a"), "get", "/r", nil)
	if !errors.Is(err, ErrServiceUnavailable) || s.count() != 1 {
		t.Fatalf("%v after %d requests", err, s.count())
	}
	if len(rec.giveUps) != 1 || rec.giveUps[0].Reason != GiveUpDeadline {
		t.Errorf("give-up %+v", rec.giveUps)
	}
}

func TestBurst429WithoutRetryAfterIsNotRetried(t *testing.T) {
	s := newScript(t, reply{status: 429, body: `{"error":"rate_limited"}`})
	rec := &recorder{}
	c, _ := testClient(t, s, func(c *Config) { c.Hooks = rec.hooks() })
	_, err := c.IsAllowed(bg, User("a"), "get", "/r", nil)
	if !errors.Is(err, ErrRateLimited) || s.count() != 1 || rec.giveUps[0].Reason != GiveUpNonRetryable {
		t.Fatalf("%v, %d requests, %+v", err, s.count(), rec.giveUps)
	}
}

func TestAttemptTimeoutIsRetried(t *testing.T) {
	s := newScript(t, reply{hang: true}, reply{status: 200, body: okBody})
	rec := &recorder{}
	c, _ := testClient(t, s, func(c *Config) {
		c.Retry.AttemptTimeout = 100 * time.Millisecond
		c.Hooks = rec.hooks()
	})
	if ok, err := c.IsAllowed(bg, User("a"), "get", "/r", nil); !ok || err != nil {
		t.Fatalf("%v %v", ok, err)
	}
	if rec.attempts[0].ErrorType != string(KindTimeout) || rec.attempts[0].Status != 0 || rec.attempts[0].Duration < 100*time.Millisecond {
		t.Errorf("attempt %+v", rec.attempts[0])
	}
}

func TestLastAttemptTimeoutIsTimeoutError(t *testing.T) {
	s := newScript(t, reply{hang: true})
	c, _ := testClient(t, s, func(c *Config) { c.Retry = RetryPolicy{MaxAttempts: 1, AttemptTimeout: 50 * time.Millisecond} })
	_, err := c.IsAllowed(bg, User("a"), "get", "/r", nil)
	if !errors.Is(err, ErrTimeout) || errors.Is(err, ErrDeadlineExceeded) || !errors.Is(err, ErrNetwork) {
		t.Fatalf("%v", err)
	}
}

func TestCancellationDuringDelay(t *testing.T) {
	s := newScript(t, reply{status: 503})
	rec := &recorder{}
	ctx, cancel := context.WithCancel(bg)
	c, _ := testClient(t, s, func(c *Config) {
		c.FailureMode = FailureModeClosed
		c.Hooks = rec.hooks()
		c.Hooks.OnRetry = func(RetryEvent) { cancel() }
	})
	ok, err := c.IsAllowed(ctx, User("a"), "get", "/r", nil)
	if ok || !errors.Is(err, context.Canceled) {
		t.Fatalf("%v %v", ok, err)
	}
	var e *Error
	if errors.As(err, &e) {
		t.Errorf("cancellation is an *Error: %v", e)
	}
	if len(rec.giveUps) != 0 {
		t.Errorf("give-up fired on cancellation: %+v", rec.giveUps)
	}
}

func TestRealSleepIsCancellable(t *testing.T) {
	s := newScript(t, reply{status: 503})
	c, _ := testClient(t, s, func(c *Config) { c.Retry = RetryPolicy{BaseDelay: time.Second, Deadline: time.Minute} })
	c.clock = systemClock()
	ctx, cancel := context.WithCancel(bg)
	defer cancel()
	time.AfterFunc(200*time.Millisecond, cancel)
	start := time.Now()
	_, err := c.IsAllowed(ctx, User("a"), "get", "/r", nil)
	if !errors.Is(err, context.Canceled) || time.Since(start) > 900*time.Millisecond {
		t.Fatalf("%v after %v", err, time.Since(start))
	}
}

func TestCallerDeadlineShortensTheCall(t *testing.T) {
	s := newScript(t, reply{status: 503}, reply{status: 503})
	c, v := testClient(t, s, func(c *Config) { c.Retry.BaseDelay = 500 * time.Millisecond })
	ctx, cancel := context.WithTimeout(bg, 300*time.Millisecond)
	defer cancel()
	_, err := c.IsAllowed(ctx, User("a"), "get", "/r", nil)
	// The 500 ms delay does not fit the 300 ms caller bound: give up at once.
	if !errors.Is(err, ErrServiceUnavailable) || s.count() != 1 || len(v.TakeSleeps()) != 0 {
		t.Fatalf("%v after %d requests", err, s.count())
	}
}

func TestAlreadyCancelledContext(t *testing.T) {
	s := newScript(t)
	c, _ := testClient(t, s, nil)
	ctx, cancel := context.WithCancel(bg)
	cancel()
	if _, err := c.Check(ctx, User("a"), "get", "/r", nil); !errors.Is(err, context.Canceled) || s.count() != 0 {
		t.Fatalf("%v", err)
	}
}

func TestFailClosed(t *testing.T) {
	s := newScript(t, reply{status: 401, body: `{"error":"unauthorized"}`}, reply{status: 401, body: `{"error":"unauthorized"}`})
	rec := &recorder{}
	c, _ := testClient(t, s, func(c *Config) { c.FailureMode = FailureModeClosed; c.Hooks = rec.hooks() })
	if ok, err := c.IsAllowed(bg, User("a"), "get", "/r", nil); ok || err != nil {
		t.Errorf("IsAllowed: %v %v", ok, err)
	}
	if ok, err := c.IsAllowed(bg, nil, "get", "/r", nil); ok || err != nil {
		t.Errorf("IsAllowed with a validation error: %v %v", ok, err)
	}
	if _, err := c.Check(bg, User("a"), "get", "/r", nil); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("Check: %v", err)
	}
	if len(rec.giveUps) != 3 {
		t.Errorf("give-ups %+v", rec.giveUps)
	}
}

func TestHookPanicsAreIgnored(t *testing.T) {
	s := newScript(t, reply{status: 503}, reply{status: 200, body: `{"allowed":false,"reason":1}`})
	boom := func() { panic("hook") }
	c, _ := testClient(t, s, func(c *Config) {
		c.Hooks = Hooks{
			OnAttempt: func(AttemptEvent) { boom() }, OnRetry: func(RetryEvent) { boom() },
			OnGiveUp: func(GiveUpEvent) { boom() }, OnBreakerStateChange: func(BreakerStateChangeEvent) { boom() },
		}
		c.CircuitBreaker = &CircuitBreaker{FailureThreshold: 1}
	})
	if ok, err := c.IsAllowed(bg, User("a"), "get", "/r", nil); ok || err != nil {
		t.Fatalf("%v %v", ok, err)
	}
}

func TestBreaker(t *testing.T) {
	s := newScript(t,
		reply{status: 502},                     // failure 1
		reply{status: 404},                     // a 4xx resets the count
		reply{status: 502}, reply{status: 504}, // opens
		reply{status: 500},               // half-open trial fails: open again
		reply{status: 200, body: okBody}, // half-open trial succeeds
	)
	rec := &recorder{}
	c, v := testClient(t, s, func(c *Config) {
		c.Retry.MaxAttempts = 1
		c.CircuitBreaker = &CircuitBreaker{FailureThreshold: 2, CoolDown: 10 * time.Second}
		c.Hooks = rec.hooks()
	})
	call := func() error { _, err := c.IsAllowed(bg, User("a"), "get", "/r", nil); return err }
	for range 4 {
		_ = call()
	}
	if c.breaker.state != BreakerOpen || s.count() != 4 {
		t.Fatalf("state %s after %d requests", c.breaker.state, s.count())
	}
	_ = call() // validation errors do not count either way
	if _, err := c.IsAllowed(bg, nil, "get", "/r", nil); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal(err)
	}
	if err := call(); !errors.Is(err, ErrCircuitOpen) || s.count() != 4 {
		t.Fatalf("%v", err)
	}
	v.Advance(10 * time.Second)
	if err := call(); !errors.Is(err, ErrServer) {
		t.Fatalf("trial: %v", err)
	}
	if err := call(); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("after failed trial: %v", err)
	}
	v.Advance(10 * time.Second)
	if err := call(); err != nil {
		t.Fatalf("second trial: %v", err)
	}
	want := []BreakerStateChangeEvent{
		{BreakerClosed, BreakerOpen}, {BreakerOpen, BreakerHalfOpen}, {BreakerHalfOpen, BreakerOpen},
		{BreakerOpen, BreakerHalfOpen}, {BreakerHalfOpen, BreakerClosed},
	}
	if !reflect.DeepEqual(rec.breaker, want) {
		t.Errorf("transitions %v", rec.breaker)
	}
}

func TestBreakerAllowsOneHalfOpenTrial(t *testing.T) {
	b := newBreaker(CircuitBreaker{FailureThreshold: 1, CoolDown: time.Second})
	now := time.Now()
	b.record(false, outcomeFailure, now)
	ok, trial, _ := b.allow(now.Add(time.Second))
	if !ok || !trial {
		t.Fatal("trial not allowed")
	}
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, _, _ := b.allow(now.Add(time.Second)); ok {
				t.Error("second concurrent call allowed during the trial")
			}
		}()
	}
	wg.Wait()
	b.record(true, outcomeNeutral, now) // a cancelled trial frees the slot
	if ok, trial, _ := b.allow(now.Add(time.Second)); !ok || !trial {
		t.Error("slot not freed after a neutral trial")
	}
}
