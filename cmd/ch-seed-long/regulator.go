package main

import (
	"context"
	"log"
	"sync/atomic"
	"time"
)

// regulatorConfig parameterizes the AIMD control loop. Defaults are tuned for
// a typical local bench-stack (low-RTT writes; the dominant signal is tail
// latency rising under sustained load).
type regulatorConfig struct {
	Tick           time.Duration // how often to re-evaluate target (default 2s)
	MaxN           int32         // hard ceiling
	MinN           int32         // hard floor (typically 1)
	HoldBandPct    float64       // p50 tolerance band — within HoldBandPct of baseline = "stable, hold steady"
	IncreaseAtPct  float64       // p50 must be ≤ baseline × IncreaseAtPct to ramp up
	TailDecreasePct float64      // p99 / p50 ratio above this value triggers additive decrease (tail-latency throttling)
	MinSamples     int           // minimum ring observations before the regulator acts
	OnChange       func(oldN, newN int32, reason string) // optional log/metrics hook
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
// AIMD logic per tick:
//   - error_rate > 0%             → multiplicative decrease: N = max(MinN, N/2)
//   - p99/p50 > TailDecreasePct   → additive decrease:        N = max(MinN, N-1)
//   - p50 ≤ baseline×IncreaseAtPct → additive increase:        N = min(MaxN, N+1)
//   - else                         → hold
func runRegulator(ctx context.Context, target *atomic.Int32, ring *rttRing, cfg regulatorConfig, _ *concurrencyLimiter) {
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

	baseline := &regulatorBaseline{}
	ticker := time.NewTicker(cfg.Tick)
	defer ticker.Stop()

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

		oldN := target.Load()
		newN := oldN
		reason := "hold"

		// Decreases (errors, tail-latency) fire without needing a baseline —
		// they react to absolute pressure signals. Ramp-up requires a baseline
		// to decide whether current p50 is "near minimum" (headroom) or not.
		switch {
		case errPct > 0:
			newN = oldN / 2
			if newN < cfg.MinN {
				newN = cfg.MinN
			}
			reason = "errors"
		case p50 > 0 && p99 > 0 && float64(p99)/float64(p50) > cfg.TailDecreasePct:
			newN = oldN - 1
			if newN < cfg.MinN {
				newN = cfg.MinN
			}
			reason = "tail-latency"
		case base > 0 && float64(p50) <= float64(base)*cfg.IncreaseAtPct && oldN < cfg.MaxN:
			newN = oldN + 1
			reason = "ramp-up"
		case base > 0 && float64(p50) > float64(base)*cfg.HoldBandPct:
			reason = "hold-elevated"
		}

		if newN != oldN {
			target.Store(newN)
			if cfg.OnChange != nil {
				cfg.OnChange(oldN, newN, reason)
			} else {
				log.Printf("[seed-long] regulator: %d → %d (reason=%s p50=%dms p99=%dms err%%=%.1f base=%dms)",
					oldN, newN, reason, p50.Milliseconds(), p99.Milliseconds(), errPct, base.Milliseconds())
			}
		}
	}
}
