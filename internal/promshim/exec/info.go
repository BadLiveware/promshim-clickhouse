package exec

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"ch-observability/internal/promshim/model"
	commonmodel "github.com/prometheus/common/model"
	promlabels "github.com/prometheus/prometheus/model/labels"
)

const defaultInfoMetricName = "target_info"

var infoIdentifyingLabels = []string{"instance", "job"}

func ApplyInfo(input, info model.RuntimeValue, selectorMatchers []*promlabels.Matcher) (model.VectorValue, error) {
	base, ok := input.(model.VectorValue)
	if !ok {
		return model.VectorValue{}, unsupportedf("info() requires vector input, got %T", input)
	}
	infoVector, ok := info.(model.VectorValue)
	if !ok {
		return model.VectorValue{}, unsupportedf("info() requires vector info-series input, got %T", info)
	}
	dataLabelMatchers := infoDataLabelMatchers(selectorMatchers)
	effectiveNameMatchers := effectiveInfoNameMatchers(selectorMatchers)
	ignoredInfoNames := effectiveInfoMetricNames(infoVector.Samples)
	infoByKey := map[string]model.InstantSample{}
	for _, sample := range infoVector.Samples {
		name := sample.Metric[commonmodel.MetricNameLabel]
		if name == "" {
			continue
		}
		key := infoSampleKey(name, sample.Metric)
		if _, exists := infoByKey[key]; exists {
			return model.VectorValue{}, badDataf("found duplicate series for info metric %q on identifying labels", name)
		}
		infoByKey[key] = cloneInstantSample(sample)
	}

	out := make([]model.InstantSample, 0, len(base.Samples))
	for _, sample := range base.Samples {
		if matchesAllNameMatchers(sample.Metric[commonmodel.MetricNameLabel], effectiveNameMatchers) {
			out = append(out, cloneInstantSample(sample))
			continue
		}
		merged := model.CloneMetric(sample.Metric)
		matchedInfo := false
		seenInfoNames := map[string]struct{}{}
		for infoName := range ignoredInfoNames {
			infoSample, ok := infoByKey[infoSampleKey(infoName, sample.Metric)]
			if !ok {
				continue
			}
			if _, exists := seenInfoNames[infoName]; exists {
				continue
			}
			for label, value := range infoSample.Metric {
				if label == commonmodel.MetricNameLabel {
					continue
				}
				if isInfoIdentifyingLabel(label) {
					continue
				}
				if len(dataLabelMatchers) > 0 {
					matchers, exists := dataLabelMatchers[label]
					if !exists {
						continue
					}
					if !allMatchersMatch(value, matchers) {
						continue
					}
				}
				if existing, exists := merged[label]; exists {
					if existing != value {
						continue
					}
					continue
				}
				merged[label] = value
			}
			matchedInfo = true
			seenInfoNames[infoName] = struct{}{}
		}
		if !matchedInfo && len(dataLabelMatchers) > 0 && !allDataLabelMatchersMatchEmpty(dataLabelMatchers) {
			continue
		}
		out = append(out, model.InstantSample{Metric: merged, Timestamp: sample.Timestamp, Value: sample.Value})
	}
	sort.SliceStable(out, func(i, j int) bool {
		left := model.LabelsKey(out[i].Metric)
		right := model.LabelsKey(out[j].Metric)
		if left == right {
			return out[i].Timestamp < out[j].Timestamp
		}
		return left < right
	})
	return model.VectorValue{Samples: out}, nil
}

func BuildInfoFetchExprString(base model.VectorValue, selectorMatchers []*promlabels.Matcher) (string, error) {
	idValues := map[string]map[string]struct{}{}
	for _, sample := range base.Samples {
		for _, label := range infoIdentifyingLabels {
			value := sample.Metric[label]
			if value == "" {
				continue
			}
			if idValues[label] == nil {
				idValues[label] = map[string]struct{}{}
			}
			idValues[label][value] = struct{}{}
		}
	}
	if len(idValues) == 0 {
		return "", nil
	}
	matchers := make([]string, 0, len(selectorMatchers)+len(idValues)+1)
	for label, values := range idValues {
		parts := make([]string, 0, len(values))
		for value := range values {
			parts = append(parts, regexp.QuoteMeta(value))
		}
		sort.Strings(parts)
		matcher, err := promlabels.NewMatcher(promlabels.MatchRegexp, label, strings.Join(parts, "|"))
		if err != nil {
			return "", err
		}
		matchers = append(matchers, matcher.String())
	}
	nameMatchers := []*promlabels.Matcher{}
	for _, matcher := range selectorMatchers {
		if matcher == nil {
			continue
		}
		if matcher.Name == commonmodel.MetricNameLabel {
			nameMatchers = append(nameMatchers, matcher)
		} else {
			matchers = append(matchers, matcher.String())
		}
	}
	for _, matcher := range effectiveInfoNameMatchers(nameMatchers) {
		matchers = append(matchers, matcher.String())
	}
	sort.Strings(matchers)
	return "{" + strings.Join(matchers, ",") + "}", nil
}

