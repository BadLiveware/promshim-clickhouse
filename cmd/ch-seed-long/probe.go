package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/prometheus/common/expfmt"
)

// probeConfig parameterizes the kill-switch health probes.
type probeConfig struct {
	Interval        time.Duration                         // poll cadence (default 5s)
	CHURL           string                                // ClickHouse HTTP URL; empty disables CH probes
	CHUsername      string                                // basic auth user for CH (defaults to "default")
	CHPassword      string                                // basic auth password for CH
	PromURL         string                                // Prometheus HTTP URL; empty disables Prom probes
	MaxActiveParts  int                                   // CH parts threshold; 0 disables
	MaxMergeBacklog int64                                 // CH BackgroundPoolTask threshold; 0 disables
	MaxHostCPUPct   float64                               // throttle when host CPU exceeds this percent (0 disables)
	ThrottleDivisor int32                                 // multiplicative decrease factor on kill-switch fire (default 4)
	ThrottleHoldOff time.Duration                         // pause regulator ramp-up for this long after a fire (default 10s)
	OnFire          func(reason string, oldN, newN int32) // optional log/metrics hook
}

func defaultProbeConfig(chURL, chUser, chPass, promURL string, interval time.Duration, hostLoadPct float64) probeConfig {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return probeConfig{
		Interval:        interval,
		CHURL:           chURL,
		CHUsername:      chUser,
		CHPassword:      chPass,
		PromURL:         promURL,
		MaxActiveParts:  300,
		MaxMergeBacklog: 50,
		MaxHostCPUPct:   hostLoadPct, // already a percentage (50 = 50% busy)
		ThrottleDivisor: 4,
		ThrottleHoldOff: 10 * time.Second,
	}
}

// runHealthProbe periodically queries CH and Prom for explicit pressure
// signals. When any signal trips, it: (a) divides target by ThrottleDivisor
// (multiplicative decrease, harsher than RTT-based AIMD), and (b) writes a
// future ramp-up-freeze deadline into rampUpFreeze (Unix-nanos) so the
// regulator's ramp-up branch is suppressed for ThrottleHoldOff after a fire.
func runHealthProbe(ctx context.Context, target *atomic.Int32, rampUpFreeze *atomic.Int64, cfg probeConfig) {
	if cfg.Interval <= 0 {
		return // disabled
	}
	if cfg.ThrottleDivisor < 2 {
		cfg.ThrottleDivisor = 4
	}
	if cfg.ThrottleHoldOff <= 0 {
		cfg.ThrottleHoldOff = 10 * time.Second
	}

	client := &http.Client{Timeout: 3 * time.Second}
	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()

	// CPU sampler tracks the previous /proc/stat snapshot so each tick can
	// compute an instantaneous percentage by diff. Initialize lazily.
	var lastCPU cpuSnapshot

	fire := func(reason string) {
		oldN := target.Load()
		newN := oldN / cfg.ThrottleDivisor
		if newN < 1 {
			newN = 1
		}
		if newN < oldN {
			target.Store(newN)
			suppressRampUntil(rampUpFreeze, time.Now().Add(cfg.ThrottleHoldOff))
			if cfg.OnFire != nil {
				cfg.OnFire(reason, oldN, newN)
			} else {
				log.Printf("[seed-long] kill-switch: %s — concurrency %d → %d, freeze ramp-up for %s",
					reason, oldN, newN, cfg.ThrottleHoldOff)
			}
		}
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		// Host-CPU probe — universally applicable. Samples /proc/stat for
		// instantaneous CPU% (diff against previous tick), fires when it
		// exceeds MaxHostCPUPct. Reactive within one probe interval (~5s);
		// load average would lag 30+ seconds for the same step change, so
		// for sub-3-minute seed runs the load1 signal would arrive after
		// most of the damage was done.
		if cfg.MaxHostCPUPct > 0 {
			cur, err := readCPUSnapshot()
			if err == nil {
				if lastCPU.total > 0 { // need a previous sample to compute delta
					if pct := cpuBusyPct(lastCPU, cur); pct > cfg.MaxHostCPUPct {
						fire(fmt.Sprintf("host CPU %.1f%% > %.1f%%", pct, cfg.MaxHostCPUPct))
					}
				}
				lastCPU = cur
			}
		}

		// CH probes — only fire if CHURL is configured
		if cfg.CHURL != "" {
			if cfg.MaxActiveParts > 0 {
				if parts, err := chQueryInt(ctx, client, cfg.CHURL, cfg.CHUsername, cfg.CHPassword,
					"SELECT count() FROM system.parts WHERE active AND table LIKE '.inner_id.data.%'"); err == nil {
					if int(parts) > cfg.MaxActiveParts {
						fire(fmt.Sprintf("CH active parts %d > %d", parts, cfg.MaxActiveParts))
					}
				}
			}
			if cfg.MaxMergeBacklog > 0 {
				if backlog, err := chQueryInt(ctx, client, cfg.CHURL, cfg.CHUsername, cfg.CHPassword,
					"SELECT value FROM system.metrics WHERE name = 'BackgroundMergesAndMutationsPoolTask'"); err == nil {
					if backlog > cfg.MaxMergeBacklog {
						fire(fmt.Sprintf("CH merge backlog %d > %d", backlog, cfg.MaxMergeBacklog))
					}
				}
			}
		}

		// Prom probe: scrape its /metrics for the active-appenders gauge.
		// A rising count means writes are queueing on the head side.
		// Threshold here is intentionally loose; tighten if it produces noise.
		if cfg.PromURL != "" {
			if active, err := promScrapeMetric(ctx, client, cfg.PromURL,
				"prometheus_tsdb_head_active_appenders"); err == nil {
				if active > 100 {
					fire(fmt.Sprintf("Prom active appenders %.0f > 100", active))
				}
			}
		}
	}
}

