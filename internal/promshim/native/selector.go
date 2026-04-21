package native

import (
	"fmt"
	"time"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

const defaultInstantSelectorLookback = 5 * time.Minute

type SelectorKind string

const (
	SelectorKindInstantVector SelectorKind = "instant_vector_selector"
	SelectorKindRangeVector   SelectorKind = "range_vector_selector"
)

type SelectorSource struct {
	Kind              SelectorKind
	MetricName        string
	Matchers          []*labels.Matcher
	InferredMatchers  []*labels.Matcher
	PushedMatchers    []*labels.Matcher
	RequireFullTags   bool
	RequiredTagLabels []string
	Lookback          time.Duration
	Offset            time.Duration
	Timestamp         *int64
	StartOrEnd        parser.ItemType
}

func buildSelectorSource(expr parser.Expr) (*SelectorSource, error) {
	switch node := expr.(type) {
	case *parser.VectorSelector:
		return &SelectorSource{
			Kind:            SelectorKindInstantVector,
			MetricName:      node.Name,
			Matchers:        cloneMatchers(node.LabelMatchers),
			RequireFullTags: true,
			Lookback:        defaultInstantSelectorLookback,
			Offset:          absoluteDuration(node.OriginalOffset),
			Timestamp:       cloneInt64Pointer(node.Timestamp),
			StartOrEnd:      node.StartOrEnd,
		}, nil
	case *parser.MatrixSelector:
		vectorSelector, ok := node.VectorSelector.(*parser.VectorSelector)
		if !ok {
			return nil, fmt.Errorf("matrix selector is missing its vector selector")
		}
		return &SelectorSource{
			Kind:            SelectorKindRangeVector,
			MetricName:      vectorSelector.Name,
			Matchers:        cloneMatchers(vectorSelector.LabelMatchers),
			RequireFullTags: true,
			Lookback:        node.Range,
			Offset:          absoluteDuration(vectorSelector.OriginalOffset),
			Timestamp:       cloneInt64Pointer(vectorSelector.Timestamp),
			StartOrEnd:      vectorSelector.StartOrEnd,
		}, nil
	default:
		return nil, nil
	}
}

func cloneSelectorSource(selector *SelectorSource) *SelectorSource {
	if selector == nil {
		return nil
	}
	return &SelectorSource{
		Kind:              selector.Kind,
		MetricName:        selector.MetricName,
		Matchers:          cloneMatchers(selector.Matchers),
		InferredMatchers:  cloneMatchers(selector.InferredMatchers),
		PushedMatchers:    cloneMatchers(selector.PushedMatchers),
		RequireFullTags:   selector.RequireFullTags,
		RequiredTagLabels: append([]string(nil), selector.RequiredTagLabels...),
		Lookback:          selector.Lookback,
		Offset:            selector.Offset,
		Timestamp:         cloneInt64Pointer(selector.Timestamp),
		StartOrEnd:        selector.StartOrEnd,
	}
}

func cloneMatchers(matchers []*labels.Matcher) []*labels.Matcher {
	if len(matchers) == 0 {
		return nil
	}
	out := make([]*labels.Matcher, 0, len(matchers))
	for _, matcher := range matchers {
		if matcher == nil {
			continue
		}
		out = append(out, labels.MustNewMatcher(matcher.Type, matcher.Name, matcher.Value))
	}
	return out
}
