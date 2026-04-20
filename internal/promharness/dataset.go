package promharness

import (
	"fmt"
	"math/rand"
	"sort"
	"time"

	"github.com/prometheus/prometheus/prompb"
)

type generatedDataset struct {
	Request     *prompb.WriteRequest
	SeriesCount int
	SampleCount int
}

func GenerateDataset(cfg SeedConfig) generatedDataset {
	rng := rand.New(rand.NewSource(cfg.Seed))
	builder := newSeriesBuilder()
	jobs := []string{"api", "worker"}
	instances := []string{"a", "b"}
	namespaces := map[string]string{"api": "blue", "worker": "green"}

	for _, job := range jobs {
		for _, instance := range instances {
			common := map[string]string{
				"job":       job,
				"instance":  instance,
				"namespace": namespaces[job],
				"pod":       fmt.Sprintf("%s-%s", job, instance),
				"service":   job,
			}
			counter := 0.0
			for i := 0; i < cfg.Points; i++ {
				timestamp := cfg.BaseTime.Add(time.Duration(i) * cfg.Step)
				upValue := float64((i + len(job) + len(instance) + int(cfg.Seed%3)) % 2)
				queueDepth := float64(10+i*2+len(job)+len(instance)) + rng.Float64()
				counter += float64(1 + ((i + len(job) + len(instance)) % 4))

				builder.AddSample("harness_up", common, timestamp, upValue)
				builder.AddSample("harness_queue_depth", common, timestamp, queueDepth)
				builder.AddSample("harness_requests_total", common, timestamp, counter)
			}
		}
	}

	series := builder.Build()
	sampleCount := 0
	for _, ts := range series {
		sampleCount += len(ts.Samples)
	}
	return generatedDataset{
		Request:     &prompb.WriteRequest{Timeseries: series},
		SeriesCount: len(series),
		SampleCount: sampleCount,
	}
}

type seriesBuilder struct {
	series map[string]*prompb.TimeSeries
}

func newSeriesBuilder() *seriesBuilder {
	return &seriesBuilder{series: map[string]*prompb.TimeSeries{}}
}

func (b *seriesBuilder) AddSample(metric string, labels map[string]string, timestamp time.Time, value float64) {
	fullLabels := make(map[string]string, len(labels)+1)
	fullLabels["__name__"] = metric
	for key, val := range labels {
		fullLabels[key] = val
	}
	key, promLabels := toPromLabels(fullLabels)
	series, ok := b.series[key]
	if !ok {
		series = &prompb.TimeSeries{Labels: promLabels}
		b.series[key] = series
	}
	series.Samples = append(series.Samples, prompb.Sample{Timestamp: timestamp.UnixMilli(), Value: value})
}

func (b *seriesBuilder) Build() []prompb.TimeSeries {
	keys := make([]string, 0, len(b.series))
	for key := range b.series {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]prompb.TimeSeries, 0, len(keys))
	for _, key := range keys {
		result = append(result, *b.series[key])
	}
	return result
}

func toPromLabels(labels map[string]string) (string, []prompb.Label) {
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]prompb.Label, 0, len(keys))
	key := ""
	for _, label := range keys {
		result = append(result, prompb.Label{Name: label, Value: labels[label]})
		key += label + "\xff" + labels[label] + "\xfe"
	}
	return key, result
}
