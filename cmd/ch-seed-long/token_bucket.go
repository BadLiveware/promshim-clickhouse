package main

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// tokenBucket is a concurrency limiter whose capacity is read from an
// atomic.Int32 on every acquire. This lets the regulator change the
// effective concurrency without restarting workers.
//
// Semantics:
//   * Workers call acquire() before sending a batch and release() after.
//   * A worker whose ID is >= current target waits in a parking loop
//     until either: (a) its slot becomes valid (target rises), or
//     (b) the context is cancelled.
//   * Always-valid acquisition (current target == max) is the fast path
//     and incurs no atomic dance beyond the read.
//
// We use worker-ID-based slotting instead of a counter-based semaphore
// because a counter doesn't fairly drain "extra" workers when the
// regulator decreases target — the workers that happen to be holding
// tokens at decrease-time would keep them while new arrivals wait, but
// what we want is for the highest-ID workers to be the ones that pause.
type tokenBucket struct {
	max    int
	target *atomic.Int32

	mu   sync.Mutex
	cond *sync.Cond
}

func newTokenBucket(max int, target *atomic.Int32) *tokenBucket {
	tb := &tokenBucket{
		max:    max,
		target: target,
	}
	tb.cond = sync.NewCond(&tb.mu)
	return tb
}

// acquire blocks until the worker with workerID is permitted to run
// (i.e., workerID < current target) or the context is cancelled.
// Returns true on permission granted, false on cancellation.
func (tb *tokenBucket) acquire(ctx context.Context, workerID int) bool {
	// Fast path: worker is below target, no need to take the lock or
	// even read the atomic twice.
	if int(tb.target.Load()) > workerID {
		return true
	}

	// Slow path: park on the condition variable. To respect ctx
	// cancellation while parking, we run a short polling loop with a
	// timeout via cond.Broadcast wake-ups from a separate context-watch
	// goroutine. The simple approach here uses a periodic re-check.
	tb.mu.Lock()
	defer tb.mu.Unlock()
	for int(tb.target.Load()) <= workerID {
		select {
		case <-ctx.Done():
			return false
		default:
		}
		// Wait briefly then re-check. We don't wake on every regulator
		// change — that's noisy — but we re-check at most every 100ms.
		// Regulator typically ticks at 1-2s anyway.
		go func() {
			time.Sleep(100 * time.Millisecond)
			tb.cond.Broadcast()
		}()
		tb.cond.Wait()
	}
	return true
}

// release is a no-op in the slot-based design. It exists for API symmetry
// and to leave room for future bookkeeping (e.g., per-worker fairness
// metrics).
func (tb *tokenBucket) release() {}

// notify wakes parked workers. The regulator should call this after
// changing target to let workers re-evaluate.
func (tb *tokenBucket) notify() {
	tb.cond.Broadcast()
}
