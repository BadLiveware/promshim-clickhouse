package native

import (
	"time"

	"github.com/prometheus/prometheus/promql/parser"
)

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

func leafTimeRequirements(expr parser.Expr) TimeRequirements {
	switch node := expr.(type) {
	case *parser.VectorSelector:
		offset := absoluteDuration(node.OriginalOffset)
		return TimeRequirements{Lookback: offset, Offset: offset}
	case *parser.MatrixSelector:
		vector, _ := node.VectorSelector.(*parser.VectorSelector)
		offset := time.Duration(0)
		if vector != nil {
			offset = absoluteDuration(vector.OriginalOffset)
		}
		return TimeRequirements{Lookback: node.Range + offset, Offset: offset}
	default:
		return TimeRequirements{}
	}
}

func subqueryTimeRequirements(child TimeRequirements, subqueryRange, offset time.Duration) TimeRequirements {
	result := child
	result.Lookback += absoluteDuration(subqueryRange) + absoluteDuration(offset)
	if absOffset := absoluteDuration(offset); absOffset > result.Offset {
		result.Offset = absOffset
	}
	result.NeedsSubqueryStepGrid = true
	return result
}

func absoluteDuration(value time.Duration) time.Duration {
	if value < 0 {
		return -value
	}
	return value
}
