package promshim

import (
	nativeplan "github.com/BadLiveware/promshim-ch/internal/promshim/native"
	planpkg "github.com/BadLiveware/promshim-ch/internal/promshim/plan"
	"github.com/BadLiveware/promshim-ch/internal/promshim/storage"

	"github.com/prometheus/prometheus/promql/parser"
)

func rejectRangeModeFixedTemporalAnchor(ctx planContext, fragment *nativeplan.NativeFragment) bool {
	return ctx.Mode == evalModeRange && nativeplan.HasFixedTemporalAnchor(fragment)
}

func maybeBuildNativeRangeFunctionPlan(node *logicalRangeFunctionPlan, ctx planContext, analysis *nativeplan.Analysis) (queryPlan, bool, error) {
	if ctx.Mode == evalModeRange {
		info := analysis.InfoFor(node)
		if info == nil || info.Fragment == nil || info.Fragment.RangeFunction == nil {
			return nil, false, nil
		}
		if !nativeplan.IsSupportedNativeRangeModeForDirectSelector(info.Fragment) && !nativeplan.IsSupportedNativeRangeModeForWindowedArraysSubquery(info.Fragment) {
			return nil, false, nil
		}
		return maybeBuildNativeRangeLikePlanAllowRange(node, node.Func, node.ExprString(), ctx, analysis, true)
	}
	return maybeBuildNativeRangeLikePlan(node, node.Func, node.ExprString(), ctx, analysis)
}

func maybeBuildNativeRatePlan(node *logicalRatePlan, ctx planContext, analysis *nativeplan.Analysis) (queryPlan, bool, error) {
	if ctx.Mode == evalModeRange {
		info := analysis.InfoFor(node)
		if info == nil || info.Fragment == nil || info.Fragment.RangeFunction == nil {
			return nil, false, nil
		}
		if !nativeplan.IsSupportedNativeRangeModeForDirectSelector(info.Fragment) && !nativeplan.IsSupportedNativeRangeModeForCounterSubquery(info.Fragment) {
			return nil, false, nil
		}
		return maybeBuildNativeRangeLikePlanAllowRange(node, node.Func, node.ExprString(), ctx, analysis, true)
	}
	return maybeBuildNativeRangeLikePlan(node, node.Func, node.ExprString(), ctx, analysis)
}

func maybeBuildNativeIncreasePlan(node *logicalIncreasePlan, ctx planContext, analysis *nativeplan.Analysis) (queryPlan, bool, error) {
	if ctx.Mode == evalModeRange {
		info := analysis.InfoFor(node)
		if info == nil || info.Fragment == nil || info.Fragment.RangeFunction == nil {
			return nil, false, nil
		}
		if !nativeplan.IsSupportedNativeRangeModeForDirectSelector(info.Fragment) && !nativeplan.IsSupportedNativeRangeModeForCounterSubquery(info.Fragment) {
			return nil, false, nil
		}
		return maybeBuildNativeRangeLikePlanAllowRange(node, "increase", node.ExprString(), ctx, analysis, true)
	}
	return maybeBuildNativeRangeLikePlan(node, "increase", node.ExprString(), ctx, analysis)
}

func maybeBuildNativeDeltaPlan(node *logicalDeltaPlan, ctx planContext, analysis *nativeplan.Analysis) (queryPlan, bool, error) {
	if ctx.Mode == evalModeRange {
		info := analysis.InfoFor(node)
		if info == nil || info.Fragment == nil || info.Fragment.RangeFunction == nil {
			return nil, false, nil
		}
		if !nativeplan.IsSupportedNativeRangeModeForDirectSelector(info.Fragment) && !nativeplan.IsSupportedNativeRangeModeForCounterSubquery(info.Fragment) {
			return nil, false, nil
		}
		return maybeBuildNativeRangeLikePlanAllowRange(node, node.Func, node.ExprString(), ctx, analysis, true)
	}
	return maybeBuildNativeRangeLikePlan(node, node.Func, node.ExprString(), ctx, analysis)
}

