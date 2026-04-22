package exec

import (
	"math"
	"sort"
	"strconv"
	"strings"

	"ch-observability/internal/promshim/model"
	promlabels "github.com/prometheus/prometheus/model/labels"
)

const histogramSmallDeltaTolerance = 1e-12

type classicHistogramBucket struct {
	UpperBound float64
	Count      float64
}

type classicHistogramGroup struct {
	Metric    map[string]string
	Timestamp float64
	Buckets   map[float64]float64
}

func ApplyHistogramQuantileRuntimeValue(quantile float64, value model.RuntimeValue) (model.RuntimeValue, error) {
	switch typed := value.(type) {
	case model.VectorValue:
		return model.VectorValue{Samples: histogramQuantileInstantSamples(typed.Samples, quantile)}, nil
	case model.MatrixValue:
		return model.MatrixValue{Series: applyClassicHistogramRange(typed.Series, func(samples []model.InstantSample) []model.InstantSample {
			return histogramQuantileInstantSamples(samples, quantile)
		})}, nil
	default:
		return nil, executionf("histogram_quantile requires vector or matrix input, got %T", value)
	}
}

func ApplyHistogramQuantilesRuntimeValue(label string, quantiles []*float64, value model.RuntimeValue) (model.RuntimeValue, error) {
	resolved := make([]float64, 0, len(quantiles))
	for _, quantile := range quantiles {
		if quantile == nil {
			resolved = append(resolved, math.NaN())
			continue
		}
		resolved = append(resolved, *quantile)
	}
	switch typed := value.(type) {
	case model.VectorValue:
		return model.VectorValue{Samples: histogramQuantilesInstantSamples(typed.Samples, label, resolved)}, nil
	case model.MatrixValue:
		return model.MatrixValue{Series: applyClassicHistogramRange(typed.Series, func(samples []model.InstantSample) []model.InstantSample {
			return histogramQuantilesInstantSamples(samples, label, resolved)
		})}, nil
	default:
		return nil, executionf("histogram_quantiles requires vector or matrix input, got %T", value)
	}
}

func ApplyHistogramCountRuntimeValue(value model.RuntimeValue) (model.RuntimeValue, error) {
	switch typed := value.(type) {
	case model.VectorValue:
		return model.VectorValue{Samples: histogramCountInstantSamples(typed.Samples)}, nil
	case model.MatrixValue:
		return model.MatrixValue{Series: applyClassicHistogramRange(typed.Series, histogramCountInstantSamples)}, nil
	default:
		return nil, executionf("histogram_count requires vector or matrix input, got %T", value)
	}
}

func ApplyHistogramFractionRuntimeValue(lower, upper float64, value model.RuntimeValue) (model.RuntimeValue, error) {
	switch typed := value.(type) {
	case model.VectorValue:
		return model.VectorValue{Samples: histogramFractionInstantSamples(typed.Samples, lower, upper)}, nil
	case model.MatrixValue:
		return model.MatrixValue{Series: applyClassicHistogramRange(typed.Series, func(samples []model.InstantSample) []model.InstantSample {
			return histogramFractionInstantSamples(samples, lower, upper)
		})}, nil
	default:
		return nil, executionf("histogram_fraction requires vector or matrix input, got %T", value)
	}
}

func ApplyHistogramSumRuntimeValue(value model.RuntimeValue) (model.RuntimeValue, error) {
	switch typed := value.(type) {
	case model.VectorValue:
		return model.VectorValue{Samples: histogramSumInstantSamples(typed.Samples)}, nil
	case model.MatrixValue:
		return model.MatrixValue{Series: applyClassicHistogramRange(typed.Series, histogramSumInstantSamples)}, nil
	default:
		return nil, executionf("histogram_sum requires vector or matrix input, got %T", value)
	}
}

func ApplyHistogramStdVarRuntimeValue(value model.RuntimeValue) (model.RuntimeValue, error) {
	switch typed := value.(type) {
	case model.VectorValue:
		return model.VectorValue{Samples: histogramStdVarInstantSamples(typed.Samples)}, nil
	case model.MatrixValue:
		return model.MatrixValue{Series: applyClassicHistogramRange(typed.Series, histogramStdVarInstantSamples)}, nil
	default:
		return nil, executionf("histogram_stdvar requires vector or matrix input, got %T", value)
	}
}

func ApplyHistogramStdDevRuntimeValue(value model.RuntimeValue) (model.RuntimeValue, error) {
	switch typed := value.(type) {
	case model.VectorValue:
		return model.VectorValue{Samples: histogramStdDevInstantSamples(typed.Samples)}, nil
	case model.MatrixValue:
		return model.MatrixValue{Series: applyClassicHistogramRange(typed.Series, histogramStdDevInstantSamples)}, nil
	default:
		return nil, executionf("histogram_stddev requires vector or matrix input, got %T", value)
	}
}

