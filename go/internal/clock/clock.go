// Copyright (c) 2026 The Speedle Authors
// Licensed under the Universal Permissive License v 1.0 as shown at https://oss.oracle.com/licenses/upl.

// Package clock is the time and randomness seam of the retry loop and the
// circuit breaker. Production code uses System and math/rand; the
// conformance runner injects a virtual clock and a fixed random source.
// It is internal so that it is not part of the public API.
package clock

import (
	"context"
	"sync"
	"time"
)

// Clock is what the retry loop and the breaker read time through.
type Clock interface {
	Now() time.Time
	// Sleep waits for d or until ctx is done, whichever comes first, and
	// returns ctx.Err() in the second case.
	Sleep(ctx context.Context, d time.Duration) error
}

// System is the real clock.
type System struct{}

func (System) Now() time.Time { return time.Now() }

func (System) Sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Virtual is the conformance clock: Sleep returns at once and adds the
// duration to an offset, and Now is real monotonic time plus the offset.
// It records every requested sleep.
type Virtual struct {
	mu     sync.Mutex
	offset time.Duration
	sleeps []time.Duration
}

func (v *Virtual) Now() time.Time {
	v.mu.Lock()
	defer v.mu.Unlock()
	return time.Now().Add(v.offset)
}

func (v *Virtual) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.sleeps = append(v.sleeps, d)
	if d > 0 {
		v.offset += d
	}
	return nil
}

// Advance adds d to the offset without recording a sleep.
func (v *Virtual) Advance(d time.Duration) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.offset += d
}

// TakeSleeps returns the sleeps recorded since the last call and clears them.
func (v *Virtual) TakeSleeps() []time.Duration {
	v.mu.Lock()
	defer v.mu.Unlock()
	s := v.sleeps
	v.sleeps = nil
	return s
}