func maybeBuildNativeChangesPlan(node *logicalChangesPlan, ctx planContext, analysis *nativeplan.Analysis) (queryPlan, bool, error) {
	if ctx.Mode == evalModeRange {
		info := analysis.InfoFor(node)
		if info == nil || info.Fragment == nil || info.Fragment.RangeFunction == nil {
			return nil, false, nil
		}
		if !nativeplan.IsSupportedNativeRangeModeForDirectSelector(info.Fragment) && !nativeplan.IsSupportedNativeRangeModeForCounterSubquery(info.Fragment) {
			return nil, false, nil
		}
		return maybeBuildNativeRangeLikePlanAllowRange(node, "changes", node.ExprString(), ctx, analysis, true)
	}
	return maybeBuildNativeRangeLikePlan(node, "changes", node.ExprString(), ctx, analysis)
}

func maybeBuildNativeDerivPlan(node *logicalDerivPlan, ctx planContext, analysis *nativeplan.Analysis) (queryPlan, bool, error) {
	if ctx.Mode == evalModeRange {
		info := analysis.InfoFor(node)
		if info == nil || info.Fragment == nil || info.Fragment.RangeFunction == nil {
			return nil, false, nil
		}
		if !nativeplan.IsSupportedNativeRangeModeForDirectSelector(info.Fragment) && !nativeplan.IsSupportedNativeRangeModeForCounterSubquery(info.Fragment) {
			return nil, false, nil
		}
		return maybeBuildNativeRangeLikePlanAllowRange(node, "deriv", node.ExprString(), ctx, analysis, true)
	}
	return maybeBuildNativeRangeLikePlan(node, "deriv", node.ExprString(), ctx, analysis)
}

func maybeBuildNativeRangeLikePlan(node planpkg.LogicalPlan, kind, expr string, ctx planContext, analysis *nativeplan.Analysis) (queryPlan, bool, error) {
	return maybeBuildNativeRangeLikePlanAllowRange(node, kind, expr, ctx, analysis, false)
}

func maybeBuildNativeRangeLikePlanAllowRange(node planpkg.LogicalPlan, kind, expr string, ctx planContext, analysis *nativeplan.Analysis, allowRange bool) (queryPlan, bool, error) {
	if ctx.Mode != evalModeInstant && !(allowRange && ctx.Mode == evalModeRange) {
		return nil, false, nil
	}
	info := analysis.InfoFor(node)
	if info == nil || info.Fragment == nil || info.Fragment.Kind != nativeplan.FragmentKindRangeFunction {
		return nil, false, nil
	}
	optimized, err := nativeplan.BuildOptimizedFragmentWithContext(node, analysis, nativeplan.OptimizationContext{
		Mode:             renderModeForPlanContext(ctx),
		EvaluationTimeMS: ctx.EvaluationTime.UnixMilli(),
		StartMS:          ctx.Start.UnixMilli(),
		EndMS:            ctx.End.UnixMilli(),
		StepMS:           ctx.Step.Milliseconds(),
	})
	if err != nil {
		return nil, false, err
	}
	if rejectRangeModeFixedTemporalAnchor(ctx, optimized.Fragment) {
		return nil, false, nil
	}
	renderMode := renderModeForPlanContext(ctx)
	rendered, err := nativeplan.RenderFragment(storage.QueryConfig{Database: "preview", Table: "preview"}, optimized.Fragment, nativeplan.RenderParams{
		Mode:             renderMode,
		EvaluationTimeMS: ctx.EvaluationTime.UnixMilli(),
		StartMS:          ctx.Start.UnixMilli(),
		EndMS:            ctx.End.UnixMilli(),
		StepMS:           ctx.Step.Milliseconds(),
		RequiredStartMS:  optimized.Report.RequiredInputStartMS,
		RequiredEndMS:    optimized.Report.RequiredInputEndMS,
		ResolveSourcePromQL: func(expr parser.Expr) (string, error) {
			return resolveDelegatedPromQL(expr, evalParams{Mode: ctx.Mode, EvaluationTime: ctx.EvaluationTime, Start: ctx.Start, End: ctx.End, Step: ctx.Step})
		},
	})
	if err != nil {
		return nil, false, err
	}
	if err := nativeplan.ApplyRenderedSQLMetadata(optimized.Report, renderMode, rendered.SQL); err != nil {
		return nil, false, err
	}
	children := []ExplainNode{}
	for _, child := range info.Children {
		if child == nil {
			continue
		}
		children = append(children, explainNativeAggregationSource(child))
	}
	return &nativeSubtreePlan{
		Kind:               kind,
		Expr:               expr,
		Reason:             info.NativeReason,
		Estimate:           estimateRangePlan(ctx),
		Children:           children,
		Fragment:           optimized.Fragment,
		OptimizationReport: optimized.Report,
		Info:               info,
	}, true, nil
}

