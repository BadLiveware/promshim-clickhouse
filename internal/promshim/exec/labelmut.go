package exec

import (
	"sort"
	"strings"

	modelpkg "github.com/BadLiveware/promshim-ch/internal/promshim/model"
)

func ApplyLabelReplaceRuntimeValue(value modelpkg.RuntimeValue, cfg modelpkg.LabelReplaceConfig) (modelpkg.RuntimeValue, error) {
	switch typed := value.(type) {
	case modelpkg.VectorValue:
		mutated := make([]modelpkg.InstantSample, 0, len(typed.Samples))
		for _, sample := range typed.Samples {
			metric := modelpkg.CloneMetric(sample.Metric)
			srcVal := metric[cfg.Src]
			indexes := cfg.Regex.FindStringSubmatchIndex(srcVal)
			if indexes != nil {
				metric[cfg.Dst] = string(cfg.Regex.ExpandString([]byte{}, cfg.Repl, srcVal, indexes))
			}
			mutated = append(mutated, modelpkg.InstantSample{Metric: metric, Timestamp: sample.Timestamp, Value: sample.Value})
		}
		merged, err := mergeSamplesWithSameLabelset(mutated)
		if err != nil {
			return nil, err
		}
		return modelpkg.VectorValue{Samples: merged}, nil
	case modelpkg.MatrixValue:
		mutated := make([]modelpkg.RangeSeries, 0, len(typed.Series))
		for _, series := range typed.Series {
			metric := modelpkg.CloneMetric(series.Metric)
			srcVal := metric[cfg.Src]
			indexes := cfg.Regex.FindStringSubmatchIndex(srcVal)
			if indexes != nil {
				metric[cfg.Dst] = string(cfg.Regex.ExpandString([]byte{}, cfg.Repl, srcVal, indexes))
			}
			mutated = append(mutated, modelpkg.RangeSeries{Metric: metric, Values: modelpkg.CloneRangePoints(series.Values)})
		}
		merged, err := mergeSeriesWithSameLabelset(mutated)
		if err != nil {
			return nil, err
		}
		return modelpkg.MatrixValue{Series: merged}, nil
	default:
		return nil, executionf("label_replace requires vector or matrix input, got %T", value)
	}
}

func ApplyLabelJoinRuntimeValue(value modelpkg.RuntimeValue, cfg modelpkg.LabelJoinConfig) (modelpkg.RuntimeValue, error) {
	switch typed := value.(type) {
	case modelpkg.VectorValue:
		mutated := make([]modelpkg.InstantSample, 0, len(typed.Samples))
		for _, sample := range typed.Samples {
			metric := modelpkg.CloneMetric(sample.Metric)
			srcVals := make([]string, len(cfg.SrcLabels))
			for i, src := range cfg.SrcLabels {
				srcVals[i] = metric[src]
			}
			metric[cfg.Dst] = strings.Join(srcVals, cfg.Separator)
			mutated = append(mutated, modelpkg.InstantSample{Metric: metric, Timestamp: sample.Timestamp, Value: sample.Value})
		}
		merged, err := mergeSamplesWithSameLabelset(mutated)
		if err != nil {
			return nil, err
		}
		return modelpkg.VectorValue{Samples: merged}, nil
	case modelpkg.MatrixValue:
		mutated := make([]modelpkg.RangeSeries, 0, len(typed.Series))
		for _, series := range typed.Series {
			metric := modelpkg.CloneMetric(series.Metric)
			srcVals := make([]string, len(cfg.SrcLabels))
			for i, src := range cfg.SrcLabels {
				srcVals[i] = metric[src]
			}
			metric[cfg.Dst] = strings.Join(srcVals, cfg.Separator)
			mutated = append(mutated, modelpkg.RangeSeries{Metric: metric, Values: modelpkg.CloneRangePoints(series.Values)})
		}
		merged, err := mergeSeriesWithSameLabelset(mutated)
		if err != nil {
			return nil, err
		}
		return modelpkg.MatrixValue{Series: merged}, nil
	default:
		return nil, executionf("label_join requires vector or matrix input, got %T", value)
	}
}

func mergeSamplesWithSameLabelset(samples []modelpkg.InstantSample) ([]modelpkg.InstantSample, error) {
	if len(samples) <= 1 {
		return samples, nil
	}
	merged := make(map[string]modelpkg.InstantSample, len(samples))
	for _, sample := range samples {
		key := modelpkg.LabelsKey(sample.Metric)
		if _, exists := merged[key]; exists {
			return nil, executionf("vector cannot contain metrics with the same labelset")
		}
		merged[key] = sample
	}
	keys := make([]string, 0, len(merged))
	for key := range merged {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]modelpkg.InstantSample, 0, len(keys))
	for _, key := range keys {
		result = append(result, merged[key])
	}
	return result, nil
}

func mergeSeriesWithSameLabelset(series []modelpkg.RangeSeries) ([]modelpkg.RangeSeries, error) {
	if len(series) <= 1 {
		return series, nil
	}
	grouped := make(map[string]modelpkg.RangeSeries, len(series))
	for _, item := range series {
		key := modelpkg.LabelsKey(item.Metric)
		if existing, ok := grouped[key]; ok {
			existing.Values = append(existing.Values, modelpkg.CloneRangePoints(item.Values)...)
			grouped[key] = existing
		} else {
			grouped[key] = modelpkg.RangeSeries{Metric: modelpkg.CloneMetric(item.Metric), Values: modelpkg.CloneRangePoints(item.Values)}
		}
	}
	keys := make([]string, 0, len(grouped))
	for key := range grouped {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]modelpkg.RangeSeries, 0, len(keys))
	for _, key := range keys {
		item := grouped[key]
		values, err := modelpkg.SortAndValidateRangePoints(item.Values)
		if err != nil {
			return nil, executionf("%s", err.Error())
		}
		item.Values = values
		result = append(result, item)
	}
	return result, nil
}
