package model

import (
	"sort"
	"strings"

	"github.com/prometheus/prometheus/model/labels"
)

func NormalizeLabelSet(metric map[string]string) []NormalizedLabel {
	if len(metric) == 0 {
		return nil
	}
	keys := make([]string, 0, len(metric))
	for key := range metric {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]NormalizedLabel, 0, len(keys))
	for _, key := range keys {
		result = append(result, NormalizedLabel{Name: key, Value: metric[key]})
	}
	return result
}

func LabelsKey(metric map[string]string) string {
	normalized := NormalizeLabelSet(metric)
	if len(normalized) == 0 {
		return ""
	}
	var builder strings.Builder
	for _, label := range normalized {
		builder.WriteString(label.Name)
		builder.WriteByte('\xff')
		builder.WriteString(label.Value)
		builder.WriteByte('\xfe')
	}
	return builder.String()
}

func AggregationMetric(metric map[string]string, grouping []string, without bool) map[string]string {
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

func DropMetricName(metric map[string]string) map[string]string {
	result := make(map[string]string, len(metric))
	for key, value := range metric {
		if key == labels.MetricName {
			continue
		}
		result[key] = value
	}
	return result
}

func CloneMetric(metric map[string]string) map[string]string {
	result := make(map[string]string, len(metric))
	for key, value := range metric {
		result[key] = value
	}
	return result
}

func CloneSamples(samples []InstantSample) []InstantSample {
	result := make([]InstantSample, 0, len(samples))
	for _, sample := range samples {
		result = append(result, InstantSample{Metric: CloneMetric(sample.Metric), Timestamp: sample.Timestamp, Value: sample.Value})
	}
	return result
}

func CloneSeries(series []RangeSeries) []RangeSeries {
	result := make([]RangeSeries, 0, len(series))
	for _, item := range series {
		result = append(result, RangeSeries{Metric: CloneMetric(item.Metric), Values: CloneRangePoints(item.Values)})
	}
	return result
}

func CloneRangePoints(points []RangePoint) []RangePoint {
	result := make([]RangePoint, 0, len(points))
	for _, point := range points {
		result = append(result, point)
	}
	return result
}