func maybeBuildNativeAbsentPlan(node *logicalAbsentPlan, ctx planContext, analysis *nativeplan.Analysis) (queryPlan, bool, error) {
	return maybeBuildNativeAbsentLikePlan(node, "absent", node.ExprString(), ctx, analysis)
}

func maybeBuildNativeAbsentOverTimePlan(node *logicalAbsentOverTimePlan, ctx planContext, analysis *nativeplan.Analysis) (queryPlan, bool, error) {
	return maybeBuildNativeAbsentLikePlan(node, "absent_over_time", node.ExprString(), ctx, analysis)
}

func maybeBuildNativeAbsentLikePlan(node planpkg.LogicalPlan, kind, expr string, ctx planContext, analysis *nativeplan.Analysis) (queryPlan, bool, error) {
	if ctx.Mode != evalModeInstant && ctx.Mode != evalModeRange {
		return nil, false, nil
	}
	info := analysis.InfoFor(node)
	if info == nil || info.Fragment == nil || info.Fragment.Kind != nativeplan.FragmentKindAbsent || info.Fragment.Absent == nil {
		return nil, false, nil
	}
	optimized, err := nativeplan.BuildOptimizedFragmentWithContext(node, analysis, nativeplan.OptimizationContext{Mode: renderModeForPlanContext(ctx), EvaluationTimeMS: ctx.EvaluationTime.UnixMilli(), StartMS: ctx.Start.UnixMilli(), EndMS: ctx.End.UnixMilli(), StepMS: ctx.Step.Milliseconds()})
	if err != nil {
		return nil, false, err
	}
	if rejectRangeModeFixedTemporalAnchor(ctx, optimized.Fragment) {
		return nil, false, nil
	}
	renderMode := renderModeForPlanContext(ctx)
	rendered, err := nativeplan.RenderFragment(storage.QueryConfig{Database: "preview", Table: "preview"}, optimized.Fragment, nativeplan.RenderParams{Mode: renderMode, EvaluationTimeMS: ctx.EvaluationTime.UnixMilli(), StartMS: ctx.Start.UnixMilli(), EndMS: ctx.End.UnixMilli(), StepMS: ctx.Step.Milliseconds(), RequiredStartMS: optimized.Report.RequiredInputStartMS, RequiredEndMS: optimized.Report.RequiredInputEndMS, ResolveSourcePromQL: func(expr parser.Expr) (string, error) {
		return resolveDelegatedPromQL(expr, evalParams{Mode: ctx.Mode, EvaluationTime: ctx.EvaluationTime, Start: ctx.Start, End: ctx.End, Step: ctx.Step})
	}})
	if err != nil {
		return nil, false, err
	}
	if err := nativeplan.ApplyRenderedSQLMetadata(optimized.Report, renderMode, rendered.SQL); err != nil {
		return nil, false, err
	}
	children := []ExplainNode{}
	for _, child := range info.Children {
		if child == nil {
			continue
		}
		children = append(children, explainNativeAggregationSource(child))
	}
	return &nativeSubtreePlan{Kind: kind, Expr: expr, Reason: info.NativeReason, Estimate: estimateRangePlan(ctx), Children: children, Fragment: optimized.Fragment, OptimizationReport: optimized.Report, Info: info}, true, nil
}
