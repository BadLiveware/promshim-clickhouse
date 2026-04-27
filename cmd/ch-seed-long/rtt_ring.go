package main

import (
	"sort"
	"sync"
	"time"
)

// rttRing is a fixed-size ring buffer of recent batch outcomes.
// Used by the regulator to compute p50/p99/error_rate over a moving window
// without locking on every observation hot path.
type rttRing struct {
	mu       sync.Mutex
	cap      int
	dur      []time.Duration
	wasError []bool
	next     int
	filled   bool
}

func newRTTRing(capacity int) *rttRing {
	if capacity < 1 {
		capacity = 1
	}
	return &rttRing{
		cap:      capacity,
		dur:      make([]time.Duration, capacity),
		wasError: make([]bool, capacity),
	}
}

// observe records the outcome of one remote-write batch. Must be safe under
// concurrent worker calls.
func (r *rttRing) observe(d time.Duration, isErr bool) {
	r.mu.Lock()
	r.dur[r.next] = d
	r.wasError[r.next] = isErr
	r.next++
	if r.next >= r.cap {
		r.next = 0
		r.filled = true
	}
	r.mu.Unlock()
}

// summary returns p50, p99, and error percentage over the current ring
// contents. Returns zero durations and 0% when the ring is empty.
func (r *rttRing) summary() (p50, p99 time.Duration, errPct float64) {
	r.mu.Lock()
	n := r.next
	if r.filled {
		n = r.cap
	}
	if n == 0 {
		r.mu.Unlock()
		return 0, 0, 0
	}
	durs := make([]time.Duration, n)
	copy(durs, r.dur[:n])
	errs := 0
	for i := 0; i < n; i++ {
		if r.wasError[i] {
			errs++
		}
	}
	r.mu.Unlock()

	sort.Slice(durs, func(i, j int) bool { return durs[i] < durs[j] })
	p50Idx := n / 2
	p99Idx := (n * 99) / 100
	if p99Idx >= n {
		p99Idx = n - 1
	}
	return durs[p50Idx], durs[p99Idx], 100 * float64(errs) / float64(n)
}
