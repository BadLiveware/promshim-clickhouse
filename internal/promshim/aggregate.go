package promshim

import (
	"encoding/json"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/prometheus/promql/parser"
)

type instantSample struct {
	Metric    map[string]string
	Timestamp float64
	Value     float64
}

type rangePoint struct {
	Timestamp float64
	Value     float64
}

type rangeSeries struct {
	Metric map[string]string
	Values []rangePoint
}

type aggregateReducer interface {
	Add(value float64)
	Result() float64
}

type sumReducer struct {
	value float64
}

func (r *sumReducer) Add(value float64) {
	r.value += value
}

func (r *sumReducer) Result() float64 {
	return r.value
}

type countReducer struct {
	count float64
}

func (r *countReducer) Add(_ float64) {
	r.count++
}

func (r *countReducer) Result() float64 {
	return r.count
}

type minReducer struct {
	value    float64
	hasValue bool
}

func (r *minReducer) Add(value float64) {
	if !r.hasValue {
		r.value = value
		r.hasValue = true
		return
	}
	if math.IsNaN(r.value) || (!math.IsNaN(value) && value < r.value) {
		r.value = value
	}
}

func (r *minReducer) Result() float64 {
	return r.value
}

type maxReducer struct {
	value    float64
	hasValue bool
}

func (r *maxReducer) Add(value float64) {
	if !r.hasValue {
		r.value = value
		r.hasValue = true
		return
	}
	if math.IsNaN(r.value) || (!math.IsNaN(value) && value > r.value) {
		r.value = value
	}
}

func (r *maxReducer) Result() float64 {
	return r.value
}

type avgReducer struct {
	sum   float64
	count float64
}

func (r *avgReducer) Add(value float64) {
	r.sum += value
	r.count++
}

func (r *avgReducer) Result() float64 {
	if r.count == 0 {
		return math.NaN()
	}
	return r.sum / r.count
}

func decodeInstantSamples(body io.Reader) ([]instantSample, error) {
	samples := make([]instantSample, 0, 16)
	scanner := newScanner(body)
	for scanner.Scan() {
		var row instantRow
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			return nil, newExecutionErrorf("failed to decode instant row JSON: %v", err)
		}
		timestamp, err := parseClickHouseTimestamp(row.Timestamp)
		if err != nil {
			return nil, newExecutionErrorf("failed to parse instant row timestamp %q: %v", row.Timestamp, err)
		}
		value, err := rawPromValueToFloat64(row.Value)
		if err != nil {
			return nil, newExecutionErrorf("failed to parse instant row value %s: %v", string(row.Value), err)
		}
		samples = append(samples, instantSample{
			Metric:    tagsToObject(row.Tags),
			Timestamp: timestamp,
			Value:     value,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, newExecutionErrorf("failed while scanning instant result rows: %v", err)
	}
	return samples, nil
}

func decodeRangeSeries(body io.Reader) ([]rangeSeries, error) {
	series := make([]rangeSeries, 0, 16)
	scanner := newScanner(body)
	for scanner.Scan() {
		var row matrixRow
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			return nil, newExecutionErrorf("failed to decode range row JSON: %v", err)
		}
		values := make([]rangePoint, 0, len(row.TimeSeries))
		for _, sample := range row.TimeSeries {
			if len(sample) != 2 {
				return nil, newExecutionErrorf("unexpected time_series row shape with %d elements", len(sample))
			}
			var timestampRaw string
			if err := json.Unmarshal(sample[0], &timestampRaw); err != nil {
				return nil, newExecutionErrorf("failed to decode range sample timestamp: %v", err)
			}
			timestamp, err := parseClickHouseTimestamp(timestampRaw)
			if err != nil {
				return nil, newExecutionErrorf("failed to parse range sample timestamp %q: %v", timestampRaw, err)
			}
			value, err := rawPromValueToFloat64(sample[1])
			if err != nil {
				return nil, newExecutionErrorf("failed to parse range sample value %s: %v", string(sample[1]), err)
			}
			values = append(values, rangePoint{Timestamp: timestamp, Value: value})
		}
		series = append(series, rangeSeries{Metric: tagsToObject(row.Tags), Values: values})
	}
	if err := scanner.Err(); err != nil {
		return nil, newExecutionErrorf("failed while scanning range result rows: %v", err)
	}
	return series, nil
}

