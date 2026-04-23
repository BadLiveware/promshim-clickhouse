package logical

import (
	"time"

	"github.com/prometheus/prometheus/promql/parser"
)

// combineTimeRequirements returns the element-wise maximum of a set
// of TimeRequirements. Lookback and Offset take the max (because a
// parent needs whichever child reads the furthest); NeedsSubqueryStepGrid
// is a monotone OR.
func combineTimeRequirements(requirements ...TimeRequirements) TimeRequirements {
	combined := TimeRequirements{}
	for _, requirement := range requirements {
		if requirement.Lookback > combined.Lookback {
			combined.Lookback = requirement.Lookback
		}
		if requirement.Offset > combined.Offset {
			combined.Offset = requirement.Offset
		}
		combined.NeedsSubqueryStepGrid = combined.NeedsSubqueryStepGrid || requirement.NeedsSubqueryStepGrid
	}
	return combined
}

// leafTimeRequirements derives the temporal slice a selector-backed
// leaf needs from storage. Non-selector leaves (scalar literals) need
// no samples and return a zero-value.
func leafTimeRequirements(expr parser.Expr) TimeRequirements {
	switch node := expr.(type) {
	case *parser.VectorSelector:
		return TimeRequirements{
			Lookback: DefaultInstantSelectorLookback,
			Offset:   absoluteDuration(node.OriginalOffset),
		}
	case *parser.MatrixSelector:
		offset := time.Duration(0)
		if v, ok := node.VectorSelector.(*parser.VectorSelector); ok {
			offset = absoluteDuration(v.OriginalOffset)
		}
		return TimeRequirements{Lookback: node.Range, Offset: offset}
	default:
		return TimeRequirements{}
	}
}

// subqueryTimeRequirements extends a child's requirements with the
// subquery's own range and offset. NeedsSubqueryStepGrid flips on
// because a subquery forces an upstream step grid.
func subqueryTimeRequirements(child TimeRequirements, subqueryRange, offset time.Duration) TimeRequirements {
	result := child
	result.Lookback += absoluteDuration(subqueryRange)
	result.Offset += absoluteDuration(offset)
	result.NeedsSubqueryStepGrid = true
	return result
}

func absoluteDuration(value time.Duration) time.Duration {
	if value < 0 {
		return -value
	}
	return value
}
