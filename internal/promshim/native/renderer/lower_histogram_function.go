package renderer

import (
	"fmt"

	logicalpkg "github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/native/physical"
	"github.com/prometheus/prometheus/promql/parser"
)

// lowerHistogramFunction lowers any of the three histogram-function plan
// kinds (HistogramQuantilePlan, HistogramFractionPlan,
// HistogramQuantilesPlan) to a RenderedQuery by delegating to
// renderHistogramFunctionLogical in histogram_logical.go.
//
// Hierarchical fallback: if renderHistogramFunctionLogical returns
// errUnsupportedLowerNode the caller falls back to the next execution tier.
//
// Supported functions: histogram_quantile, histogram_fraction,
// histogram_quantiles.
func lowerHistogramFunction(ctx LoweringCtx, n logicalpkg.Node) (RenderedQuery, error) {
	if n == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: lowerHistogramFunction called with nil")
	}
	if ctx.Analysis == nil || ctx.Analysis.InfoFor(n) == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: histogram function missing logical analysis")
	}
	histogramFallbackDecision, annotateFallbackDecision := histogramFallbackOrDecision(n)
	semanticsDecision, annotateSemanticsDecision := histogramSemanticsPreservationDecision(n, histogramFallbackDecision)
	rendered, err := renderHistogramFunctionLogical(ctx.Config, ctx.Analysis, ctx.NativeAnalysis, n, ctx.Params, ctx.OptimizationReport)
	if err != nil {
		return RenderedQuery{}, err
	}
	if annotateFallbackDecision {
		rendered.ExtraPhysicalDecisions = appendRenderedQueryPhysicalDecisions(rendered.ExtraPhysicalDecisions, histogramFallbackDecision)
	}
	if annotateSemanticsDecision {
		rendered.ExtraPhysicalDecisions = appendRenderedQueryPhysicalDecisions(rendered.ExtraPhysicalDecisions, semanticsDecision)
	}
	return finalizeRenderedFragment(rendered)
}

func histogramSemanticsPreservationDecision(node logicalpkg.Node, fallbackDecision physical.Decision) (physical.Decision, bool) {
	decision := physical.Decision{Kind: "histogram_semantics_preservation", Strategy: "not_applicable"}
	qt, ok := node.(*logicalpkg.HistogramQuantilePlan)
	if !ok {
		return decision, false
	}
	// Only emit semantics decision when histogram is recognized (has a child expression)
	if qt.Child == nil {
		return decision, false
	}
	// Check if fallback or is recognized
	if fallbackDecision.Strategy == "recognized" {
		decision.Strategy = "preserved"
		decision.Reason = "histogram native/classic fallback renders through standard or semantics preserving labelset compatibility"
		decision.Guards = []string{"histogram_quantile", "fallback_or", "standard_set_op_semantics", "le_labelset_compatibility"}
		return decision, true
	}
	// Check if child is a binary or operator (unrecognized fallback)
	if childBin, ok := qt.Child.(*logicalpkg.BinaryPlan); ok && childBin.Op == parser.LOR {
		decision.Strategy = "preserved"
		decision.Reason = "histogram or expression renders through native binary-or lowering preserving Prometheus set operators"
		decision.Guards = []string{"histogram_quantile", "binary_or_lowering", "native_vector_join_path"}
		return decision, true
	}
	// For direct (non-or) histogram inputs, semantics are trivially preserved
	decision.Strategy = "preserved"
	decision.Reason = "histogram_quantile with direct input preserves semantics through standard lowering"
	decision.Guards = []string{"histogram_quantile", "direct_input", "standard_lowering"}
	return decision, true
}

