package exec

import (
	"math"
	"sort"
	"time"

	"ch-observability/internal/promshim/model"
	"github.com/prometheus/prometheus/promql/parser"
)

type aggregateReducer interface {
	Add(value float64)
	Result() float64
}

type sumReducer struct{ value float64 }

func (r *sumReducer) Add(value float64) { r.value += value }
func (r *sumReducer) Result() float64   { return r.value }

type countReducer struct{ count float64 }

func (r *countReducer) Add(_ float64)   { r.count++ }
func (r *countReducer) Result() float64 { return r.count }

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
func (r *minReducer) Result() float64 { return r.value }

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
func (r *maxReducer) Result() float64 { return r.value }

type avgReducer struct{ sum, count float64 }

func (r *avgReducer) Add(value float64) { r.sum += value; r.count++ }
func (r *avgReducer) Result() float64 {
	if r.count == 0 {
		return math.NaN()
	}
	return r.sum / r.count
}

func AggregateRuntimeValue(op parser.ItemType, value model.RuntimeValue, grouping []string, without bool, evaluationTime time.Time) (model.RuntimeValue, error) {
	switch typed := value.(type) {
	case model.VectorValue:
		samples, err := AggregateInstantSamples(op, typed.Samples, grouping, without, evaluationTime)
		if err != nil {
			return nil, err
		}
		return model.VectorValue{Samples: samples}, nil
	case model.MatrixValue:
		series, err := AggregateRangeSeries(op, typed.Series, grouping, without)
		if err != nil {
			return nil, err
		}
		return model.MatrixValue{Series: series}, nil
	default:
		return nil, executionf("aggregation requires vector or matrix input, got %T", value)
	}
}

func AggregateInstantSamples(op parser.ItemType, samples []model.InstantSample, grouping []string, without bool, evaluationTime time.Time) ([]model.InstantSample, error) {
	timestamp := float64(evaluationTime.UnixNano()) / float64(time.Second)
	type bucket struct {
		Metric  map[string]string
		Reducer aggregateReducer
	}
	buckets := make(map[string]*bucket, len(samples))
	for _, sample := range samples {
		metric := model.AggregationMetric(sample.Metric, grouping, without)
		key := model.LabelsKey(metric)
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
	result := make([]model.InstantSample, 0, len(keys))
	for _, key := range keys {
		bucket := buckets[key]
		result = append(result, model.InstantSample{Metric: bucket.Metric, Timestamp: timestamp, Value: bucket.Reducer.Result()})
	}
	return result, nil
}

func AggregateRangeSeries(op parser.ItemType, series []model.RangeSeries, grouping []string, without bool) ([]model.RangeSeries, error) {
	type bucket struct {
		Metric map[string]string
		Values map[float64]aggregateReducer
	}
	buckets := make(map[string]*bucket, len(series))
	for _, input := range series {
		metric := model.AggregationMetric(input.Metric, grouping, without)
		key := model.LabelsKey(metric)
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
	result := make([]model.RangeSeries, 0, len(keys))
	for _, key := range keys {
		bucket := buckets[key]
		timestamps := make([]float64, 0, len(bucket.Values))
		for timestamp := range bucket.Values {
			timestamps = append(timestamps, timestamp)
		}
		sort.Float64s(timestamps)
		values := make([]model.RangePoint, 0, len(timestamps))
		for _, timestamp := range timestamps {
			values = append(values, model.RangePoint{Timestamp: timestamp, Value: bucket.Values[timestamp].Result()})
		}
		result = append(result, model.RangeSeries{Metric: bucket.Metric, Values: values})
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
		return nil, unsupportedf("aggregation reducer for operator %q is not implemented yet", op.String())
	}
}

func sortedBucketKeys[T any](buckets map[string]*T) []string {
	keys := make([]string, 0, len(buckets))
	for key := range buckets {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