func ApplyHistogramAvgRuntimeValue(value model.RuntimeValue) (model.RuntimeValue, error) {
	switch typed := value.(type) {
	case model.VectorValue:
		return model.VectorValue{Samples: histogramAvgInstantSamples(typed.Samples)}, nil
	case model.MatrixValue:
		return model.MatrixValue{Series: applyClassicHistogramRange(typed.Series, histogramAvgInstantSamples)}, nil
	default:
		return nil, executionf("histogram_avg requires vector or matrix input, got %T", value)
	}
}

func applyClassicHistogramRange(series []model.RangeSeries, instantFn func([]model.InstantSample) []model.InstantSample) []model.RangeSeries {
	samplesByTimestamp, timestamps := rangeSeriesSamplesByTimestamp(series)
	grouped := make(map[string]model.RangeSeries, len(series))
	for _, timestamp := range timestamps {
		instant := instantFn(samplesByTimestamp[timestamp])
		for _, sample := range instant {
			key := model.LabelsKey(sample.Metric)
			item := grouped[key]
			if item.Metric == nil {
				item.Metric = model.CloneMetric(sample.Metric)
			}
			item.Values = append(item.Values, model.RangePoint{Timestamp: sample.Timestamp, Value: sample.Value})
			grouped[key] = item
		}
	}
	keys := sortedMapKeys(grouped)
	result := make([]model.RangeSeries, 0, len(keys))
	for _, key := range keys {
		result = append(result, grouped[key])
	}
	return result
}

func histogramQuantileInstantSamples(samples []model.InstantSample, quantile float64) []model.InstantSample {
	return mapClassicHistogramInstant(samples, func(buckets []classicHistogramBucket) float64 {
		return classicBucketQuantile(quantile, buckets)
	})
}

func histogramQuantilesInstantSamples(samples []model.InstantSample, label string, quantiles []float64) []model.InstantSample {
	groups := buildClassicHistogramGroups(samples)
	keys := sortedBucketKeys(groups)
	result := make([]model.InstantSample, 0, len(keys)*len(quantiles))
	for _, key := range keys {
		group := groups[key]
		buckets := sortedClassicHistogramBuckets(group.Buckets)
		for _, quantile := range quantiles {
			metric := model.CloneMetric(group.Metric)
			metric[label] = promlabels.FormatOpenMetricsFloat(quantile)
			result = append(result, model.InstantSample{Metric: metric, Timestamp: group.Timestamp, Value: classicBucketQuantile(quantile, buckets)})
		}
	}
	return sortVectorSamples(result).Samples
}

func histogramCountInstantSamples(samples []model.InstantSample) []model.InstantSample {
	return mapClassicHistogramInstant(samples, classicBucketCount)
}

func histogramFractionInstantSamples(samples []model.InstantSample, lower, upper float64) []model.InstantSample {
	return mapClassicHistogramInstant(samples, func(buckets []classicHistogramBucket) float64 {
		return classicBucketFraction(lower, upper, buckets)
	})
}

func histogramSumInstantSamples(samples []model.InstantSample) []model.InstantSample {
	return mapClassicHistogramInstant(samples, classicBucketSumEstimate)
}

func histogramAvgInstantSamples(samples []model.InstantSample) []model.InstantSample {
	return mapClassicHistogramInstant(samples, classicBucketAvgEstimate)
}

func histogramStdVarInstantSamples(samples []model.InstantSample) []model.InstantSample {
	return mapClassicHistogramInstant(samples, classicBucketStdVarEstimate)
}

func histogramStdDevInstantSamples(samples []model.InstantSample) []model.InstantSample {
	return mapClassicHistogramInstant(samples, classicBucketStdDevEstimate)
}

func mapClassicHistogramInstant(samples []model.InstantSample, valueFn func([]classicHistogramBucket) float64) []model.InstantSample {
	groups := buildClassicHistogramGroups(samples)
	keys := sortedBucketKeys(groups)
	result := make([]model.InstantSample, 0, len(keys))
	for _, key := range keys {
		group := groups[key]
		buckets := sortedClassicHistogramBuckets(group.Buckets)
		result = append(result, model.InstantSample{
			Metric:    model.CloneMetric(group.Metric),
			Timestamp: group.Timestamp,
			Value:     valueFn(buckets),
		})
	}
	return result
}

