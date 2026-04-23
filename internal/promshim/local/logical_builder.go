package local

import (
	"ch-observability/internal/promshim/logical"
	"github.com/prometheus/prometheus/promql/parser"
)

type logicalPlan = logical.Node

type logicalLeafExprPlan = logical.LeafExprPlan

type logicalScalarLiteralPlan = logical.ScalarLiteralPlan

type logicalUnaryPlan = logical.UnaryPlan

type logicalBinaryPlan = logical.BinaryPlan

type logicalAggregationPlan = logical.AggregationPlan

type logicalHistogramQuantilePlan = logical.HistogramQuantilePlan

type logicalHistogramFractionPlan = logical.HistogramFractionPlan

type logicalHistogramProjectionPlan = logical.HistogramProjectionPlan

type logicalHistogramQuantilesPlan = logical.HistogramQuantilesPlan

type logicalRangeFunctionPlan = logical.RangeFunctionPlan

type logicalVectorPlan = logical.VectorPlan

type logicalRoundPlan = logical.RoundPlan

type logicalSortPlan = logical.SortPlan

type logicalScalarConvertPlan = logical.ScalarConvertPlan

type logicalInfoPlan = logical.InfoPlan

type logicalPointwiseFunctionPlan = logical.PointwiseFunctionPlan

type logicalScalarBuiltinPlan = logical.ScalarBuiltinPlan

type logicalRatePlan = logical.RatePlan

type logicalIncreasePlan = logical.IncreasePlan

type logicalDeltaPlan = logical.DeltaPlan

type logicalChangesPlan = logical.ChangesPlan

type logicalDerivPlan = logical.DerivPlan

type logicalQuantileOverTimePlan = logical.QuantileOverTimePlan

type logicalAbsentPlan = logical.AbsentPlan

type logicalAbsentOverTimePlan = logical.AbsentOverTimePlan

type logicalSubqueryPlan = logical.SubqueryPlan

type logicalLabelReplacePlan = logical.LabelReplacePlan

type logicalLabelJoinPlan = logical.LabelJoinPlan

// BuildLogicalPlan converts a Prometheus expression to the promshim logical IR.
//
// Deprecated: use logical.ToLogical instead.
func BuildLogicalPlan(expr parser.Expr) (logicalPlan, error) {
	return logical.ToLogical(expr)
}
