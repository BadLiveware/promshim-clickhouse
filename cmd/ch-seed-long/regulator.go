package main

import (
	"context"
	"fmt"
	"log"
	"sync/atomic"
	"time"
)

// regulatorConfig parameterizes the AIMD control loop. Defaults are tuned for
// a typical local bench-stack (low-RTT writes; the dominant signal is tail
// latency rising under sustained load).
type regulatorConfig struct {
	Tick            time.Duration                         // how often to re-evaluate target (default 2s)
	MaxN            int32                                 // hard ceiling
	MinN            int32                                 // hard floor (typically 1)
	HoldBandPct     float64                               // p50 tolerance band — within HoldBandPct of baseline = "stable, hold steady"
	IncreaseAtPct   float64                               // p50 must be ≤ baseline × IncreaseAtPct to ramp up
	TailDecreasePct float64                               // p99 / p50 ratio above this value triggers additive decrease (tail-latency throttling)
	MinSamples      int                                   // minimum ring observations before the regulator acts
	StallTicks      int                                   // consecutive zero-completion ticks before stall throttling fires (default 3 = 6s at 2s tick)
	HoldLogEvery    int                                   // log a "hold" decision every N ticks even when target unchanged (default 10 ≈ 20s); 0 disables
	OnChange        func(oldN, newN int32, reason string) // optional log/metrics hook
}

// defaultRegulatorConfig is what the seeder uses when no overrides are
// provided. Hand-tuned values that are conservative-leaning: ramp up only
// when latency is essentially at its floor, throttle aggressively on errors.
func defaultRegulatorConfig(maxN int32) regulatorConfig {
	return regulatorConfig{
		Tick:            2 * time.Second,
		MaxN:            maxN,
		MinN:            1,
		HoldBandPct:     1.5,
		IncreaseAtPct:   1.2,
		TailDecreasePct: 3.0,
		MinSamples:      8,
		StallTicks:      3,  // 3 × 2s = 6s of zero completions triggers throttle
		HoldLogEvery:    10, // 10 × 2s = 20s between "still here, holding" logs
	}
}

// regulatorBaseline tracks the smallest non-error p50 ever observed. The
// regulator uses this as the ramp-up reference point: while current p50 is
// near baseline, we have headroom; when it rises above baseline × bands,
// the system is congested.
type regulatorBaseline struct {
	minP50 atomic.Int64 // nanoseconds; 0 means uninitialized
}

func (rb *regulatorBaseline) observe(p50 time.Duration) {
	cur := rb.minP50.Load()
	d := int64(p50)
	if d <= 0 {
		return
	}
	for cur == 0 || d < cur {
		if rb.minP50.CompareAndSwap(cur, d) {
			return
		}
		cur = rb.minP50.Load()
	}
}

func (rb *regulatorBaseline) get() time.Duration {
	return time.Duration(rb.minP50.Load())
}