func buildClassicHistogramGroups(samples []model.InstantSample) map[string]*classicHistogramGroup {
	groups := make(map[string]*classicHistogramGroup, len(samples))
	for _, sample := range samples {
		upperBound, ok := parseClassicHistogramUpperBound(sample.Metric["le"])
		if !ok {
			continue
		}
		metric := histogramResultMetric(sample.Metric)
		key := model.LabelsKey(metric)
		group := groups[key]
		if group == nil {
			group = &classicHistogramGroup{Metric: metric, Timestamp: sample.Timestamp, Buckets: map[float64]float64{}}
			groups[key] = group
		}
		group.Buckets[upperBound] += sample.Value
	}
	return groups
}

func sortedClassicHistogramBuckets(buckets map[float64]float64) []classicHistogramBucket {
	upperBounds := make([]float64, 0, len(buckets))
	for upper := range buckets {
		upperBounds = append(upperBounds, upper)
	}
	sort.Float64s(upperBounds)
	result := make([]classicHistogramBucket, 0, len(upperBounds))
	for _, upper := range upperBounds {
		result = append(result, classicHistogramBucket{UpperBound: upper, Count: buckets[upper]})
	}
	return result
}

func parseClassicHistogramUpperBound(raw string) (float64, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	upper, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(upper) {
		return 0, false
	}
	return upper, true
}

func histogramResultMetric(metric map[string]string) map[string]string {
	result := make(map[string]string, len(metric))
	for label, value := range metric {
		if label == "__name__" || label == "le" {
			continue
		}
		result[label] = value
	}
	return result
}

func classicBucketQuantile(quantile float64, buckets []classicHistogramBucket) float64 {
	switch {
	case math.IsNaN(quantile):
		return math.NaN()
	case quantile < 0:
		return math.Inf(-1)
	case quantile > 1:
		return math.Inf(+1)
	}

	if len(buckets) < 2 || !hasInfUpperBound(buckets) {
		return math.NaN()
	}
	ensureMonotonicClassicBuckets(buckets)

	observations := buckets[len(buckets)-1].Count
	if observations <= 0 || math.IsNaN(observations) {
		return math.NaN()
	}

	rank := quantile * observations
	bucketIndex := sort.Search(len(buckets)-1, func(i int) bool {
		return buckets[i].Count >= rank
	})

	switch {
	case bucketIndex == len(buckets)-1:
		return buckets[len(buckets)-2].UpperBound
	case bucketIndex == 0 && buckets[0].UpperBound <= 0:
		return buckets[0].UpperBound
	default:
		bucketStart := 0.0
		bucketEnd := buckets[bucketIndex].UpperBound
		count := buckets[bucketIndex].Count
		if bucketIndex > 0 {
			bucketStart = buckets[bucketIndex-1].UpperBound
			count -= buckets[bucketIndex-1].Count
			rank -= buckets[bucketIndex-1].Count
		}
		if count <= 0 || math.IsNaN(count) {
			return math.NaN()
		}
		return bucketStart + (bucketEnd-bucketStart)*(rank/count)
	}
}

func classicBucketCount(buckets []classicHistogramBucket) float64 {
	if len(buckets) < 2 || !hasInfUpperBound(buckets) {
		return math.NaN()
	}
	ensureMonotonicClassicBuckets(buckets)
	return buckets[len(buckets)-1].Count
}

func classicBucketFraction(lower, upper float64, buckets []classicHistogramBucket) float64 {
	if len(buckets) < 2 || !hasInfUpperBound(buckets) {
		return math.NaN()
	}
	ensureMonotonicClassicBuckets(buckets)

	count := buckets[len(buckets)-1].Count
	if count == 0 || math.IsNaN(lower) || math.IsNaN(upper) {
		return math.NaN()
	}
	if lower >= upper {
		return 0
	}

	var (
		rank, lowerRank, upperRank float64
		lowerSet, upperSet         bool
	)

	lowerBound := 0.0
	if buckets[0].UpperBound <= 0 {
		lowerBound = math.Inf(-1)
	}

	for i, bucket := range buckets {
		if i > 0 {
			lowerBound = buckets[i-1].UpperBound
		}
		upperBound := bucket.UpperBound

		interpolateLinearly := func(v float64) float64 {
			if lowerBound == math.Inf(-1) {
				return bucket.Count
			}
			return rank + (bucket.Count-rank)*(v-lowerBound)/(upperBound-lowerBound)
		}

		if !lowerSet && lowerBound >= lower {
			lowerRank = rank
			lowerSet = true
		}
		if !upperSet && lowerBound >= upper {
			upperRank = rank
			upperSet = true
		}
		if lowerSet && upperSet {
			break
		}
		if !lowerSet && lowerBound < lower && upperBound > lower {
			lowerRank = interpolateLinearly(lower)
			lowerSet = true
		}
		if !upperSet && lowerBound < upper && upperBound > upper {
			upperRank = interpolateLinearly(upper)
			upperSet = true
		}
		if lowerSet && upperSet {
			break
		}
		rank = bucket.Count
	}
	if !lowerSet || lowerRank > count {
		lowerRank = count
	}
	if !upperSet || upperRank > count {
		upperRank = count
	}
	return (upperRank - lowerRank) / count
}

