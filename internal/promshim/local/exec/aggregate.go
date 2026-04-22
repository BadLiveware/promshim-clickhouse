package exec

import (
	"math"
	"sort"
	"strconv"
	"time"

	"github.com/BadLiveware/promshim-ch/internal/promshim/model"
	promlabels "github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

type AggregationOptions struct {
	Grouping       []string
	Without        bool
	EvaluationTime time.Time
	ParamNumber    *float64
	ParamString    string
}

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

type varianceReducer struct {
	count float64
	mean  float64
	m2    float64
}

func (r *varianceReducer) Add(value float64) {
	r.count++
	delta := value - r.mean
	r.mean += delta / r.count
	r.m2 += delta * (value - r.mean)
}

func (r *varianceReducer) Result() float64 {
	if r.count == 0 {
		return math.NaN()
	}
	return r.m2 / r.count
}

type stdvarReducer struct{ varianceReducer }

type stddevReducer struct{ varianceReducer }

func (r *stddevReducer) Result() float64 {
	return math.Sqrt(r.varianceReducer.Result())
}

type quantileReducer struct {
	quantile float64
	values   []float64
}

func (r *quantileReducer) Add(value float64) {
	r.values = append(r.values, value)
}

func (r *quantileReducer) Result() float64 {
	return calculateQuantileFromValues(r.quantile, r.values)
}

type groupReducer struct{ seen bool }

func (r *groupReducer) Add(_ float64) { r.seen = true }
func (r *groupReducer) Result() float64 {
	if !r.seen {
		return math.NaN()
	}
	return 1
}

func AggregateRuntimeValue(op parser.ItemType, value model.RuntimeValue, opts AggregationOptions) (model.RuntimeValue, error) {
	switch op {
	case parser.TOPK, parser.BOTTOMK:
		k, err := aggregationKValue(op, opts.ParamNumber)
		if err != nil {
			return nil, err
		}
		switch typed := value.(type) {
		case model.VectorValue:
			return model.VectorValue{Samples: AggregateTopBottomInstantSamples(op, typed.Samples, opts.Grouping, opts.Without, k)}, nil
		case model.MatrixValue:
			return model.MatrixValue{Series: AggregateTopBottomRangeSeries(op, typed.Series, opts.Grouping, opts.Without, k)}, nil
		default:
			return nil, executionf("aggregation requires vector or matrix input, got %T", value)
		}
	case parser.LIMITK:
		k, err := aggregationKValue(op, opts.ParamNumber)
		if err != nil {
			return nil, err
		}
		switch typed := value.(type) {
		case model.VectorValue:
			return model.VectorValue{Samples: AggregateLimitKInstantSamples(typed.Samples, opts.Grouping, opts.Without, k)}, nil
		case model.MatrixValue:
			return model.MatrixValue{Series: AggregateLimitKRangeSeries(typed.Series, opts.Grouping, opts.Without, k)}, nil
		default:
			return nil, executionf("aggregation requires vector or matrix input, got %T", value)
		}
	case parser.LIMIT_RATIO:
		ratio, err := aggregationRatioValue(opts.ParamNumber)
		if err != nil {
			return nil, err
		}
		switch typed := value.(type) {
		case model.VectorValue:
			return model.VectorValue{Samples: AggregateLimitRatioInstantSamples(typed.Samples, opts.Grouping, opts.Without, ratio)}, nil
		case model.MatrixValue:
			return model.MatrixValue{Series: AggregateLimitRatioRangeSeries(typed.Series, opts.Grouping, opts.Without, ratio)}, nil
		default:
			return nil, executionf("aggregation requires vector or matrix input, got %T", value)
		}
	case parser.COUNT_VALUES:
		label, err := countValuesLabel(opts.ParamString)
		if err != nil {
			return nil, err
		}
		switch typed := value.(type) {
		case model.VectorValue:
			return model.VectorValue{Samples: AggregateCountValuesInstantSamples(typed.Samples, opts.Grouping, opts.Without, opts.EvaluationTime, label)}, nil
		case model.MatrixValue:
			return model.MatrixValue{Series: AggregateCountValuesRangeSeries(typed.Series, opts.Grouping, opts.Without, label)}, nil
		default:
			return nil, executionf("aggregation requires vector or matrix input, got %T", value)
		}
	default:
		switch typed := value.(type) {
		case model.VectorValue:
			samples, err := AggregateInstantSamples(op, typed.Samples, opts.Grouping, opts.Without, opts.EvaluationTime, opts.ParamNumber)
			if err != nil {
				return nil, err
			}
			return model.VectorValue{Samples: samples}, nil
		case model.MatrixValue:
			series, err := AggregateRangeSeries(op, typed.Series, opts.Grouping, opts.Without, opts.ParamNumber)
			if err != nil {
				return nil, err
			}
			return model.MatrixValue{Series: series}, nil
		default:
			return nil, executionf("aggregation requires vector or matrix input, got %T", value)
		}
	}
}

func AggregateInstantSamples(op parser.ItemType, samples []model.InstantSample, grouping []string, without bool, evaluationTime time.Time, paramNumber *float64) ([]model.InstantSample, error) {
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
			reducer, err := newAggregateReducer(op, paramNumber)
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

func AggregateRangeSeries(op parser.ItemType, series []model.RangeSeries, grouping []string, without bool, paramNumber *float64) ([]model.RangeSeries, error) {
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
				reducer, err := newAggregateReducer(op, paramNumber)
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

func AggregateTopBottomInstantSamples(op parser.ItemType, samples []model.InstantSample, grouping []string, without bool, k int) []model.InstantSample {
	if k < 1 || len(samples) == 0 {
		return nil
	}
	buckets := make(map[string][]model.InstantSample, len(samples))
	for _, sample := range samples {
		key := model.LabelsKey(model.AggregationMetric(sample.Metric, grouping, without))
		buckets[key] = append(buckets[key], sample)
	}
	keys := sortedMapKeys(buckets)
	result := make([]model.InstantSample, 0, len(samples))
	for _, key := range keys {
		bucket := append([]model.InstantSample(nil), buckets[key]...)
		sort.SliceStable(bucket, func(i, j int) bool { return aggregateSampleLess(op, bucket[i], bucket[j]) })
		limit := min(k, len(bucket))
		for _, sample := range bucket[:limit] {
			result = append(result, model.InstantSample{Metric: model.CloneMetric(sample.Metric), Timestamp: sample.Timestamp, Value: sample.Value})
		}
	}
	return result
}

func AggregateTopBottomRangeSeries(op parser.ItemType, series []model.RangeSeries, grouping []string, without bool, k int) []model.RangeSeries {
	if k < 1 || len(series) == 0 {
		return nil
	}
	samplesByTimestamp, timestamps := rangeSeriesSamplesByTimestamp(series)
	grouped := make(map[string]model.RangeSeries, len(series))
	for _, timestamp := range timestamps {
		selected := AggregateTopBottomInstantSamples(op, samplesByTimestamp[timestamp], grouping, without, k)
		for _, sample := range selected {
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

func AggregateLimitKInstantSamples(samples []model.InstantSample, grouping []string, without bool, k int) []model.InstantSample {
	if k < 1 || len(samples) == 0 {
		return nil
	}
	buckets := make(map[string][]model.InstantSample, len(samples))
	for _, sample := range samples {
		key := model.LabelsKey(model.AggregationMetric(sample.Metric, grouping, without))
		buckets[key] = append(buckets[key], sample)
	}
	keys := sortedMapKeys(buckets)
	result := make([]model.InstantSample, 0, len(samples))
	for _, key := range keys {
		bucket := append([]model.InstantSample(nil), buckets[key]...)
		sort.SliceStable(bucket, func(i, j int) bool { return model.LabelsKey(bucket[i].Metric) < model.LabelsKey(bucket[j].Metric) })
		limit := min(k, len(bucket))
		for _, sample := range bucket[:limit] {
			result = append(result, model.InstantSample{Metric: model.CloneMetric(sample.Metric), Timestamp: sample.Timestamp, Value: sample.Value})
		}
	}
	return result
}

func AggregateLimitKRangeSeries(series []model.RangeSeries, grouping []string, without bool, k int) []model.RangeSeries {
	if k < 1 || len(series) == 0 {
		return nil
	}
	samplesByTimestamp, timestamps := rangeSeriesSamplesByTimestamp(series)
	grouped := make(map[string]model.RangeSeries, len(series))
	for _, timestamp := range timestamps {
		selected := AggregateLimitKInstantSamples(samplesByTimestamp[timestamp], grouping, without, k)
		for _, sample := range selected {
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

func AggregateLimitRatioInstantSamples(samples []model.InstantSample, grouping []string, without bool, ratio float64) []model.InstantSample {
	if ratio == 0 || len(samples) == 0 {
		return nil
	}
	buckets := make(map[string][]model.InstantSample, len(samples))
	for _, sample := range samples {
		key := model.LabelsKey(model.AggregationMetric(sample.Metric, grouping, without))
		buckets[key] = append(buckets[key], sample)
	}
	keys := sortedMapKeys(buckets)
	result := make([]model.InstantSample, 0, len(samples))
	for _, key := range keys {
		for _, sample := range buckets[key] {
			if limitRatioIncludesSample(sample.Metric, ratio) {
				result = append(result, model.InstantSample{Metric: model.CloneMetric(sample.Metric), Timestamp: sample.Timestamp, Value: sample.Value})
			}
		}
	}
	return result
}

func AggregateLimitRatioRangeSeries(series []model.RangeSeries, grouping []string, without bool, ratio float64) []model.RangeSeries {
	if ratio == 0 || len(series) == 0 {
		return nil
	}
	samplesByTimestamp, timestamps := rangeSeriesSamplesByTimestamp(series)
	grouped := make(map[string]model.RangeSeries, len(series))
	for _, timestamp := range timestamps {
		selected := AggregateLimitRatioInstantSamples(samplesByTimestamp[timestamp], grouping, without, ratio)
		for _, sample := range selected {
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

func limitRatioIncludesSample(metric map[string]string, ratio float64) bool {
	if ratio >= 1 || ratio <= -1 {
		return true
	}
	hash := promlabels.FromMap(metric).Hash()
	if ratio > 0 {
		return hash < uint64(ratio*float64(^uint64(0)))
	}
	return hash >= uint64((1+ratio)*float64(^uint64(0)))
}

func AggregateCountValuesInstantSamples(samples []model.InstantSample, grouping []string, without bool, evaluationTime time.Time, valueLabel string) []model.InstantSample {
	timestamp := float64(evaluationTime.UnixNano()) / float64(time.Second)
	effectiveGrouping := countValuesGrouping(grouping, without, valueLabel)
	type bucket struct {
		Metric map[string]string
		Count  int
	}
	buckets := make(map[string]*bucket, len(samples))
	for _, sample := range samples {
		metric := model.CloneMetric(sample.Metric)
		metric[valueLabel] = formatAggregationValue(sample.Value)
		aggregatedMetric := model.AggregationMetric(metric, effectiveGrouping, without)
		key := model.LabelsKey(aggregatedMetric)
		if buckets[key] == nil {
			buckets[key] = &bucket{Metric: aggregatedMetric}
		}
		buckets[key].Count++
	}
	keys := sortedBucketKeys(buckets)
	result := make([]model.InstantSample, 0, len(keys))
	for _, key := range keys {
		bucket := buckets[key]
		result = append(result, model.InstantSample{Metric: bucket.Metric, Timestamp: timestamp, Value: float64(bucket.Count)})
	}
	return result
}

func AggregateCountValuesRangeSeries(series []model.RangeSeries, grouping []string, without bool, valueLabel string) []model.RangeSeries {
	samplesByTimestamp, timestamps := rangeSeriesSamplesByTimestamp(series)
	grouped := make(map[string]model.RangeSeries, len(series))
	for _, timestamp := range timestamps {
		selected := AggregateCountValuesInstantSamples(samplesByTimestamp[timestamp], grouping, without, time.Unix(0, int64(timestamp*float64(time.Second))).UTC(), valueLabel)
		for _, sample := range selected {
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

func aggregationKValue(op parser.ItemType, value *float64) (int, error) {
	if value == nil {
		return 0, badDataf("aggregation operator %q requires a scalar parameter", op.String())
	}
	if math.IsNaN(*value) {
		return 0, badDataf("parameter value is NaN")
	}
	return int(*value), nil
}

func aggregationRatioValue(value *float64) (float64, error) {
	if value == nil {
		return 0, badDataf("aggregation operator %q requires a scalar parameter", "limit_ratio")
	}
	if math.IsNaN(*value) {
		return 0, badDataf("ratio value is NaN")
	}
	ratio := *value
	if ratio < -1 {
		ratio = -1
	}
	if ratio > 1 {
		ratio = 1
	}
	return ratio, nil
}

func countValuesLabel(value string) (string, error) {
	if value == "" {
		return "", badDataf("count_values requires a label parameter")
	}
	return value, nil
}

func countValuesGrouping(grouping []string, without bool, valueLabel string) []string {
	if without {
		return append([]string(nil), grouping...)
	}
	result := append([]string(nil), grouping...)
	for _, label := range result {
		if label == valueLabel {
			return result
		}
	}
	result = append(result, valueLabel)
	return result
}

func rangeSeriesSamplesByTimestamp(series []model.RangeSeries) (map[float64][]model.InstantSample, []float64) {
	grouped := make(map[float64][]model.InstantSample, len(series))
	timestamps := make([]float64, 0)
	seen := make(map[float64]struct{})
	for _, item := range series {
		for _, point := range item.Values {
			grouped[point.Timestamp] = append(grouped[point.Timestamp], model.InstantSample{Metric: item.Metric, Timestamp: point.Timestamp, Value: point.Value})
			if _, ok := seen[point.Timestamp]; ok {
				continue
			}
			seen[point.Timestamp] = struct{}{}
			timestamps = append(timestamps, point.Timestamp)
		}
	}
	sort.Float64s(timestamps)
	return grouped, timestamps
}

func aggregateSampleLess(op parser.ItemType, left, right model.InstantSample) bool {
	leftNaN := math.IsNaN(left.Value)
	rightNaN := math.IsNaN(right.Value)
	switch {
	case leftNaN && rightNaN:
		return model.LabelsKey(left.Metric) < model.LabelsKey(right.Metric)
	case leftNaN:
		return false
	case rightNaN:
		return true
	}
	switch op {
	case parser.TOPK:
		if left.Value != right.Value {
			return left.Value > right.Value
		}
	case parser.BOTTOMK:
		if left.Value != right.Value {
			return left.Value < right.Value
		}
	default:
		panic("unexpected aggregation selector operator")
	}
	return model.LabelsKey(left.Metric) < model.LabelsKey(right.Metric)
}

func formatAggregationValue(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func newAggregateReducer(op parser.ItemType, paramNumber *float64) (aggregateReducer, error) {
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
	case parser.STDVAR:
		return &stdvarReducer{}, nil
	case parser.STDDEV:
		return &stddevReducer{}, nil
	case parser.QUANTILE:
		if paramNumber == nil {
			return nil, badDataf("aggregation operator %q requires a scalar parameter", op.String())
		}
		return &quantileReducer{quantile: *paramNumber}, nil
	case parser.GROUP:
		return &groupReducer{}, nil
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

func sortedMapKeys[T any](items map[string]T) []string {
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
