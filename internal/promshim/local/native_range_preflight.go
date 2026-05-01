package local

import (
	"context"
	"time"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/native"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/storage"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

// ApplyNativeRangePreflight resolves bounded metadata probes for native range
// chunk decisions that deliberately defer the small-vs-broad selector question
// until a ClickHouse client is available. Failures keep the safe chunked plan.
func ApplyNativeRangePreflight(ctx context.Context, client *storage.Client, cfg storage.QueryConfig, plan Plan) Plan {
	chunked, ok := plan.(*chunkedRangePlan)
	if !ok || chunked == nil || chunked.Decision == nil || chunked.Decision.Policy != "bounded_series_preflight" {
		return plan
	}
	decision := cloneNativeRangeChunkDecision(chunked.Decision)
	chunked.Decision = decision
	selector, ok := nativeRangePreflightSelector(chunked.Child)
	if !ok || client == nil || decision.PreflightThreshold <= 0 {
		decision.PreflightError = "preflight selector unavailable"
		annotateNativeRangeChunkDecision(chunked.Child, decision)
		return chunked
	}
	requiredStartMS, requiredEndMS, ok := nativeRangePreflightBounds(chunked.Child)
	if !ok {
		decision.PreflightError = "preflight required input bounds unavailable"
		annotateNativeRangeChunkDecision(chunked.Child, decision)
		return chunked
	}
	probeCtx := ctx
	if timeout := nativeRangePreflightTimeoutFromDecision(decision); timeout > 0 {
		var cancel context.CancelFunc
		probeCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	sql, params, err := storage.BuildCappedSelectorStatsQuery(cfg, selector, requiredStartMS, requiredEndMS, decision.PreflightThreshold)
	if err != nil {
		decision.PreflightError = err.Error()
		annotateNativeRangeChunkDecision(chunked.Child, decision)
		return chunked
	}
	settings := nativeRangePreflightSettings(decision)
	stats, err := client.QuerySelectorStats(probeCtx, storage.QueryRequest{SQL: sql, Params: params, Settings: settings, Purpose: storage.QueryPurposeSelectorStats})
	if err != nil {
		decision.PreflightError = err.Error()
		annotateNativeRangeChunkDecision(chunked.Child, decision)
		return chunked
	}
	decision.PreflightMatched = stats.MatchedSeries
	decision.PreflightCapped = stats.MatchedSeries > decision.PreflightThreshold
	if decision.PreflightCapped {
		decision.Reason = "bounded series preflight exceeded threshold; keeping safe native range chunking"
		annotateNativeRangeChunkDecision(chunked.Child, decision)
		return chunked
	}
	decision.Chunked = false
	decision.ChunkPointsPerSeries = 0
	decision.Reason = "bounded series preflight stayed under threshold; native range chunking skipped"
	annotateNativeRangeChunkDecision(chunked.Child, decision)
	return chunked.Child
}

func cloneNativeRangeChunkDecision(decision *nativeRangeChunkDecision) *nativeRangeChunkDecision {
	if decision == nil {
		return nil
	}
	cloned := *decision
	return &cloned
}

func nativeRangePreflightTimeoutFromDecision(decision *nativeRangeChunkDecision) time.Duration {
	if decision == nil || decision.PreflightTimeoutMS <= 0 {
		return 0
	}
	return time.Duration(decision.PreflightTimeoutMS) * time.Millisecond
}

func nativeRangePreflightSettings(decision *nativeRangeChunkDecision) map[string]any {
	settings := map[string]any{"max_threads": 1}
	if decision != nil {
		if decision.PreflightTimeoutMS > 0 {
			settings["max_execution_time"] = float64(decision.PreflightTimeoutMS) / 1000
		}
		if decision.PreflightMaxMemoryBytes > 0 {
			settings["max_memory_usage"] = decision.PreflightMaxMemoryBytes
		}
	}
	return settings
}

func nativeRangePreflightBounds(plan Plan) (int64, int64, bool) {
	if nativePlan, ok := plan.(*nativeSubtreePlan); ok && nativePlan != nil && nativePlan.OptimizationReport != nil {
		start := nativePlan.OptimizationReport.RequiredInputStartMS
		end := nativePlan.OptimizationReport.RequiredInputEndMS
		if start != 0 && end != 0 {
			return start, end, true
		}
	}
	return 0, 0, false
}

func nativeRangePreflightSelector(plan Plan) (storage.SelectorSource, bool) {
	nativePlan, ok := plan.(*nativeSubtreePlan)
	if !ok || nativePlan == nil {
		return storage.SelectorSource{}, false
	}
	expr := firstSelectorExpr(nativePlan.rootLogicalNode())
	selector, err := native.BuildSelectorSource(expr)
	if err != nil || selector == nil {
		return storage.SelectorSource{}, false
	}
	return storage.SelectorSource{
		Kind:       storage.SelectorKind(selector.Kind),
		MetricName: selector.MetricName,
		Matchers:   cloneLabelMatchers(selector.Matchers),
		NeedTags:   false,
		LookbackMS: selector.Lookback.Milliseconds(),
		OffsetMS:   selector.Offset.Milliseconds(),
	}, true
}

func firstSelectorExpr(node logical.Node) parser.Expr {
	switch n := node.(type) {
	case *logical.AggregationPlan:
		return firstSelectorExpr(n.Child)
	case *logical.RangeFunctionPlan:
		return firstSelectorExpr(n.Child)
	case *logical.UnaryPlan:
		return firstSelectorExpr(n.Child)
	case *logical.BinaryPlan:
		if expr := firstSelectorExpr(n.LHS); expr != nil {
			return expr
		}
		return firstSelectorExpr(n.RHS)
	case *logical.VectorPlan:
		return firstSelectorExpr(n.Child)
	case *logical.LeafExprPlan:
		return firstSelectorParserExpr(n.Expr)
	default:
		return nil
	}
}

func firstSelectorParserExpr(expr parser.Expr) parser.Expr {
	switch n := expr.(type) {
	case *parser.MatrixSelector, *parser.VectorSelector:
		return n
	case *parser.Call:
		for _, arg := range n.Args {
			if found := firstSelectorParserExpr(arg); found != nil {
				return found
			}
		}
	case *parser.AggregateExpr:
		return firstSelectorParserExpr(n.Expr)
	case *parser.ParenExpr:
		return firstSelectorParserExpr(n.Expr)
	case *parser.UnaryExpr:
		return firstSelectorParserExpr(n.Expr)
	case *parser.BinaryExpr:
		if found := firstSelectorParserExpr(n.LHS); found != nil {
			return found
		}
		return firstSelectorParserExpr(n.RHS)
	}
	return nil
}

func cloneLabelMatchers(matchers []*labels.Matcher) []*labels.Matcher {
	if len(matchers) == 0 {
		return nil
	}
	cloned := make([]*labels.Matcher, 0, len(matchers))
	for _, matcher := range matchers {
		if matcher == nil {
			continue
		}
		cloned = append(cloned, labels.MustNewMatcher(matcher.Type, matcher.Name, matcher.Value))
	}
	return cloned
}