func rawPromValueToFloat64(raw json.RawMessage) (float64, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return math.NaN(), nil
	}
	if raw[0] == '"' {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return 0, err
		}
		if strings.EqualFold(text, "nan") {
			return math.NaN(), nil
		}
		return strconv.ParseFloat(text, 64)
	}
	var value float64
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, err
	}
	return value, nil
}

func aggregateRuntimeValue(op parser.ItemType, value runtimeValue, grouping []string, without bool, evaluationTime time.Time) (runtimeValue, error) {
	switch typed := value.(type) {
	case vectorValue:
		samples, err := aggregateInstantSamples(op, typed.Samples, grouping, without, evaluationTime)
		if err != nil {
			return nil, err
		}
		return vectorValue{Samples: samples}, nil
	case matrixValue:
		series, err := aggregateRangeSeries(op, typed.Series, grouping, without)
		if err != nil {
			return nil, err
		}
		return matrixValue{Series: series}, nil
	default:
		return nil, newExecutionErrorf("aggregation requires vector or matrix input, got %T", value)
	}
}

func aggregateInstantSamples(op parser.ItemType, samples []instantSample, grouping []string, without bool, evaluationTime time.Time) ([]instantSample, error) {
	timestamp := float64(evaluationTime.UnixNano()) / float64(time.Second)
	type bucket struct {
		Metric  map[string]string
		Reducer aggregateReducer
	}

	buckets := make(map[string]*bucket, len(samples))
	for _, sample := range samples {
		metric := aggregationMetric(sample.Metric, grouping, without)
		key := labelsKey(metric)
		if _, ok := buckets[key]; !ok {
			reducer, err := newAggregateReducer(op)
			if err != nil {
				return nil, err
			}
			buckets[key] = &bucket{Metric: metric, Reducer: reducer}
		}
		buckets[key].Reducer.Add(sample.Value)
	}

	keys := sortedBucketKeys(buckets)
	result := make([]instantSample, 0, len(keys))
	for _, key := range keys {
		bucket := buckets[key]
		result = append(result, instantSample{
			Metric:    bucket.Metric,
			Timestamp: timestamp,
			Value:     bucket.Reducer.Result(),
		})
	}
	return result, nil
}

func aggregateRangeSeries(op parser.ItemType, series []rangeSeries, grouping []string, without bool) ([]rangeSeries, error) {
	type bucket struct {
		Metric map[string]string
		Values map[float64]aggregateReducer
	}

	buckets := make(map[string]*bucket, len(series))
	for _, input := range series {
		metric := aggregationMetric(input.Metric, grouping, without)
		key := labelsKey(metric)
		if _, ok := buckets[key]; !ok {
			buckets[key] = &bucket{Metric: metric, Values: map[float64]aggregateReducer{}}
		}
		for _, point := range input.Values {
			if _, ok := buckets[key].Values[point.Timestamp]; !ok {
				reducer, err := newAggregateReducer(op)
				if err != nil {
					return nil, err
				}
				buckets[key].Values[point.Timestamp] = reducer
			}
			buckets[key].Values[point.Timestamp].Add(point.Value)
		}
	}

	keys := sortedBucketKeys(buckets)
	result := make([]rangeSeries, 0, len(keys))
	for _, key := range keys {
		bucket := buckets[key]
		timestamps := make([]float64, 0, len(bucket.Values))
		for timestamp := range bucket.Values {
			timestamps = append(timestamps, timestamp)
		}
		sort.Float64s(timestamps)
		values := make([]rangePoint, 0, len(timestamps))
		for _, timestamp := range timestamps {
			values = append(values, rangePoint{Timestamp: timestamp, Value: bucket.Values[timestamp].Result()})
		}
		result = append(result, rangeSeries{Metric: bucket.Metric, Values: values})
	}
	return result, nil
}

func newAggregateReducer(op parser.ItemType) (aggregateReducer, error) {
	switch op {
	case parser.SUM:
		return &sumReducer{}, nil
	case parser.COUNT:
		return &countReducer{}, nil
	case parser.MIN:
		return &minReducer{}, nil
	case parser.MAX:
		return &maxReducer{}, nil
	case parser.AVG:
		return &avgReducer{}, nil
	default:
		return nil, newUnsupportedErrorf("aggregation reducer for operator %q is not implemented yet", op.String())
	}
}

func formatPromValue(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func sortedBucketKeys[T any](buckets map[string]*T) []string {
	keys := make([]string, 0, len(buckets))
	for key := range buckets {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
