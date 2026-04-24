package renderer

import (
	logicalpkg "github.com/BadLiveware/promshim-ch/internal/promshim/logical"
)

// decideHistogramChildNarrowing returns the tag-narrowing decision for a
// histogram-function or histogram-projection child. If the child is a
// grouping aggregation (`sum by (...)` shape), the parent only needs the
// grouping labels from the underlying selector — requiredLabels are a
// fresh copy of the grouping labels, and requireFullTags is false.
// Otherwise (nil child, Without=true, or no grouping), the parent cannot
// narrow and requireFullTags is true.
//
// The returned slice is freshly allocated; callers own it and may hand it
// to RenderParams.
func decideHistogramChildNarrowing(child *logicalpkg.AggregationPlan) (requireFullTags bool, requiredLabels []string) {
	if child == nil || child.Without || len(child.Grouping) == 0 {
		return true, nil
	}
	labels := make([]string, len(child.Grouping))
	copy(labels, child.Grouping)
	return false, labels
}

// childAggregationUsesOnlyLETags reports whether the child aggregation
// groups by exactly ["le"] with Without=false. The histogram renderer
// uses this to pick the identity-tag rows shortcut that skips the full
// aggregation rewrite.
func childAggregationUsesOnlyLETags(child *logicalpkg.AggregationPlan) bool {
	if child == nil || child.Without {
		return false
	}
	return len(child.Grouping) == 1 && child.Grouping[0] == "le"
}
