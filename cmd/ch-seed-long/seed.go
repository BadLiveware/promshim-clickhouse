package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/prometheus/prompb"

	"github.com/BadLiveware/promshim-clickhouse/internal/promharness"
)

// streamConfig configures a parallel seeding run. Behavior is deterministic
// across MaxConcurrency values for the SAMPLES emitted (the generator runs
// single-threaded and advances series state in time order); only the order in
// which batches reach the endpoint can vary, which the remote-write protocol
// is indifferent to.
type streamConfig struct {
	Endpoint        string
	StartTime       time.Time
	EndTime         time.Time
	Step            time.Duration
	BatchSamples    int
	Series          []seriesDesc
	State           []seriesState

	// MaxConcurrency caps the number of in-flight remote-write POSTs.
	// When the regulator is disabled (NoAdaptive=true), this is the fixed
	// number of worker goroutines used.
	MaxConcurrency int

	// InitialConcurrency is the starting target for the regulator. Ignored
	// when NoAdaptive is true (workers always run at MaxConcurrency).
	InitialConcurrency int

	// NoAdaptive disables the regulator. Workers always run at MaxConcurrency
	// for deterministic behavior — useful when benchmarking the seeder itself.
	NoAdaptive bool

	// ProbeInterval is how often the health-probe goroutine polls
	// ClickHouse and Prometheus. Zero or negative disables the probe.
	ProbeInterval time.Duration

	// CHURL is the ClickHouse HTTP endpoint (port 28124-style) used by the
	// health probe to query system.metrics. Empty disables CH probing.
	CHURL      string
	CHUsername string
	CHPassword string

	// PromURL is the Prometheus HTTP endpoint used by the health probe.
	// Empty disables Prom probing.
	PromURL string

	// MaxHostLoadPct is the percentage-of-NumCPU threshold for the host
	// /proc/loadavg 1-min average. Default 50 (safe for shared dev hosts);
	// 80–90 for CI / dedicated bench. 0 disables host-load probing.
	MaxHostLoadPct float64
}

// streamStats holds end-of-run aggregate counters reported back to main.
type streamStats struct {
	Batches       int
	Samples       int
	Errors        int
	Wallclock     time.Duration
	MaxObservedN  int
	MinObservedN  int
}

