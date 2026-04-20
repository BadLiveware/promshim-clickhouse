package promshim

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/prometheus/model/labels"
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

func supportedSumAggregation(expr parser.Expr) (*parser.AggregateExpr, bool) {
	unwrapped := unwrapTransparentExpr(expr)
	agg, ok := unwrapped.(*parser.AggregateExpr)
	if !ok {
		return nil, false
	}
	return agg, isSupportedSumAggregation(agg)
}

func unwrapTransparentExpr(expr parser.Expr) parser.Expr {
	for {
		switch e := expr.(type) {
		case *parser.ParenExpr:
			expr = e.Expr
		case *parser.StepInvariantExpr:
			expr = e.Expr
		default:
			return expr
		}
	}
}

func (h *Handler) executeInstantSumAggregation(ctx context.Context, agg *parser.AggregateExpr, evaluationTime time.Time) ([]map[string]any, *apiError) {
	samples, apiErr := h.executeDelegatedInstantVector(ctx, agg.Expr, evaluationTime)
	if apiErr != nil {
		return nil, apiErr
	}
	return aggregateInstantSum(samples, agg.Grouping, agg.Without, evaluationTime), nil
}

func (h *Handler) executeRangeSumAggregation(ctx context.Context, agg *parser.AggregateExpr, start, end time.Time, step time.Duration) ([]map[string]any, *apiError) {
	series, apiErr := h.executeDelegatedRangeMatrix(ctx, agg.Expr, start, end, step)
	if apiErr != nil {
		return nil, apiErr
	}
	return aggregateRangeSum(series, agg.Grouping, agg.Without), nil
}

func (h *Handler) executeDelegatedInstantVector(ctx context.Context, expr parser.Expr, evaluationTime time.Time) ([]instantSample, *apiError) {
	sql, params := buildInstantQuerySQL(h.opts, expr.String(), evaluationTime.UnixMilli())
	response, err := h.client.Execute(ctx, sql, params)
	if err != nil {
		return nil, toAPIError(err)
	}
	defer response.Body.Close()
	return decodeInstantSamples(response.Body)
}

func (h *Handler) executeDelegatedRangeMatrix(ctx context.Context, expr parser.Expr, start, end time.Time, step time.Duration) ([]rangeSeries, *apiError) {
	sql, params := buildRangeQuerySQL(h.opts, expr.String(), start.UnixMilli(), end.UnixMilli(), step.Milliseconds())
	response, err := h.client.Execute(ctx, sql, params)
	if err != nil {
		return nil, toAPIError(err)
	}
	defer response.Body.Close()
	return decodeRangeSeries(response.Body)
}

