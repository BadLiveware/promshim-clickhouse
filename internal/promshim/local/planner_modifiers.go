package local

import (
	"math"
	"time"

	logicalpkg "github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/model"
	"github.com/prometheus/prometheus/promql/parser"
)

// clickHouseRangePadding compensates for ClickHouse's (t-range, t] range selector
// semantics versus Prometheus's [t-range, t]: padding every matrix/subquery range
// by 1ms pulls the left-boundary sample back into the window without perturbing
// aligned scrapes on the right. See harness P1 findings for the root cause.
const clickHouseRangePadding = time.Millisecond

func resolveDelegatedPromQL(expr parser.Expr, params EvalParams, noStepSubqueryInterval time.Duration) (string, error) {
	parsed, err := logicalpkg.ParseExpression(expr.String())
	if err != nil {
		return "", NewExecutionErrorf("re-parsing expression for delegation rewrite: %v", err)
	}
	if containsStartEndAtModifier(parsed) {
		resolveStartEndAtModifierRecursive(parsed, params)
	}
	fillNoStepSubqueryIntervals(parsed, noStepSubqueryInterval)
	padDelegatedRanges(parsed, clickHouseRangePadding)
	return parsed.String(), nil
}

// fillNoStepSubqueryIntervals makes the no-step subquery default explicit in
// delegated PromQL text. Prometheus fills a missing subquery step with the
// server-side default evaluation interval; without this rewrite the empty
// step would survive serialization (`up[15m:]`) and ClickHouse would apply
// its own default instead of promshim's configured one. Explicit steps are
// never overridden.
func fillNoStepSubqueryIntervals(expr parser.Expr, interval time.Duration) {
	if interval <= 0 {
		interval = DefaultEvaluationInterval
	}
	switch node := expr.(type) {
	case *parser.SubqueryExpr:
		if node.Step <= 0 {
			node.Step = interval
		}
		fillNoStepSubqueryIntervals(node.Expr, interval)
	case *parser.Call:
		for _, arg := range node.Args {
			fillNoStepSubqueryIntervals(arg, interval)
		}
	case *parser.AggregateExpr:
		if node.Param != nil {
			fillNoStepSubqueryIntervals(node.Param, interval)
		}
		fillNoStepSubqueryIntervals(node.Expr, interval)
	case *parser.BinaryExpr:
		fillNoStepSubqueryIntervals(node.LHS, interval)
		fillNoStepSubqueryIntervals(node.RHS, interval)
	case *parser.UnaryExpr:
		fillNoStepSubqueryIntervals(node.Expr, interval)
	case *parser.ParenExpr:
		fillNoStepSubqueryIntervals(node.Expr, interval)
	case *parser.StepInvariantExpr:
		fillNoStepSubqueryIntervals(node.Expr, interval)
	}
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

func resolveStartEndAtModifierRecursive(expr parser.Expr, params EvalParams) {
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

func resolveSelectorStartEndAtModifier(selector *parser.VectorSelector, params EvalParams) {
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

func resolveSubqueryStartEndAtModifier(subquery *parser.SubqueryExpr, params EvalParams) {
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

func resolveStartEndMillis(token parser.ItemType, params EvalParams) *int64 {
	var resolved int64
	switch token {
	case parser.START:
		if params.Mode == EvalModeRange {
			resolved = params.Start.UnixMilli()
		} else {
			resolved = params.EvaluationTime.UnixMilli()
		}
	case parser.END:
		if params.Mode == EvalModeRange {
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
