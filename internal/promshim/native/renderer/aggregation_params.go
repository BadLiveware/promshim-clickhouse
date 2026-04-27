package renderer

import (
	"os"

	logicalpkg "github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/native"
	"github.com/prometheus/prometheus/promql/parser"
)

const DisableNativeAggregationLabelProjectionEnv = "PROM_SHIM_DISABLE_NATIVE_AGGREGATION_LABEL_PROJECTION"

func aggregationChildRenderParams(n *logicalpkg.AggregationPlan, params RenderParams) RenderParams {
	if n == nil || nativeAggregationLabelProjectionDisabled() || !aggregationChildAllowsLabelProjection(n.Child) {
		return params
	}
	out := params
	if native.IsSelectionNativeAggregation(n.Op) || n.Op == parser.COUNT_VALUES || n.Without {
		out.RequireFullTags = true
		out.RequiredTagLabels = nil
		return out
	}
	if len(n.Grouping) > 0 {
		out.RequireFullTags = false
		out.RequiredTagLabels = append([]string(nil), n.Grouping...)
	}
	return out
}

func nativeAggregationLabelProjectionDisabled() bool {
	switch os.Getenv(DisableNativeAggregationLabelProjectionEnv) {
	case "1", "true", "TRUE", "yes", "YES", "on", "ON":
		return true
	default:
		return false
	}
}

func aggregationChildAllowsLabelProjection(n logicalpkg.Node) bool {
	switch node := n.(type) {
	case *logicalpkg.LeafExprPlan:
		if node == nil || node.Expr == nil || node.Expr.Type() != parser.ValueTypeVector {
			return false
		}
		_, isRangeSelector := node.Expr.(*parser.MatrixSelector)
		return !isRangeSelector
	case *logicalpkg.UnaryPlan:
		return aggregationChildAllowsLabelProjection(node.Child)
	case *logicalpkg.RoundPlan:
		return aggregationChildAllowsLabelProjection(node.Child)
	case *logicalpkg.ScalarConvertPlan:
		return aggregationChildAllowsLabelProjection(node.Child)
	case *logicalpkg.PointwiseFunctionPlan:
		return aggregationChildAllowsLabelProjection(node.Child)
	default:
		return false
	}
}