func histogramFallbackOrDecision(node logicalpkg.Node) (physical.Decision, bool) {
	decision := physical.Decision{Kind: "histogram_fallback_or", Strategy: "not_recognized"}
	qt, ok := node.(*logicalpkg.HistogramQuantilePlan)
	if !ok {
		return decision, false
	}
	orExpr, ok := qt.Child.(*logicalpkg.BinaryPlan)
	if !ok || orExpr.Op != parser.LOR {
		decision.Reason = "histogram_quantile child is not a literal-or expression"
		return decision, true
	}
	_, _, ok = classifyHistogramOrBranches(orExpr.LHS, orExpr.RHS)
	if !ok {
		decision.Reason = "histogram-or branches could not be classified as native / classic _bucket sum"
		decision.Rejected = []physical.Alternative{{Strategy: "recognized", Reason: decision.Reason}}
		return decision, true
	}
	decision.Strategy = "recognized"
	decision.Reason = "recognized native/classic histogram fallback or shape"
	decision.Guards = []string{"histogram_quantile", "native_or_classic", "compatible_sum_rate_structure"}
	return decision, true
}

func repeatedHistogramQuantileDecision(node logicalpkg.Node) (physical.Decision, bool) {
	decision := physical.Decision{Kind: "histogram_repeated_quantile", Strategy: "not_recognized"}
	items := collectSiblingHistogramQuantileInputs(node)
	if len(items) < 2 {
		return decision, false
	}
	// group by child expression string
	groups := map[string][]float64{}
	for _, item := range items {
		key := describedNodeExprString(item.Child)
		if key == "" {
			continue
		}
		groups[key] = append(groups[key], item.Quantile)
	}
	for _, quantiles := range groups {
		if len(quantiles) >= 2 {
			decision.Strategy = "recognized"
			decision.Reason = "recognized repeated histogram_quantile input across sibling expressions"
			decision.Guards = []string{"histogram_quantile", "shared_input_expression"}
			return decision, true
		}
	}
	decision.Reason = "no sibling histogram_quantile calls sharing the same input found"
	return decision, true
}

type histogramQuantileItem struct {
	Quantile float64
	Child    logicalpkg.Node
}

func collectSiblingHistogramQuantileInputs(node logicalpkg.Node) []histogramQuantileItem {
	if node == nil {
		return nil
	}
	switch n := node.(type) {
	case *logicalpkg.BinaryPlan:
		if isArithmeticOrComparisonOp(n.Op) {
			return append(
				collectSiblingHistogramQuantileInputs(n.LHS),
				collectSiblingHistogramQuantileInputs(n.RHS)...,
			)
		}
		return nil
	case *logicalpkg.HistogramQuantilePlan:
		return []histogramQuantileItem{{Quantile: n.Quantile, Child: n.Child}}
	case *logicalpkg.AggregationPlan:
		return collectSiblingHistogramQuantileInputs(n.Child)
	default:
		return nil
	}
}

func isArithmeticOrComparisonOp(op parser.ItemType) bool {
	switch op {
	case parser.ADD, parser.SUB, parser.MUL, parser.DIV,
		parser.EQLC, parser.NEQ, parser.GTR, parser.LSS, parser.GTE, parser.LTE:
		return true
	default:
		return false
	}
}

func describedNodeExprString(node logicalpkg.Node) string {
	if node == nil {
		return ""
	}
	described, ok := node.(interface{ ExprString() string })
	if !ok {
		return ""
	}
	return described.ExprString()
}

func classifyHistogramOrBranches(lhs, rhs logicalpkg.Node) (native, classic *logicalpkg.AggregationPlan, ok bool) {
	lhsAgg, lhsOK := clsAggregationPlan(lhs, parser.SUM)
	rhsAgg, rhsOK := clsAggregationPlan(rhs, parser.SUM)
	if !lhsOK || !rhsOK {
		return nil, nil, false
	}
	lhsHasLE := hasGroupingLabel(lhsAgg, "le")
	rhsHasLE := hasGroupingLabel(rhsAgg, "le")
	if lhsHasLE && !rhsHasLE {
		return rhsAgg, lhsAgg, true
	}
	if rhsHasLE && !lhsHasLE {
		return lhsAgg, rhsAgg, true
	}
	return nil, nil, false
}

func clsAggregationPlan(node logicalpkg.Node, op parser.ItemType) (*logicalpkg.AggregationPlan, bool) {
	n, ok := node.(*logicalpkg.AggregationPlan)
	return n, ok && n.Op == op
}

func hasGroupingLabel(agg *logicalpkg.AggregationPlan, label string) bool {
	if agg == nil {
		return false
	}
	for _, g := range agg.Grouping {
		if g == label {
			return true
		}
	}
	return false
}
