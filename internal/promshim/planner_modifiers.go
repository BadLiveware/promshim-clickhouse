package promshim

import (
	"math"
	"time"

	"ch-observability/internal/promshim/model"
	planpkg "ch-observability/internal/promshim/plan"
	"github.com/prometheus/prometheus/promql/parser"
)

// clickHouseRangePadding compensates for ClickHouse's (t-range, t] range selector
// semantics versus Prometheus's [t-range, t]: padding every matrix/subquery range
// by 1ms pulls the left-boundary sample back into the window without perturbing
// aligned scrapes on the right. See harness P1 findings for the root cause.
const clickHouseRangePadding = time.Millisecond

func resolveDelegatedPromQL(expr parser.Expr, params evalParams) (string, error) {
	parsed, err := planpkg.ParseExpression(expr.String())
	if err != nil {
		return "", newExecutionErrorf("re-parsing expression for delegation rewrite: %v", err)
	}
	if containsStartEndAtModifier(parsed) {
		resolveStartEndAtModifierRecursive(parsed, params)
	}
	padDelegatedRanges(parsed, clickHouseRangePadding)
	return parsed.String(), nil
}

func padDelegatedRanges(expr parser.Expr, delta time.Duration) {
	switch node := expr.(type) {
	case *parser.MatrixSelector:
		node.Range += delta
	case *parser.SubqueryExpr:
		node.Range += delta
		padDelegatedRanges(node.Expr, delta)
	case *parser.Call:
		for _, arg := range node.Args {
			padDelegatedRanges(arg, delta)
		}
	case *parser.AggregateExpr:
		if node.Param != nil {
			padDelegatedRanges(node.Param, delta)
		}
		padDelegatedRanges(node.Expr, delta)
	case *parser.BinaryExpr:
		padDelegatedRanges(node.LHS, delta)
		padDelegatedRanges(node.RHS, delta)
	case *parser.UnaryExpr:
		padDelegatedRanges(node.Expr, delta)
	case *parser.ParenExpr:
		padDelegatedRanges(node.Expr, delta)
	case *parser.StepInvariantExpr:
		padDelegatedRanges(node.Expr, delta)
	}
}

func containsStartEndAtModifier(expr parser.Expr) bool {
	switch node := expr.(type) {
	case *parser.VectorSelector:
		return node.StartOrEnd == parser.START || node.StartOrEnd == parser.END
	case *parser.MatrixSelector:
		return containsStartEndAtModifier(node.VectorSelector)
	case *parser.SubqueryExpr:
		if node.StartOrEnd == parser.START || node.StartOrEnd == parser.END {
			return true
		}
		return containsStartEndAtModifier(node.Expr)
	case *parser.Call:
		for _, arg := range node.Args {
			if containsStartEndAtModifier(arg) {
				return true
			}
		}
		return false
	case *parser.AggregateExpr:
		if node.Param != nil && containsStartEndAtModifier(node.Param) {
			return true
		}
		return containsStartEndAtModifier(node.Expr)
	case *parser.BinaryExpr:
		return containsStartEndAtModifier(node.LHS) || containsStartEndAtModifier(node.RHS)
	case *parser.UnaryExpr:
		return containsStartEndAtModifier(node.Expr)
	case *parser.ParenExpr:
		return containsStartEndAtModifier(node.Expr)
	case *parser.StepInvariantExpr:
		return containsStartEndAtModifier(node.Expr)
	default:
		return false
	}
}

func resolveStartEndAtModifierRecursive(expr parser.Expr, params evalParams) {
	switch node := expr.(type) {
	case *parser.VectorSelector:
		resolveSelectorStartEndAtModifier(node, params)
	case *parser.MatrixSelector:
		if selector, ok := node.VectorSelector.(*parser.VectorSelector); ok {
			resolveSelectorStartEndAtModifier(selector, params)
		}
		resolveStartEndAtModifierRecursive(node.VectorSelector, params)
	case *parser.SubqueryExpr:
		resolveSubqueryStartEndAtModifier(node, params)
		resolveStartEndAtModifierRecursive(node.Expr, params)
	case *parser.Call:
		for _, arg := range node.Args {
			resolveStartEndAtModifierRecursive(arg, params)
		}
	case *parser.AggregateExpr:
		if node.Param != nil {
			resolveStartEndAtModifierRecursive(node.Param, params)
		}
		resolveStartEndAtModifierRecursive(node.Expr, params)
	case *parser.BinaryExpr:
		resolveStartEndAtModifierRecursive(node.LHS, params)
		resolveStartEndAtModifierRecursive(node.RHS, params)
	case *parser.UnaryExpr:
		resolveStartEndAtModifierRecursive(node.Expr, params)
	case *parser.ParenExpr:
		resolveStartEndAtModifierRecursive(node.Expr, params)
	case *parser.StepInvariantExpr:
		resolveStartEndAtModifierRecursive(node.Expr, params)
	}
}

func resolveSelectorStartEndAtModifier(selector *parser.VectorSelector, params evalParams) {
	if selector == nil {
		return
	}
	resolved := resolveStartEndMillis(selector.StartOrEnd, params)
	if resolved == nil {
		return
	}
	selector.Timestamp = resolved
	selector.StartOrEnd = 0
}

func resolveSubqueryStartEndAtModifier(subquery *parser.SubqueryExpr, params evalParams) {
	if subquery == nil {
		return
	}
	resolved := resolveStartEndMillis(subquery.StartOrEnd, params)
	if resolved == nil {
		return
	}
	subquery.Timestamp = resolved
	subquery.StartOrEnd = 0
}

func resolveStartEndMillis(token parser.ItemType, params evalParams) *int64 {
	var resolved int64
	switch token {
	case parser.START:
		if params.Mode == evalModeRange {
			resolved = params.Start.UnixMilli()
		} else {
			resolved = params.EvaluationTime.UnixMilli()
		}
	case parser.END:
		if params.Mode == evalModeRange {
			resolved = params.End.UnixMilli()
		} else {
			resolved = params.EvaluationTime.UnixMilli()
		}
	default:
		return nil
	}
	value := resolved
	return &value
}

func cloneInt64Pointer(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneFloat64Pointer(value *float64) *float64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneFloat64Pointers(values []*float64) []*float64 {
	if len(values) == 0 {
		return nil
	}
	out := make([]*float64, 0, len(values))
	for _, value := range values {
		out = append(out, cloneFloat64Pointer(value))
	}
	return out
}

func defaultSubqueryStep(params evalParams) time.Duration {
	if params.Step > 0 {
		return params.Step
	}
	return time.Minute
}

func isBareSelectorExpr(expr parser.Expr) bool {
	switch expr.(type) {
	case *parser.VectorSelector, *parser.MatrixSelector:
		return true
	default:
		return false
	}
}

func dropNaNInstantSamples(samples []model.InstantSample) []model.InstantSample {
	if len(samples) == 0 {
		return samples
	}
	out := samples[:0]
	for _, s := range samples {
		if math.IsNaN(s.Value) {
			continue
		}
		out = append(out, s)
	}
	return out
}

func dropNaNRangePoints(series []model.RangeSeries) []model.RangeSeries {
	if len(series) == 0 {
		return series
	}
	out := series[:0]
	for _, s := range series {
		filtered := s.Values[:0]
		for _, p := range s.Values {
			if math.IsNaN(p.Value) {
				continue
			}
			filtered = append(filtered, p)
		}
		if len(filtered) == 0 {
			continue
		}
		s.Values = filtered
		out = append(out, s)
	}
	return out
}
