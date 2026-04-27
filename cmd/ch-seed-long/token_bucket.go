package main

import (
	"context"
	"sync/atomic"
	"time"
)

// concurrencyLimiter gates how many workers may proceed at any moment, with
// the cap controlled externally by an atomic.Int32 (the regulator's target).
//
// Design: poll-based, not cond-var-based. A previous design used sync.Cond
// with broadcast wake-ups; under sustained over-target conditions that
// produced a self-perpetuating broadcast storm (every parked worker spawned
// a per-iteration sleeper goroutine, each sleeper woke all parked workers,
// each then spawned its own sleeper — quadratic scheduling churn).
//
// Slot semantics: a worker with workerID < target is permitted; otherwise
// it polls every PollInterval until either (a) the target rises above its
// ID, (b) the context is cancelled, or (c) the draining flag is set
// (signaling the generator has closed the channel and parked workers
// should exit cleanly without consuming further batches).
type concurrencyLimiter struct {
	target       *atomic.Int32
	draining     *atomic.Bool
	pollInterval time.Duration
}

func newConcurrencyLimiter(target *atomic.Int32, draining *atomic.Bool) *concurrencyLimiter {
	return &concurrencyLimiter{
		target:       target,
		draining:     draining,
		pollInterval: 50 * time.Millisecond,
	}
}

// waitForSlot blocks until workerID < current target. Returns true on
// permission granted, false on cancellation or drain. Should be called
// BEFORE dequeuing a batch — calling it after means a parked worker is
// holding a stranded batch the regulator wants to suppress.
func (cl *concurrencyLimiter) waitForSlot(ctx context.Context, workerID int) bool {
	// Fast path: usually the worker is below target.
	if int(cl.target.Load()) > workerID {
		return true
	}
	// Slow path: poll. Cheap (one atomic read per tick) and bounded
	// (no goroutine spawning, no broadcast cascades).
	for {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(cl.pollInterval):
		}
		if cl.draining.Load() {
			return false
		}
		if int(cl.target.Load()) > workerID {
			return true
		}
	}
}
