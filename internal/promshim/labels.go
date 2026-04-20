package promshim

import (
	"sort"
	"strings"

	"github.com/prometheus/prometheus/model/labels"
)

type normalizedLabel struct {
	Name  string
	Value string
}

func normalizeLabelSet(metric map[string]string) []normalizedLabel {
	if len(metric) == 0 {
		return nil
	}
	keys := make([]string, 0, len(metric))
	for key := range metric {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]normalizedLabel, 0, len(keys))
	for _, key := range keys {
		result = append(result, normalizedLabel{Name: key, Value: metric[key]})
	}
	return result
}

func labelsKey(metric map[string]string) string {
	normalized := normalizeLabelSet(metric)
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

func dropMetricName(metric map[string]string) map[string]string {
	result := make(map[string]string, len(metric))
	for key, value := range metric {
		if key == labels.MetricName {
			continue
		}
		result[key] = value
	}
	return result
}

func cloneMetric(metric map[string]string) map[string]string {
	result := make(map[string]string, len(metric))
	for key, value := range metric {
		result[key] = value
	}
	return result
}

func cloneSamples(samples []instantSample) []instantSample {
	result := make([]instantSample, 0, len(samples))
	for _, sample := range samples {
		result = append(result, instantSample{Metric: cloneMetric(sample.Metric), Timestamp: sample.Timestamp, Value: sample.Value})
	}
	return result
}

func cloneSeries(series []rangeSeries) []rangeSeries {
	result := make([]rangeSeries, 0, len(series))
	for _, item := range series {
		result = append(result, rangeSeries{Metric: cloneMetric(item.Metric), Values: cloneRangePoints(item.Values)})
	}
	return result
}

func cloneRangePoints(points []rangePoint) []rangePoint {
	result := make([]rangePoint, 0, len(points))
	for _, point := range points {
		result = append(result, point)
	}
	return result
}
