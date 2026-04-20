package promshim

import (
	"regexp"
	"sort"
	"strings"

	"github.com/prometheus/common/model"
)

type localLabelReplaceConfig struct {
	Dst   string
	Repl  string
	Src   string
	Regex *regexp.Regexp
}

type localLabelJoinConfig struct {
	Dst       string
	Separator string
	SrcLabels []string
}

func applyLabelReplaceRuntimeValue(value runtimeValue, cfg localLabelReplaceConfig) (runtimeValue, error) {
	switch typed := value.(type) {
	case vectorValue:
		mutated := make([]instantSample, 0, len(typed.Samples))
		for _, sample := range typed.Samples {
			metric := cloneMetric(sample.Metric)
			srcVal := metric[cfg.Src]
			indexes := cfg.Regex.FindStringSubmatchIndex(srcVal)
			if indexes != nil {
				metric[cfg.Dst] = string(cfg.Regex.ExpandString([]byte{}, cfg.Repl, srcVal, indexes))
			}
			mutated = append(mutated, instantSample{Metric: metric, Timestamp: sample.Timestamp, Value: sample.Value})
		}
		merged, err := mergeSamplesWithSameLabelset(mutated)
		if err != nil {
			return nil, err
		}
		return vectorValue{Samples: merged}, nil
	case matrixValue:
		mutated := make([]rangeSeries, 0, len(typed.Series))
		for _, series := range typed.Series {
			metric := cloneMetric(series.Metric)
			srcVal := metric[cfg.Src]
			indexes := cfg.Regex.FindStringSubmatchIndex(srcVal)
			if indexes != nil {
				metric[cfg.Dst] = string(cfg.Regex.ExpandString([]byte{}, cfg.Repl, srcVal, indexes))
			}
			mutated = append(mutated, rangeSeries{Metric: metric, Values: cloneRangePoints(series.Values)})
		}
		merged, err := mergeSeriesWithSameLabelset(mutated)
		if err != nil {
			return nil, err
		}
		return matrixValue{Series: merged}, nil
	default:
		return nil, newExecutionErrorf("label_replace requires vector or matrix input, got %T", value)
	}
}

func applyLabelJoinRuntimeValue(value runtimeValue, cfg localLabelJoinConfig) (runtimeValue, error) {
	switch typed := value.(type) {
	case vectorValue:
		mutated := make([]instantSample, 0, len(typed.Samples))
		for _, sample := range typed.Samples {
			metric := cloneMetric(sample.Metric)
			srcVals := make([]string, len(cfg.SrcLabels))
			for i, src := range cfg.SrcLabels {
				srcVals[i] = metric[src]
			}
			metric[cfg.Dst] = strings.Join(srcVals, cfg.Separator)
			mutated = append(mutated, instantSample{Metric: metric, Timestamp: sample.Timestamp, Value: sample.Value})
		}
		merged, err := mergeSamplesWithSameLabelset(mutated)
		if err != nil {
			return nil, err
		}
		return vectorValue{Samples: merged}, nil
	case matrixValue:
		mutated := make([]rangeSeries, 0, len(typed.Series))
		for _, series := range typed.Series {
			metric := cloneMetric(series.Metric)
			srcVals := make([]string, len(cfg.SrcLabels))
			for i, src := range cfg.SrcLabels {
				srcVals[i] = metric[src]
			}
			metric[cfg.Dst] = strings.Join(srcVals, cfg.Separator)
			mutated = append(mutated, rangeSeries{Metric: metric, Values: cloneRangePoints(series.Values)})
		}
		merged, err := mergeSeriesWithSameLabelset(mutated)
		if err != nil {
			return nil, err
		}
		return matrixValue{Series: merged}, nil
	default:
		return nil, newExecutionErrorf("label_join requires vector or matrix input, got %T", value)
	}
}

func buildLabelReplaceConfig(dst, repl, src, regexStr string) (localLabelReplaceConfig, error) {
	regex, err := regexp.Compile("^(?s:" + regexStr + ")$")
	if err != nil {
		return localLabelReplaceConfig{}, newBadDataErrorf("invalid regular expression in label_replace(): %s", regexStr)
	}
	if !model.UTF8Validation.IsValidLabelName(dst) {
		return localLabelReplaceConfig{}, newBadDataErrorf("invalid destination label name in label_replace(): %s", dst)
	}
	return localLabelReplaceConfig{Dst: dst, Repl: repl, Src: src, Regex: regex}, nil
}

func buildLabelJoinConfig(dst, sep string, srcLabels []string) (localLabelJoinConfig, error) {
	for _, src := range srcLabels {
		if !model.UTF8Validation.IsValidLabelName(src) {
			return localLabelJoinConfig{}, newBadDataErrorf("invalid source label name in label_join(): %s", src)
		}
	}
	if !model.UTF8Validation.IsValidLabelName(dst) {
		return localLabelJoinConfig{}, newBadDataErrorf("invalid destination label name in label_join(): %s", dst)
	}
	return localLabelJoinConfig{Dst: dst, Separator: sep, SrcLabels: append([]string(nil), srcLabels...)}, nil
}

func mergeSamplesWithSameLabelset(samples []instantSample) ([]instantSample, error) {
	if len(samples) <= 1 {
		return samples, nil
	}
	merged := make(map[string]instantSample, len(samples))
	for _, sample := range samples {
		key := labelsKey(sample.Metric)
		if _, exists := merged[key]; exists {
			return nil, newExecutionErrorf("vector cannot contain metrics with the same labelset")
		}
		merged[key] = sample
	}
	keys := make([]string, 0, len(merged))
	for key := range merged {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]instantSample, 0, len(keys))
	for _, key := range keys {
		result = append(result, merged[key])
	}
	return result, nil
}

func mergeSeriesWithSameLabelset(series []rangeSeries) ([]rangeSeries, error) {
	if len(series) <= 1 {
		return series, nil
	}
	grouped := make(map[string]rangeSeries, len(series))
	for _, item := range series {
		key := labelsKey(item.Metric)
		if existing, ok := grouped[key]; ok {
			existing.Values = append(existing.Values, cloneRangePoints(item.Values)...)
			grouped[key] = existing
		} else {
			grouped[key] = rangeSeries{Metric: cloneMetric(item.Metric), Values: cloneRangePoints(item.Values)}
		}
	}
	keys := make([]string, 0, len(grouped))
	for key := range grouped {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]rangeSeries, 0, len(keys))
	for _, key := range keys {
		item := grouped[key]
		values, err := sortAndValidateRangePoints(item.Values)
		if err != nil {
			return nil, err
		}
		item.Values = values
		result = append(result, item)
	}
	return result, nil
}