func decodeInstantSamples(body io.Reader) ([]instantSample, *apiError) {
	samples := make([]instantSample, 0, 16)
	scanner := newScanner(body)
	for scanner.Scan() {
		var row instantRow
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			return nil, executionAPIError(err)
		}
		timestamp, err := parseClickHouseTimestamp(row.Timestamp)
		if err != nil {
			return nil, executionAPIError(err)
		}
		value, err := rawPromValueToFloat64(row.Value)
		if err != nil {
			return nil, executionAPIError(err)
		}
		samples = append(samples, instantSample{
			Metric:    tagsToObject(row.Tags),
			Timestamp: timestamp,
			Value:     value,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, executionAPIError(err)
	}
	return samples, nil
}

func decodeRangeSeries(body io.Reader) ([]rangeSeries, *apiError) {
	series := make([]rangeSeries, 0, 16)
	scanner := newScanner(body)
	for scanner.Scan() {
		var row matrixRow
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			return nil, executionAPIError(err)
		}
		values := make([]rangePoint, 0, len(row.TimeSeries))
		for _, sample := range row.TimeSeries {
			if len(sample) != 2 {
				return nil, &apiError{StatusCode: 502, ErrorType: "execution", Error: "unexpected time_series row shape"}
			}
			var timestampRaw string
			if err := json.Unmarshal(sample[0], &timestampRaw); err != nil {
				return nil, executionAPIError(err)
			}
			timestamp, err := parseClickHouseTimestamp(timestampRaw)
			if err != nil {
				return nil, executionAPIError(err)
			}
			value, err := rawPromValueToFloat64(sample[1])
			if err != nil {
				return nil, executionAPIError(err)
			}
			values = append(values, rangePoint{Timestamp: timestamp, Value: value})
		}
		series = append(series, rangeSeries{Metric: tagsToObject(row.Tags), Values: values})
	}
	if err := scanner.Err(); err != nil {
		return nil, executionAPIError(err)
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

func aggregateInstantSum(samples []instantSample, grouping []string, without bool, evaluationTime time.Time) []map[string]any {
	timestamp := float64(evaluationTime.UnixNano()) / float64(time.Second)
	type bucket struct {
		Metric map[string]string
		Value  float64
	}
	buckets := make(map[string]*bucket, len(samples))
	for _, sample := range samples {
		metric := aggregationMetric(sample.Metric, grouping, without)
		key := labelsKey(metric)
		if _, ok := buckets[key]; !ok {
			buckets[key] = &bucket{Metric: metric}
		}
		buckets[key].Value += sample.Value
	}
	keys := sortedBucketKeys(buckets)
	result := make([]map[string]any, 0, len(keys))
	for _, key := range keys {
		bucket := buckets[key]
		result = append(result, map[string]any{
			"metric": bucket.Metric,
			"value":  []any{timestamp, formatPromValue(bucket.Value)},
		})
	}
	return result
}

func aggregateRangeSum(series []rangeSeries, grouping []string, without bool) []map[string]any {
	type bucket struct {
		Metric map[string]string
		Values map[float64]float64
	}
	buckets := make(map[string]*bucket, len(series))
	for _, input := range series {
		metric := aggregationMetric(input.Metric, grouping, without)
		key := labelsKey(metric)
		if _, ok := buckets[key]; !ok {
			buckets[key] = &bucket{Metric: metric, Values: map[float64]float64{}}
		}
		for _, point := range input.Values {
			buckets[key].Values[point.Timestamp] += point.Value
		}
	}
	keys := sortedBucketKeys(buckets)
	result := make([]map[string]any, 0, len(keys))
	for _, key := range keys {
		bucket := buckets[key]
		timestamps := make([]float64, 0, len(bucket.Values))
		for timestamp := range bucket.Values {
			timestamps = append(timestamps, timestamp)
		}
		sort.Float64s(timestamps)
		values := make([][]any, 0, len(timestamps))
		for _, timestamp := range timestamps {
			values = append(values, []any{timestamp, formatPromValue(bucket.Values[timestamp])})
		}
		result = append(result, map[string]any{
			"metric": bucket.Metric,
			"values": values,
		})
	}
	return result
}

func aggregationMetric(metric map[string]string, grouping []string, without bool) map[string]string {
	if without {
		excluded := map[string]struct{}{labels.MetricName: {}}
		for _, label := range grouping {
			excluded[label] = struct{}{}
		}
		result := make(map[string]string, len(metric))
		for key, value := range metric {
			if _, skip := excluded[key]; skip {
				continue
			}
			result[key] = value
		}
		return result
	}
	if len(grouping) == 0 {
		return map[string]string{}
	}
	result := make(map[string]string, len(grouping))
	for _, label := range grouping {
		if value, ok := metric[label]; ok {
			result[label] = value
		}
	}
	return result
}

func labelsKey(metric map[string]string) string {
	if len(metric) == 0 {
		return ""
	}
	keys := make([]string, 0, len(metric))
	for key := range metric {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var builder strings.Builder
	for _, key := range keys {
		builder.WriteString(key)
		builder.WriteByte('\xff')
		builder.WriteString(metric[key])
		builder.WriteByte('\xfe')
	}
	return builder.String()
}

func formatPromValue(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func executionAPIError(err error) *apiError {
	return &apiError{StatusCode: 502, ErrorType: "execution", Error: err.Error()}
}

func toAPIError(err error) *apiError {
	var queryErr *QueryError
	if asQueryError(err, &queryErr) {
		return &apiError{StatusCode: queryErr.StatusCode, ErrorType: queryErr.ErrorType, Error: queryErr.Message}
	}
	return executionAPIError(err)
}

func sortedBucketKeys[T any](buckets map[string]*T) []string {
	keys := make([]string, 0, len(buckets))
	for key := range buckets {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
