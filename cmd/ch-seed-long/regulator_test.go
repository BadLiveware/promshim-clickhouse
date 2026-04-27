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
	go runRegulator(ctx, &target, ring, cfg, nil)

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
	go runRegulator(ctx, &target, ring, cfg, nil)

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
	go runRegulator(ctx, &target, ring, cfg, nil)

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

	go runRegulator(ctx, &target, ring, cfg, nil)

	time.Sleep(150 * time.Millisecond)
	cancel()
	<-feedDone

	if got := target.Load(); got > cfg.MaxN {
		t.Errorf("expected target to respect MaxN=%d, got %d", cfg.MaxN, got)
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