// runStream executes the parallel seeding pipeline. It blocks until either:
//   * the entire time window has been emitted and all in-flight workers drain,
//   * the context is cancelled,
//   * a fatal error occurs (any single batch error is fatal — Prometheus
//     remote-write protocol does not define a partial-success response, so
//     we do not retry; instead we surface the error so the seeder fails fast).
func runStream(ctx context.Context, cfg streamConfig) (streamStats, error) {
	if cfg.MaxConcurrency < 1 {
		return streamStats{}, errors.New("MaxConcurrency must be >= 1")
	}
	if cfg.InitialConcurrency < 1 {
		cfg.InitialConcurrency = cfg.MaxConcurrency
	}
	if cfg.InitialConcurrency > cfg.MaxConcurrency {
		cfg.InitialConcurrency = cfg.MaxConcurrency
	}

	pointsPerBatch := cfg.BatchSamples / len(cfg.Series)
	if pointsPerBatch < 1 {
		pointsPerBatch = 1
	}

	// Total expected samples: every series emits one sample per step over the
	// full window. Used by the progress log for % done and ETA. Computed once
	// up front and passed through atomic load (cheap).
	totalSteps := int64(cfg.EndTime.Sub(cfg.StartTime) / cfg.Step)
	expectedSamples := totalSteps * int64(len(cfg.Series))

	// Buffered to 2× MaxConcurrency so the generator can stay one batch ahead
	// of the slowest worker without racing arbitrarily ahead and pre-encoding
	// dozens of batches that the regulator would prefer to suppress.
	type batch struct {
		req     *prompb.WriteRequest
		samples int
	}
	requests := make(chan batch, 2*cfg.MaxConcurrency)

	// Concurrency target — phase 2 regulator will mutate this. Phase 1 leaves
	// it pinned at MaxConcurrency.
	target := atomic.Int32{}
	if cfg.NoAdaptive {
		target.Store(int32(cfg.MaxConcurrency))
	} else {
		target.Store(int32(cfg.InitialConcurrency))
	}

	rtt := newRTTRing(256)
	var (
		batchCount      atomic.Int64
		sampleCount     atomic.Int64 // samples acked by CH/Prom (post-POST)
		samplesEmitted  atomic.Int64 // samples produced by the generator
		errCount        atomic.Int64
		maxN            atomic.Int32
		minN            atomic.Int32
	)
	maxN.Store(target.Load())
	minN.Store(target.Load())

	// Workers gate on the limiter BEFORE dequeuing. Doing it after
	// dequeuing strands batches in over-target workers when the regulator
	// throttles; doing it before means at-most `target` workers are
	// concurrently dequeue+POSTing. The `draining` flag releases parked
	// workers when the generator closes the channel.
	draining := atomic.Bool{}
	limiter := newConcurrencyLimiter(&target, &draining)

	start := time.Now()

	// Generator goroutine: produces *prompb.WriteRequest values into the
	// buffered channel. Owns all series state. Closes the channel on EOF
	// or context cancellation. Sets draining=true before closing so any
	// parked workers can exit cleanly.
	var generatorErr atomic.Pointer[error]
	go func() {
		defer func() {
			draining.Store(true)
			close(requests)
		}()
		batchStart := cfg.StartTime
		for batchStart.Before(cfg.EndTime) {
			select {
			case <-ctx.Done():
				return
			default:
			}
			batchEnd := batchStart.Add(time.Duration(pointsPerBatch) * cfg.Step)
			if batchEnd.After(cfg.EndTime) {
				batchEnd = cfg.EndTime
			}
			req := &prompb.WriteRequest{}
			batchSamples := 0
			for i := range cfg.Series {
				points := advanceSeries(&cfg.Series[i], &cfg.State[i], batchStart, batchEnd, cfg.Step)
				if len(points) == 0 {
					continue
				}
				req.Timeseries = append(req.Timeseries, prompb.TimeSeries{
					Labels:  cfg.Series[i].labels,
					Samples: points,
				})
				batchSamples += len(points)
			}
			if len(req.Timeseries) == 0 {
				batchStart = batchEnd
				continue
			}
			samplesEmitted.Add(int64(batchSamples))
			select {
			case <-ctx.Done():
				return
			case requests <- batch{req: req, samples: batchSamples}:
			}
			batchStart = batchEnd
		}
	}()

	var workerWG sync.WaitGroup
	workerWG.Add(cfg.MaxConcurrency)
	for w := 0; w < cfg.MaxConcurrency; w++ {
		workerID := w
		go func() {
			defer workerWG.Done()
			// Each worker owns its own http.Client → own TCP connection.
			// MaxIdleConnsPerHost=1 + Timeout to bound long stalls.
			client := &http.Client{
				Timeout: 5 * time.Minute,
				Transport: &http.Transport{
					MaxIdleConnsPerHost: 1,
					IdleConnTimeout:     2 * time.Minute,
					DisableCompression:  true, // payload is already snappy-encoded
				},
			}
			for {
				// Gate on the limiter BEFORE dequeuing, so over-target workers
				// don't strand batches in their local b variable while parked.
				if !limiter.waitForSlot(ctx, workerID) {
					return // ctx cancelled OR generator drained
				}
				b, ok := <-requests
				if !ok {
					return // channel closed and drained
				}
				postStart := time.Now()
				err := promharness.WriteToRemoteWriteEndpoint(ctx, client, cfg.Endpoint, b.req)
				elapsed := time.Since(postStart)
				if err != nil {
					errCount.Add(1)
					rtt.observe(elapsed, true)
					if isFatal(err) {
						stash := err
						generatorErr.CompareAndSwap(nil, &stash)
						return
					}
					continue
				}
				rtt.observe(elapsed, false)
				batchCount.Add(1)
				sampleCount.Add(int64(b.samples))
			}
		}()
	}

	// Adaptive regulator. Disabled when --no-adaptive is set; in that mode the
	// target stays pinned at MaxConcurrency and runRegulator is not started.
	rampUpFreeze := atomic.Int64{}
	if !cfg.NoAdaptive {
		regCtx, regCancel := context.WithCancel(ctx)
		defer regCancel()
		go runRegulator(regCtx, &target, rtt, &batchCount, defaultRegulatorConfig(int32(cfg.MaxConcurrency)), limiter)

		// Health probe: runs whenever ProbeInterval > 0. Always checks host
		// CPU usage (which doesn't depend on backend URLs); also checks CH and
		// Prom signals if their URLs are configured. The host-CPU check is
		// what catches local-machine saturation when seeder + backend share a
		// host — the per-batch RTT regulator can't see that directly.
		if cfg.ProbeInterval > 0 {
			go runHealthProbe(regCtx, &target, &rampUpFreeze,
				defaultProbeConfig(cfg.CHURL, cfg.CHUsername, cfg.CHPassword, cfg.PromURL,
					cfg.ProbeInterval, cfg.MaxHostLoadPct),
				limiter)
		}
	}

	// Periodic progress log so users can see throughput trending without
	// waiting for the run to finish.
	progressDone := make(chan struct{})
	go func() {
		defer close(progressDone)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		var lastSamples int64
		var lastTime time.Time = start
		for {
			select {
			case <-ctx.Done():
				return
			case t := <-ticker.C:
				cur := sampleCount.Load()
				emitted := samplesEmitted.Load()
				delta := cur - lastSamples
				dur := t.Sub(lastTime).Seconds()
				if dur <= 0 {
					continue
				}
				rate := float64(delta) / dur
				p50, p99, errPct := rtt.summary()

				// % done + ETA. Use acked (not emitted) so the percentage
				// reflects samples that are actually persisted, not just
				// pre-encoded by the generator. ETA falls back to "—" when
				// rate is zero (e.g. probe just throttled to 0 momentarily)
				// to avoid spurious "ETA=∞" display.
				var pctDone float64
				if expectedSamples > 0 {
					pctDone = 100.0 * float64(cur) / float64(expectedSamples)
				}
				eta := "—"
				if rate > 0 && cur < expectedSamples {
					remaining := expectedSamples - cur
					etaSec := float64(remaining) / rate
					eta = (time.Duration(etaSec) * time.Second).Truncate(time.Second).String()
				}

				log.Printf("[seed-long] progress: %.1f%%, batches=%d acked=%d/%d emitted=%d (+%dk acked last %.0fs = %.0fk/s) errors=%d rtt p50=%dms p99=%dms err%%=%.1f targetN=%d ETA=%s",
					pctDone, batchCount.Load(), cur, expectedSamples, emitted, delta/1000, dur, rate/1000, errCount.Load(),
					p50.Milliseconds(), p99.Milliseconds(), errPct, target.Load(), eta)
				lastSamples = cur
				lastTime = t
				if n := target.Load(); n > maxN.Load() {
					maxN.Store(n)
				}
				if n := target.Load(); n < minN.Load() {
					minN.Store(n)
				}
			}
		}
	}()

	// Wait for all workers to drain.
	workerWG.Wait()
	if errp := generatorErr.Load(); errp != nil {
		return streamStats{}, fmt.Errorf("seed batch failed: %w", *errp)
	}

	stats := streamStats{
		Batches:      int(batchCount.Load()),
		Samples:      int(sampleCount.Load()),
		Errors:       int(errCount.Load()),
		Wallclock:    time.Since(start),
		MaxObservedN: int(maxN.Load()),
		MinObservedN: int(minN.Load()),
	}
	return stats, nil
}

// isFatal reports whether a remote-write error should abort the entire seed
// run. Currently we treat all errors as fatal because Prometheus remote-write
// is at-most-once-per-batch (no idempotency keys, no partial replay), so a
// retry that succeeds after a partial-write would risk silent duplicate
// samples. Future work could differentiate transient (5xx) from permanent
// errors and retry transient ones with the same payload.
func isFatal(err error) bool {
	return err != nil
}