// cpuSnapshot is a point-in-time view of /proc/stat's first "cpu" line —
// the aggregate across all cores. Computing CPU% requires diffing two
// snapshots: busy delta divided by total delta over the same interval.
type cpuSnapshot struct {
	idle  uint64 // idle + iowait jiffies
	total uint64 // all jiffies (user + nice + system + idle + iowait + irq + softirq + steal + ...)
}

// readCPUSnapshot reads /proc/stat's first line and parses it. Returns an
// error on systems without /proc (macOS) or if the file format is unexpected.
// The caller treats an error as "host CPU probing unavailable" and skips.
func readCPUSnapshot() (cpuSnapshot, error) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return cpuSnapshot{}, err
	}
	// First line: "cpu  user nice system idle iowait irq softirq steal guest guest_nice"
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 5 {
			return cpuSnapshot{}, fmt.Errorf("/proc/stat cpu line too short: %q", line)
		}
		var snap cpuSnapshot
		for i, f := range fields[1:] {
			v, err := strconv.ParseUint(f, 10, 64)
			if err != nil {
				return cpuSnapshot{}, fmt.Errorf("/proc/stat field %d (%q): %w", i+1, f, err)
			}
			snap.total += v
			// idle (i=3) and iowait (i=4) are the "not busy" buckets
			if i == 3 || i == 4 {
				snap.idle += v
			}
		}
		return snap, nil
	}
	return cpuSnapshot{}, fmt.Errorf("/proc/stat has no cpu line")
}

// cpuBusyPct computes percent-busy across all cores between two snapshots.
// Returns 0 if the deltas are degenerate (e.g., same snapshot read twice).
func cpuBusyPct(prev, cur cpuSnapshot) float64 {
	totalDelta := cur.total - prev.total
	idleDelta := cur.idle - prev.idle
	if totalDelta == 0 {
		return 0
	}
	busy := totalDelta - idleDelta
	return 100.0 * float64(busy) / float64(totalDelta)
}

// chQueryInt executes a single-cell SELECT against ClickHouse over HTTP and
// parses the result as an int64. Returns an error on any HTTP/parse failure.
func chQueryInt(ctx context.Context, client *http.Client, baseURL, user, pass, query string) (int64, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return 0, err
	}
	q := u.Query()
	q.Set("query", query)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return 0, err
	}
	if user != "" {
		req.SetBasicAuth(user, pass)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}
	if resp.StatusCode/100 != 2 {
		return 0, fmt.Errorf("CH probe %s: %s", query, resp.Status)
	}
	return strconv.ParseInt(strings.TrimSpace(string(body)), 10, 64)
}

// promScrapeMetric pulls one named metric from a Prometheus /metrics endpoint
// using the canonical text-format parser. Returns the first sample value.
//
// Uses expfmt rather than line-based parsing so that quirks like optional
// timestamp suffixes, labels with spaces, and metric-name prefix collisions
// (e.g. "_total" suffixes) don't produce wrong values silently.
func promScrapeMetric(ctx context.Context, client *http.Client, baseURL, metric string) (float64, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return 0, err
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/metrics"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return 0, fmt.Errorf("prom scrape: %s", resp.Status)
	}

	families, err := (&expfmt.TextParser{}).TextToMetricFamilies(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("parse /metrics: %w", err)
	}
	family, ok := families[metric]
	if !ok || len(family.Metric) == 0 {
		return 0, fmt.Errorf("metric %q not found in /metrics output", metric)
	}
	m := family.Metric[0]
	switch {
	case m.Gauge != nil:
		return m.Gauge.GetValue(), nil
	case m.Counter != nil:
		return m.Counter.GetValue(), nil
	case m.Untyped != nil:
		return m.Untyped.GetValue(), nil
	default:
		return 0, fmt.Errorf("metric %q has no scalar value (type=%s)", metric, family.GetType())
	}
}
