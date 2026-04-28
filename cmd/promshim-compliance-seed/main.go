package main

import (
	"context"
	"flag"
	"log"
	"math"
	"net/http"
	"sort"
	"time"

	"github.com/prometheus/prometheus/prompb"

	"github.com/BadLiveware/promshim-clickhouse/internal/promharness"
)

type seriesDesc struct {
	labels []prompb.Label
	value  func(ts time.Time, idx int) float64
}

func main() {
	var (
		promEndpoint = flag.String("prom-endpoint", "http://localhost:29090/api/v1/write", "Prometheus remote-write endpoint.")
		chEndpoint   = flag.String("ch-endpoint", "http://default:otel@localhost:29092/write", "ClickHouse remote-write endpoint, optionally with basic-auth userinfo.")
		target       = flag.String("target", "both", "Seed target: both, prom, or ch.")
		endTimeFlag  = flag.String("end-time", "2026-04-21T21:45:42Z", "RFC3339 timestamp of the last fixture sample.")
		duration     = flag.Duration("duration", 2*time.Hour, "Fixture window duration ending at --end-time.")
		step         = flag.Duration("step", 5*time.Second, "Fixture sample interval.")
	)
	flag.Parse()

	if *duration <= 0 {
		log.Fatal("--duration must be > 0")
	}
	if *step <= 0 {
		log.Fatal("--step must be > 0")
	}
	endTime, err := time.Parse(time.RFC3339, *endTimeFlag)
	if err != nil {
		log.Fatalf("parse --end-time: %v", err)
	}
	startTime := endTime.Add(-*duration)
	request := fixtureWriteRequest(startTime, endTime, *step)
	seriesCount := len(request.Timeseries)
	sampleCount := 0
	for _, ts := range request.Timeseries {
		sampleCount += len(ts.Samples)
	}
	log.Printf("compliance fixture window=%s..%s step=%s series=%d samples=%d", startTime.Format(time.RFC3339), endTime.Format(time.RFC3339), *step, seriesCount, sampleCount)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	client := &http.Client{Timeout: 30 * time.Second}

	switch *target {
	case "both":
		write(ctx, client, "prometheus", *promEndpoint, request)
		write(ctx, client, "clickhouse", *chEndpoint, request)
	case "prom":
		write(ctx, client, "prometheus", *promEndpoint, request)
	case "ch":
		write(ctx, client, "clickhouse", *chEndpoint, request)
	default:
		log.Fatalf("--target must be both, prom, or ch (got %q)", *target)
	}
}

func write(ctx context.Context, client *http.Client, name, endpoint string, request *prompb.WriteRequest) {
	if err := promharness.WriteToRemoteWriteEndpoint(ctx, client, endpoint, request); err != nil {
		log.Fatalf("seed %s: %v", name, err)
	}
	log.Printf("seeded %s", name)
}

func fixtureWriteRequest(start, end time.Time, step time.Duration) *prompb.WriteRequest {
	series := buildFixtureSeries(end)
	// Keep samples off exact query-step boundaries. Prometheus range selectors
	// exclude the left boundary, while some ClickHouse shapes use inclusive
	// timestamp predicates; a one-second offset avoids boundary-only diffs.
	sampleStart := start.Add(time.Second)
	out := make([]prompb.TimeSeries, 0, len(series))
	for _, desc := range series {
		samples := make([]prompb.Sample, 0, int(end.Sub(start)/step)+1)
		for ts, idx := sampleStart, 0; !ts.After(end); ts, idx = ts.Add(step), idx+1 {
			value := desc.value(ts, idx)
			if math.IsNaN(value) {
				continue
			}
			samples = append(samples, prompb.Sample{Timestamp: ts.UnixMilli(), Value: value})
		}
		if len(samples) == 0 {
			continue
		}
		out = append(out, prompb.TimeSeries{Labels: desc.labels, Samples: samples})
	}
	return &prompb.WriteRequest{Timeseries: out}
}

