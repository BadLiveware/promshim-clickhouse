package native

import (
	"fmt"
	"sort"

	"ch-observability/internal/promshim/storage"

	"github.com/prometheus/prometheus/model/labels"
)

// nativeInfoSelector returns the metric-name fast path and selector matchers
// needed to reproduce local info() fetch semantics natively. When the effective
// info metric selection collapses to a single equality matcher we keep using the
// dedicated MetricName field for efficient selector SQL; broader regex and
// negative-name forms flow through Matchers so the native join can fetch and
// merge multiple *_info metrics.
func nativeInfoSelector(matchers []*labels.Matcher) (string, []*labels.Matcher) {
	nameMatchers := effectiveNativeInfoNameMatchers(matchers)
	selectorMatchers := infoJoinDataLabelMatchers(matchers)
	if len(nameMatchers) == 1 && nameMatchers[0] != nil && nameMatchers[0].Type == labels.MatchEqual {
		return nameMatchers[0].Value, selectorMatchers
	}
	for _, matcher := range nameMatchers {
		if matcher == nil {
			continue
		}
		selectorMatchers = append(selectorMatchers, labels.MustNewMatcher(matcher.Type, matcher.Name, matcher.Value))
	}
	return "", selectorMatchers
}

func effectiveNativeInfoNameMatchers(matchers []*labels.Matcher) []*labels.Matcher {
	positive := false
	cloned := make([]*labels.Matcher, 0, len(matchers))
	for _, matcher := range matchers {
		if matcher == nil || matcher.Name != labels.MetricName {
			continue
		}
		if matcher.Type == labels.MatchEqual || matcher.Type == labels.MatchRegexp {
			positive = true
		}
		cloned = append(cloned, labels.MustNewMatcher(matcher.Type, matcher.Name, matcher.Value))
	}
	if positive {
		return cloned
	}
	if len(cloned) > 0 {
		return append([]*labels.Matcher{labels.MustNewMatcher(labels.MatchRegexp, labels.MetricName, ".+_info")}, cloned...)
	}
	return []*labels.Matcher{labels.MustNewMatcher(labels.MatchEqual, labels.MetricName, "target_info")}
}

func infoJoinDataLabelMatchers(matchers []*labels.Matcher) []*labels.Matcher {
	out := make([]*labels.Matcher, 0, len(matchers))
	for _, matcher := range matchers {
		if matcher == nil || matcher.Name == labels.MetricName {
			continue
		}
		out = append(out, labels.MustNewMatcher(matcher.Type, matcher.Name, matcher.Value))
	}
	return out
}

func infoJoinCopyLabelNames(matchers []*labels.Matcher) []string {
	if len(matchers) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0)
	for _, matcher := range matchers {
		if matcher == nil || matcher.Name == labels.MetricName {
			continue
		}
		if _, ok := seen[matcher.Name]; ok {
			continue
		}
		seen[matcher.Name] = struct{}{}
		out = append(out, matcher.Name)
	}
	sort.Strings(out)
	return out
}

func infoJoinDropUnmatched(matchers []*labels.Matcher) bool {
	for _, matcher := range matchers {
		if matcher == nil || matcher.Name == labels.MetricName {
			continue
		}
		if !matcher.Matches("") {
			return true
		}
	}
	return false
}

func NativePointwiseSourceTemplate(name string, paramNumbers []*float64) (string, bool) {
	switch name {
	case "abs":
		return "abs({value})", true
	case "ceil":
		return "ceil({value})", true
	case "floor":
		return "floor({value})", true
	case "sgn":
		return "sign({value})", true
	case "exp":
		return "exp({value})", true
	case "ln":
		return "log({value})", true
	case "log2":
		return "log2({value})", true
	case "log10":
		return "log10({value})", true
	case "sqrt":
		return "sqrt({value})", true
	case "sin":
		return "sin({value})", true
	case "cos":
		return "cos({value})", true
	case "tan":
		return "tan({value})", true
	case "asin":
		return "asin({value})", true
	case "acos":
		return "acos({value})", true
	case "atan":
		return "atan({value})", true
	case "sinh":
		return "sinh({value})", true
	case "cosh":
		return "cosh({value})", true
	case "tanh":
		return "tanh({value})", true
	case "asinh":
		return "asinh({value})", true
	case "acosh":
		return "acosh({value})", true
	case "atanh":
		return "atanh({value})", true
	case "deg":
		return "degrees({value})", true
	case "rad":
		return "radians({value})", true
	case "timestamp":
		return "toFloat64(toUnixTimestamp64Milli({timestamp})) / 1000.0", true
	case "minute":
		return "toFloat64(toMinute(toDateTime(toInt64({value}), 'UTC')))", true
	case "hour":
		return "toFloat64(toHour(toDateTime(toInt64({value}), 'UTC')))", true
	case "day_of_week":
		return "toFloat64(modulo(toDayOfWeek(toDateTime(toInt64({value}), 'UTC')), 7))", true
	case "day_of_month":
		return "toFloat64(toDayOfMonth(toDateTime(toInt64({value}), 'UTC')))", true
	case "day_of_year":
		return "toFloat64(toDayOfYear(toDateTime(toInt64({value}), 'UTC')))", true
	case "days_in_month":
		return "toFloat64(toDaysInMonth(toDateTime(toInt64({value}), 'UTC')))", true
	case "month":
		return "toFloat64(toMonth(toDateTime(toInt64({value}), 'UTC')))", true
	case "year":
		return "toFloat64(toYear(toDateTime(toInt64({value}), 'UTC')))", true
	case "clamp":
		if len(paramNumbers) == 2 && paramNumbers[0] != nil && paramNumbers[1] != nil {
			if *paramNumbers[0] > *paramNumbers[1] {
				return "", false
			}
			return fmt.Sprintf("greatest(%s, least(%s, {value}))", storage.NativeFloatLiteral(*paramNumbers[0]), storage.NativeFloatLiteral(*paramNumbers[1])), true
		}
	case "clamp_min":
		if len(paramNumbers) == 1 && paramNumbers[0] != nil {
			return fmt.Sprintf("greatest({value}, %s)", storage.NativeFloatLiteral(*paramNumbers[0])), true
		}
	case "clamp_max":
		if len(paramNumbers) == 1 && paramNumbers[0] != nil {
			return fmt.Sprintf("least({value}, %s)", storage.NativeFloatLiteral(*paramNumbers[0])), true
		}
	}
	return "", false
}
