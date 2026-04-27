package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// rampUp drives the regulator with stable, low-RTT, no-error conditions and
// asserts it climbs from initial to MaxN within a bounded number of ticks.
func TestRegulatorRampsUpUnderStableLatency(t *testing.T) {
	ring := newRTTRing(64)
	for i := 0; i < 16; i++ {
		ring.observe(10*time.Millisecond, false)
	}

	var target atomic.Int32
	target.Store(1)

	cfg := regulatorConfig{
		Tick:            5 * time.Millisecond,
		MaxN:            8,
		MinN:            1,
		HoldBandPct:     1.5,
		IncreaseAtPct:   1.5, // generous ramp condition for the test
		TailDecreasePct: 3.0,
		MinSamples:      4,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	go runRegulator(ctx, &target, ring, nil, nil, cfg)

	// Continuously feed low-latency observations so baseline stays low.
	feedDone := make(chan struct{})
	go func() {
		ticker := time.NewTicker(2 * time.Millisecond)
		defer ticker.Stop()
		defer close(feedDone)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				ring.observe(10*time.Millisecond, false)
			}
		}
	}()

	// Wait for ramp-up.
	deadline := time.Now().Add(400 * time.Millisecond)
	for time.Now().Before(deadline) {
		if target.Load() == cfg.MaxN {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := target.Load(); got != cfg.MaxN {
		t.Errorf("expected target to reach MaxN=%d, got %d", cfg.MaxN, got)
	}
	cancel()
	<-feedDone
}

// errorTriggersHalving verifies the multiplicative-decrease branch.
func TestRegulatorHalvesOnErrors(t *testing.T) {
	ring := newRTTRing(64)
	// Seed low-latency baseline first.
	for i := 0; i < 32; i++ {
		ring.observe(10*time.Millisecond, false)
	}

	var target atomic.Int32
	target.Store(8)

	cfg := regulatorConfig{
		Tick:        5 * time.Millisecond,
		MaxN:        16,
		MinN:        1,
		HoldBandPct: 1.5,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	go runRegulator(ctx, &target, ring, nil, nil, cfg)

	// Inject errors.
	for i := 0; i < 16; i++ {
		ring.observe(10*time.Millisecond, true)
	}

	// One tick should be enough for halving.
	deadline := time.Now().Add(100 * time.Millisecond)
	for time.Now().Before(deadline) {
		if target.Load() < 8 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if got := target.Load(); got >= 8 {
		t.Errorf("expected target to drop below 8 on errors, got %d", got)
	}
	cancel()
}

// tailLatencyTriggersAdditiveDecrease verifies the p99/p50-ratio branch
// (additive, not multiplicative).
func TestRegulatorThrottlesOnTailLatency(t *testing.T) {
	ring := newRTTRing(128)

	var target atomic.Int32
	target.Store(8)

	cfg := regulatorConfig{
		Tick:            5 * time.Millisecond,
		MaxN:            16,
		MinN:            1,
		IncreaseAtPct:   1.2,
		HoldBandPct:     1.5,
		TailDecreasePct: 3.0,
	}

	// Mostly fast samples, sparse very-slow ones → high p99/p50 ratio.
	for i := 0; i < 100; i++ {
		ring.observe(10*time.Millisecond, false)
	}
	for i := 0; i < 5; i++ {
		ring.observe(500*time.Millisecond, false) // 50× p50
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	go runRegulator(ctx, &target, ring, nil, nil, cfg)

	deadline := time.Now().Add(150 * time.Millisecond)
	for time.Now().Before(deadline) {
		if target.Load() == 7 {
			break // dropped by 1
		}
		time.Sleep(2 * time.Millisecond)
	}
	if got := target.Load(); got != 7 {
		t.Errorf("expected target to drop by 1 on tail-latency, got %d", got)
	}
	cancel()
}

// respectsMaxN verifies the regulator never exceeds the configured ceiling
// even under very-stable, ramp-friendly conditions.
func TestRegulatorRespectsMaxN(t *testing.T) {
	ring := newRTTRing(64)
	for i := 0; i < 32; i++ {
		ring.observe(5*time.Millisecond, false)
	}

	var target atomic.Int32
	target.Store(2)

	cfg := regulatorConfig{
		Tick:          2 * time.Millisecond,
		MaxN:          4,
		MinN:          1,
		IncreaseAtPct: 2.0,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	feedDone := make(chan struct{})
	go func() {
		defer close(feedDone)
		ticker := time.NewTicker(1 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				ring.observe(5*time.Millisecond, false)
			}
		}
	}()

	go runRegulator(ctx, &target, ring, nil, nil, cfg)

	time.Sleep(150 * time.Millisecond)
	cancel()
	<-feedDone

	if got := target.Load(); got > cfg.MaxN {
		t.Errorf("expected target to respect MaxN=%d, got %d", cfg.MaxN, got)
	}
}

// stallDetection verifies the regulator throttles when no batches complete
// for StallTicks consecutive ticks, even while the RTT ring shows healthy
// recent latency. This is the failure mode where workers are all stuck
// mid-POST waiting on a slow backend — invisible to RTT-based throttling.
func TestRegulatorThrottlesOnStall(t *testing.T) {
	ring := newRTTRing(64)
	// Seed healthy-looking observations: ring shows fine p50/p99 ratio,
	// suggesting throttling is unnecessary.
	for i := 0; i < 32; i++ {
		ring.observe(50*time.Millisecond, false)
	}

	var target atomic.Int32
	target.Store(8)

	// completedBatches stays at 0 — simulating workers stuck in mid-POST.
	var completed atomic.Int64
	completed.Store(0)

	cfg := regulatorConfig{
		Tick:            5 * time.Millisecond,
		MaxN:            16,
		MinN:            1,
		HoldBandPct:     1.5,
		IncreaseAtPct:   1.2,
		TailDecreasePct: 3.0,
		StallTicks:      3, // 3 ticks of zero progress triggers throttle
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	go runRegulator(ctx, &target, ring, &completed, nil, cfg)

	// Stall halves target per fire and suppresses ramp-up for 5 ticks.
	// Repeated stall fires should drive target down to MinN within a
	// few hundred ms (8 → 4 → 2 → 1).
	deadline := time.Now().Add(400 * time.Millisecond)
	for time.Now().Before(deadline) {
		if target.Load() <= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := target.Load(); got > 4 {
		t.Errorf("expected sustained stall to drive target ≤ 4, got %d", got)
	}
	cancel()
}

// stallDetectionWithEmptyRing verifies the stall branch fires even when
// the RTT ring is empty (no batches have completed). This is the exact
// failure mode observed against Prometheus stress-50k: the first batches
// never complete, so the ring stays empty, and a top-of-loop "skip if
// p50 == 0" prevented stall detection from running. Regression guard.
func TestRegulatorThrottlesOnStallWithEmptyRing(t *testing.T) {
	ring := newRTTRing(64) // intentionally empty — no completed batches yet

	var target atomic.Int32
	target.Store(8)

	var completed atomic.Int64
	completed.Store(0)

	cfg := regulatorConfig{
		Tick:       5 * time.Millisecond,
		MaxN:       16,
		MinN:       1,
		StallTicks: 3,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	go runRegulator(ctx, &target, ring, &completed, nil, cfg)

	// Stall halves target each fire. With Tick=5ms and StallTicks=3 the
	// first throttle should land within ~20ms; sustained stall drives
	// 8 → 4 → 2 → 1 within a few hundred ms.
	deadline := time.Now().Add(400 * time.Millisecond)
	for time.Now().Before(deadline) {
		if target.Load() <= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := target.Load(); got > 4 {
		t.Errorf("expected stall (with empty RTT ring) to drive target ≤ 4, got %d", got)
	}
	cancel()
}

// stallDetectionResetsOnProgress verifies the stall counter resets when
// batches start completing again (i.e., a transient stall doesn't keep
// throttling forever after the system recovers).
func TestRegulatorStallResetsOnProgress(t *testing.T) {
	ring := newRTTRing(64)
	for i := 0; i < 32; i++ {
		ring.observe(50*time.Millisecond, false)
	}

	var target atomic.Int32
	target.Store(8)

	var completed atomic.Int64
	completed.Store(100) // start with some progress so stall counter doesn't trip immediately

	cfg := regulatorConfig{
		Tick:          5 * time.Millisecond,
		MaxN:          16,
		MinN:          1,
		IncreaseAtPct: 2.0, // very generous so ramp-up could fire — we want to verify it doesn't get suppressed
		StallTicks:    3,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	// Goroutine that simulates ongoing progress: increment completed every tick.
	feedDone := make(chan struct{})
	go func() {
		defer close(feedDone)
		ticker := time.NewTicker(2 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				completed.Add(1)
			}
		}
	}()

	go runRegulator(ctx, &target, ring, &completed, nil, cfg)

	time.Sleep(150 * time.Millisecond)
	cancel()
	<-feedDone

	// With ongoing progress, target should never have dropped from stall —
	// it should have either held or ramped up (depending on baseline+ratios).
	if got := target.Load(); got < 8 {
		t.Errorf("expected target ≥ 8 with ongoing progress (no stall), got %d", got)
	}
}

// minP50Baseline verifies the baseline tracks the smallest non-error p50.
func TestRegulatorBaselineTracksMin(t *testing.T) {
	rb := &regulatorBaseline{}
	rb.observe(50 * time.Millisecond)
	rb.observe(20 * time.Millisecond)
	rb.observe(80 * time.Millisecond)
	rb.observe(15 * time.Millisecond)
	rb.observe(60 * time.Millisecond)

	if got := rb.get(); got != 15*time.Millisecond {
		t.Errorf("baseline should track min p50, got %s", got)
	}
}

// rttRingSummary verifies the ring's p50/p99/error% computation.
func TestRTTRingSummary(t *testing.T) {
	r := newRTTRing(100)
	for i := 1; i <= 100; i++ {
		isErr := i > 95 // 5% errors at the tail
		r.observe(time.Duration(i)*time.Millisecond, isErr)
	}

	p50, p99, errPct := r.summary()
	if p50 != 51*time.Millisecond {
		// median of 1..100 sorted is index 50 → 51ms
		t.Errorf("p50: expected 51ms, got %s", p50)
	}
	if p99 != 100*time.Millisecond {
		t.Errorf("p99: expected 100ms, got %s", p99)
	}
	if errPct != 5.0 {
		t.Errorf("err%%: expected 5.0, got %v", errPct)
	}
}

// rttRingWraparound verifies the ring buffer correctly evicts old entries.
func TestRTTRingWraparound(t *testing.T) {
	r := newRTTRing(4)
	r.observe(1*time.Millisecond, false)
	r.observe(2*time.Millisecond, false)
	r.observe(3*time.Millisecond, false)
	r.observe(4*time.Millisecond, false)
	r.observe(5*time.Millisecond, false) // should evict 1ms

	p50, p99, _ := r.summary()
	// Sorted contents now: [2, 3, 4, 5]ms; p50 at index 2 → 4ms; p99 at last → 5ms
	if p50 != 4*time.Millisecond {
		t.Errorf("p50 after wraparound: expected 4ms, got %s", p50)
	}
	if p99 != 5*time.Millisecond {
		t.Errorf("p99 after wraparound: expected 5ms, got %s", p99)
	}
}