func classicBucketSumEstimate(buckets []classicHistogramBucket) float64 {
	if len(buckets) < 2 || !hasInfUpperBound(buckets) {
		return math.NaN()
	}
	ensureMonotonicClassicBuckets(buckets)

	total := 0.0
	prevCount := 0.0
	prevUpper := 0.0
	for index, bucket := range buckets {
		count := bucket.Count
		if math.IsNaN(count) {
			return math.NaN()
		}
		delta := count - prevCount
		if delta < 0 {
			delta = 0
		}

		upper := bucket.UpperBound
		lower := prevUpper
		if index == 0 && upper <= 0 {
			lower = upper
		}
		if math.IsInf(upper, +1) {
			upper = prevUpper
		}

		midpoint := lower
		if !math.IsNaN(lower) && !math.IsNaN(upper) && !math.IsInf(lower, 0) && !math.IsInf(upper, 0) {
			midpoint = lower + (upper-lower)/2
		}
		if math.IsNaN(midpoint) || math.IsInf(midpoint, 0) {
			midpoint = 0
		}
		total += delta * midpoint
		prevCount = count
		prevUpper = bucket.UpperBound
	}

	return total
}

func classicBucketStdVarEstimate(buckets []classicHistogramBucket) float64 {
	count := classicBucketCount(buckets)
	if count <= 0 || math.IsNaN(count) {
		return math.NaN()
	}
	mean := classicBucketAvgEstimate(buckets)
	if math.IsNaN(mean) {
		return math.NaN()
	}
	total := 0.0
	prevCount := 0.0
	prevUpper := 0.0
	for index, bucket := range buckets {
		count := bucket.Count
		if math.IsNaN(count) {
			return math.NaN()
		}
		delta := count - prevCount
		if delta < 0 {
			delta = 0
		}

		upper := bucket.UpperBound
		lower := prevUpper
		if index == 0 && upper <= 0 {
			lower = upper
		}
		if math.IsInf(upper, +1) {
			upper = prevUpper
		}

		midpoint := lower
		if !math.IsNaN(lower) && !math.IsNaN(upper) && !math.IsInf(lower, 0) && !math.IsInf(upper, 0) {
			midpoint = lower + (upper-lower)/2
		}
		if math.IsNaN(midpoint) || math.IsInf(midpoint, 0) {
			midpoint = 0
		}
		diff := midpoint - mean
		total += delta * diff * diff
		prevCount = count
		prevUpper = bucket.UpperBound
	}
	return total / count
}

func classicBucketStdDevEstimate(buckets []classicHistogramBucket) float64 {
	variance := classicBucketStdVarEstimate(buckets)
	if math.IsNaN(variance) {
		return variance
	}
	return math.Sqrt(variance)
}

func classicBucketAvgEstimate(buckets []classicHistogramBucket) float64 {
	count := classicBucketCount(buckets)
	if count <= 0 || math.IsNaN(count) {
		return math.NaN()
	}
	sum := classicBucketSumEstimate(buckets)
	if math.IsNaN(sum) {
		return math.NaN()
	}
	return sum / count
}

func hasInfUpperBound(buckets []classicHistogramBucket) bool {
	return len(buckets) > 0 && math.IsInf(buckets[len(buckets)-1].UpperBound, +1)
}

func ensureMonotonicClassicBuckets(buckets []classicHistogramBucket) {
	for i := 1; i < len(buckets); i++ {
		prev := buckets[i-1].Count
		curr := buckets[i].Count
		if math.IsNaN(prev) || math.IsNaN(curr) {
			continue
		}
		if curr >= prev {
			continue
		}
		if almostEqualRelative(curr, prev, histogramSmallDeltaTolerance) {
			buckets[i].Count = prev
			continue
		}
		buckets[i].Count = prev
	}
}

func almostEqualRelative(left, right, tolerance float64) bool {
	delta := math.Abs(left - right)
	if delta == 0 {
		return true
	}
	scale := math.Max(math.Abs(left), math.Abs(right))
	if scale == 0 {
		return true
	}
	return delta/scale <= tolerance
}