func effectiveInfoMetricNames(samples []model.InstantSample) map[string]struct{} {
	names := map[string]struct{}{}
	for _, sample := range samples {
		if name := sample.Metric[commonmodel.MetricNameLabel]; name != "" {
			names[name] = struct{}{}
		}
	}
	if len(names) == 0 {
		names[defaultInfoMetricName] = struct{}{}
	}
	return names
}

func effectiveInfoNameMatchers(matchers []*promlabels.Matcher) []*promlabels.Matcher {
	positive := false
	cloned := make([]*promlabels.Matcher, 0, len(matchers))
	for _, matcher := range matchers {
		if matcher == nil || matcher.Name != commonmodel.MetricNameLabel {
			continue
		}
		if matcher.Type == promlabels.MatchEqual || matcher.Type == promlabels.MatchRegexp {
			positive = true
		}
		cloned = append(cloned, promlabels.MustNewMatcher(matcher.Type, matcher.Name, matcher.Value))
	}
	if positive {
		return cloned
	}
	if len(cloned) > 0 {
		return append([]*promlabels.Matcher{promlabels.MustNewMatcher(promlabels.MatchRegexp, commonmodel.MetricNameLabel, ".+_info")}, cloned...)
	}
	return []*promlabels.Matcher{promlabels.MustNewMatcher(promlabels.MatchEqual, commonmodel.MetricNameLabel, defaultInfoMetricName)}
}

func infoDataLabelMatchers(matchers []*promlabels.Matcher) map[string][]*promlabels.Matcher {
	out := map[string][]*promlabels.Matcher{}
	for _, matcher := range matchers {
		if matcher == nil || matcher.Name == commonmodel.MetricNameLabel {
			continue
		}
		out[matcher.Name] = append(out[matcher.Name], promlabels.MustNewMatcher(matcher.Type, matcher.Name, matcher.Value))
	}
	return out
}

func infoSampleKey(infoName string, metric map[string]string) string {
	key := make([]string, 0, len(infoIdentifyingLabels)+1)
	key = append(key, infoName)
	for _, label := range infoIdentifyingLabels {
		key = append(key, label+"="+metric[label])
	}
	return strings.Join(key, "|")
}

func matchesAllNameMatchers(name string, matchers []*promlabels.Matcher) bool {
	if len(matchers) == 0 {
		return false
	}
	for _, matcher := range matchers {
		if !matcher.Matches(name) {
			return false
		}
	}
	return true
}

func allMatchersMatch(value string, matchers []*promlabels.Matcher) bool {
	for _, matcher := range matchers {
		if !matcher.Matches(value) {
			return false
		}
	}
	return true
}

func allDataLabelMatchersMatchEmpty(matchers map[string][]*promlabels.Matcher) bool {
	for _, labelMatchers := range matchers {
		if !allMatchersMatch("", labelMatchers) {
			return false
		}
	}
	return true
}

func isInfoIdentifyingLabel(label string) bool {
	for _, candidate := range infoIdentifyingLabels {
		if label == candidate {
			return true
		}
	}
	return false
}

func ValidateInfoSelectorMatchers(matchers []*promlabels.Matcher) error {
	for _, matcher := range matchers {
		if matcher == nil {
			continue
		}
		if matcher.Name != commonmodel.MetricNameLabel && !commonmodel.UTF8Validation.IsValidLabelName(matcher.Name) {
			return fmt.Errorf("invalid label name in info(): %s", matcher.Name)
		}
	}
	return nil
}