// runRegulator drives the AIMD control loop. It blocks until ctx is
// cancelled. Workers should already be running when this is invoked.
//
// AIMD logic per tick (in priority order):
//   - error_rate > 0%             → multiplicative decrease: N = max(MinN, N/2)
//   - stall (no completions for StallTicks ticks while N > MinN)
//     → additive decrease:        N = max(MinN, N-1)
//   - p99/p50 > TailDecreasePct   → additive decrease:        N = max(MinN, N-1)
//   - p50 ≤ baseline×IncreaseAtPct → additive increase:        N = min(MaxN, N+1)
//   - else                         → hold
//
// The stall branch exists because the RTT ring only updates on completed
// batches. When workers are all stuck mid-POST waiting on a slow backend,
// no observations land in the ring and the tail-latency throttle can't
// fire. Tracking the batch-count delta per tick catches this directly.
//
// The completedBatches counter is the same atomic.Int64 that the worker
// pool updates on each successful POST. Passing it lets the regulator see
// throughput trends without coupling to the worker code.
func runRegulator(ctx context.Context, target *atomic.Int32, ring *rttRing, completedBatches *atomic.Int64, cfg regulatorConfig, _ *concurrencyLimiter) {
	if cfg.Tick <= 0 {
		cfg.Tick = 2 * time.Second
	}
	if cfg.MaxN < 1 {
		cfg.MaxN = 1
	}
	if cfg.MinN < 1 {
		cfg.MinN = 1
	}
	if cfg.HoldBandPct < 1 {
		cfg.HoldBandPct = 1.5
	}
	if cfg.IncreaseAtPct < 1 {
		cfg.IncreaseAtPct = 1.2
	}
	if cfg.TailDecreasePct < 1 {
		cfg.TailDecreasePct = 3.0
	}
	if cfg.MinSamples < 1 {
		cfg.MinSamples = 8
	}
	if cfg.StallTicks < 0 {
		cfg.StallTicks = 0 // 0 disables stall detection
	}

	baseline := &regulatorBaseline{}
	ticker := time.NewTicker(cfg.Tick)
	defer ticker.Stop()

	// Stall detection state: track batch-completion count per tick so we
	// can spot "all workers in flight, none completing" — invisible to the
	// RTT ring (which only updates on completion).
	//
	// rampSuppressUntil prevents ramp-up immediately undoing a stall
	// throttle. Without it, the regulator oscillates: stall halves target,
	// ramp-up restores it, stall halves again — pointless churn that
	// doesn't actually relieve backend pressure.
	var (
		lastCompleted     int64
		stallStreak       int
		holdStreak        int
		rampSuppressUntil time.Time
	)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		p50, p99, errPct := ring.summary()
		// Skip until ring has accumulated enough observations to be meaningful.
		// The ring summary returns (0, 0, 0) for an empty ring.
		if p50 == 0 {
			continue
		}

		// Baseline tracks the smallest p50 we've ever seen on a no-error tick.
		// Refining downward as latency improves keeps the regulator honest about
		// true minimum cost and prevents it from "settling" into a degraded state.
		// Only update on no-error ticks: error-tainted p50s can be misleading.
		if errPct == 0 {
			baseline.observe(p50)
		}
		base := baseline.get()

		// Stall detection: track batch-completion delta. If no batches have
		// completed for StallTicks consecutive ticks while target > MinN,
		// the workers are all stuck on slow POSTs and the RTT ring is
		// uninformative. This is the catch-all signal for the "tail latency
		// is huge but no observation has landed yet" failure mode.
		var stallActive bool
		if completedBatches != nil && cfg.StallTicks > 0 {
			cur := completedBatches.Load()
			if cur == lastCompleted {
				stallStreak++
			} else {
				stallStreak = 0
			}
			lastCompleted = cur
			stallActive = stallStreak >= cfg.StallTicks
		}

		oldN := target.Load()
		newN := oldN
		reason := "hold"

		// Decreases (errors, stall, tail-latency) fire without needing a
		// baseline — they react to absolute pressure signals. Ramp-up
		// requires a baseline to decide whether current p50 is near minimum.
		switch {
		case errPct > 0:
			newN = oldN / 2
			if newN < cfg.MinN {
				newN = cfg.MinN
			}
			reason = "errors"
		case stallActive && oldN > cfg.MinN:
			// Multiplicative — stall is a strong signal that backend has
			// fully given up. Additive would let ramp-up oscillate it back.
			newN = oldN / 2
			if newN < cfg.MinN {
				newN = cfg.MinN
			}
			reason = fmt.Sprintf("stall (%d ticks no completions)", stallStreak)
			stallStreak = 0
			// Suppress ramp-up for a few ticks so the throttle has time to
			// actually relieve pressure before the regulator re-evaluates.
			rampSuppressUntil = time.Now().Add(5 * cfg.Tick)
		case p50 > 0 && p99 > 0 && float64(p99)/float64(p50) > cfg.TailDecreasePct:
			newN = oldN - 1
			if newN < cfg.MinN {
				newN = cfg.MinN
			}
			reason = "tail-latency"
		case base > 0 && float64(p50) <= float64(base)*cfg.IncreaseAtPct && oldN < cfg.MaxN && time.Now().After(rampSuppressUntil):
			newN = oldN + 1
			reason = "ramp-up"
		case base > 0 && float64(p50) > float64(base)*cfg.HoldBandPct:
			reason = "hold-elevated"
		}

		if newN != oldN {
			target.Store(newN)
			holdStreak = 0
			if cfg.OnChange != nil {
				cfg.OnChange(oldN, newN, reason)
			} else {
				log.Printf("[seed-long] regulator: %d → %d (reason=%s p50=%dms p99=%dms err%%=%.1f base=%dms)",
					oldN, newN, reason, p50.Milliseconds(), p99.Milliseconds(), errPct, base.Milliseconds())
			}
			continue
		}

		// Target unchanged. Surface long-running "hold" decisions periodically
		// so the user can see the regulator is alive and what it's seeing —
		// otherwise a 100-second stall window goes silent in the log.
		holdStreak++
		if cfg.HoldLogEvery > 0 && holdStreak%cfg.HoldLogEvery == 0 {
			log.Printf("[seed-long] regulator: holding at %d (reason=%s p50=%dms p99=%dms err%%=%.1f base=%dms %d ticks)",
				oldN, reason, p50.Milliseconds(), p99.Milliseconds(), errPct, base.Milliseconds(), holdStreak)
		}
	}
}