func buildFixtureSeries(end time.Time) []seriesDesc {
	instances := []string{"demo.promlabs.com:10000", "demo.promlabs.com:10001", "demo.promlabs.com:10002"}
	memTypes := []string{"buffers", "cached", "free", "used"}
	cpuModes := []string{"idle", "system", "user"}
	bucketLE := []string{"0.0001", "0.00015000000000000001", "0.00022500000000000002", "0.0003375", "0.00050625", "0.000759375", "0.0011390624999999999", "0.0017085937499999998", "0.0025628906249999996", "0.0038443359374999994", "0.00576650390625", "0.008649755859375", "0.0129746337890625", "0.01946195068359375", "0.029192926025390625", "0.04378938903808594", "0.06568408355712891", "0.09852612533569336", "0.14778918800354004", "0.22168378200531006", "0.3325256730079651", "0.49878850951194763", "0.7481827642679214", "1.122274146401882", "1.683411219602823", "+Inf"}
	methods := []string{"GET", "POST", "PUT"}
	paths := []string{"/api/foo", "/api/bar", "/api/nonexistent"}
	statuses := []string{"200", "404", "500"}

	var out []seriesDesc
	add := func(metric string, labels map[string]string, value func(ts time.Time, idx int) float64) {
		full := map[string]string{"__name__": metric}
		for k, v := range labels {
			full[k] = v
		}
		out = append(out, seriesDesc{labels: sortedLabels(full), value: value})
	}

	for instIdx, instance := range instances {
		common := map[string]string{"job": "demo", "instance": instance}
		instanceShift := float64(instIdx) * 10

		add("up", common, constant(1))
		add("demo_num_cpus", common, constant(4))
		add("demo_disk_usage_bytes", common, func(ts time.Time, idx int) float64 {
			return 500_000_000_000 + float64(idx)*4096 + instanceShift*1_000_000
		})
		add("demo_batch_last_success_timestamp_seconds", common, func(ts time.Time, idx int) float64 {
			return float64(ts.Add(-2 * time.Minute).Unix())
		})
		add("demo_intermittent_metric", common, func(ts time.Time, idx int) float64 {
			if ts.After(end.Add(-7 * time.Minute)) {
				return math.NaN()
			}
			if idx%12 >= 6 {
				return math.NaN()
			}
			return 1 + float64(instIdx)
		})

		for typeIdx, typ := range memTypes {
			labels := cloneLabels(common)
			labels["type"] = typ
			base := []float64{240_000_000, 3_100_000_000, 3_750_000_000, 1_550_000_000}[typeIdx]
			amp := []float64{40_000_000, 420_000_000, 260_000_000, 330_000_000}[typeIdx]
			typePhase := float64(typeIdx) * 0.7
			add("demo_memory_usage_bytes", labels, func(ts time.Time, idx int) float64 {
				// Force a periodic exact tie across instances to exercise topk/bottomk
				// tie handling while the rest of the gauge remains varied and large.
				if typ == "buffers" && idx%120 == 119 {
					return 173_015_040
				}
				phase := float64(idx)/3.0 + typePhase + float64(instIdx)*0.4
				return math.Round(base + instanceShift*5_000_000 + amp*(0.5+0.5*math.Sin(phase)))
			})
		}
		for modeIdx, mode := range cpuModes {
			labels := cloneLabels(common)
			labels["mode"] = mode
			modeScale := float64(modeIdx+1) * 0.25
			add("demo_cpu_usage_seconds_total", labels, func(ts time.Time, idx int) float64 {
				// Deterministic counter resets every 20 minutes keep rate/increase/resets
				// coverage meaningful without introducing randomness.
				cycle := idx % 240
				generation := idx / 240
				return 10 + float64(generation)*3 + float64(cycle)*(0.5+modeScale) + instanceShift
			})
		}
		for comboIdx := 0; comboIdx < len(methods)*len(paths); comboIdx++ {
			labels := cloneLabels(common)
			labels["method"] = methods[comboIdx%len(methods)]
			labels["path"] = paths[(comboIdx/len(methods))%len(paths)]
			labels["status"] = statuses[(instIdx+comboIdx)%len(statuses)]
			comboScale := float64(comboIdx + 1)
			add("demo_api_request_duration_seconds_count", labels, func(ts time.Time, idx int) float64 {
				return 100 + float64(idx)*(1+comboScale/10) + instanceShift
			})
			add("demo_api_request_duration_seconds_sum", labels, func(ts time.Time, idx int) float64 {
				return 10 + float64(idx)*(0.001+comboScale/1000) + instanceShift/1000
			})
			for leIdx, le := range bucketLE {
				bucketLabels := cloneLabels(labels)
				bucketLabels["le"] = le
				bucketScale := float64(leIdx+1) * (1 + comboScale/20)
				add("demo_api_request_duration_seconds_bucket", bucketLabels, func(ts time.Time, idx int) float64 {
					return 100 + float64(idx)*bucketScale + instanceShift
				})
			}
		}
	}

	markerLabels := map[string]string{
		"fixture":   "promql-demo",
		"generator": "promshim-compliance-seed",
	}
	add("promshim_compliance_fixture_info", markerLabels, constant(1))
	return out
}

func constant(v float64) func(time.Time, int) float64 {
	return func(time.Time, int) float64 { return v }
}

func cloneLabels(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func sortedLabels(labels map[string]string) []prompb.Label {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]prompb.Label, 0, len(keys))
	for _, k := range keys {
		out = append(out, prompb.Label{Name: k, Value: labels[k]})
	}
	return out
}

func init() {
	log.SetFlags(0)
	log.SetPrefix("[compliance-seed] ")
}
