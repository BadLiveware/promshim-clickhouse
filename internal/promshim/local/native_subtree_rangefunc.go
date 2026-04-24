package local

import (
	logicalpkg "github.com/BadLiveware/promshim-ch/internal/promshim/logical"
	nativeplan "github.com/BadLiveware/promshim-ch/internal/promshim/native"
)

func rejectRangeModeFixedTemporalAnchor(ctx PlanContext, fragment *nativeplan.NativeFragment) bool {
	// Range-mode native rendering now handles fixed @ anchors by evaluating the
	// fragment once at the resolved anchor and shaping the result onto the outer
	// range response grid when needed, so anchored trees no longer need planner-
	// level rejection here.
	_ = ctx
	_ = fragment
	return false
}

func maybeBuildNativeRangeFunctionPlan(node *logicalRangeFunctionPlan, ctx PlanContext, analysis *nativeplan.Analysis) (Plan, bool, error) {
	if ctx.Mode == EvalModeRange {
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

func maybeBuildNativeRatePlan(node *logicalRatePlan, ctx PlanContext, analysis *nativeplan.Analysis) (Plan, bool, error) {
	if ctx.Mode == EvalModeRange {
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

func maybeBuildNativeIncreasePlan(node *logicalIncreasePlan, ctx PlanContext, analysis *nativeplan.Analysis) (Plan, bool, error) {
	if ctx.Mode == EvalModeRange {
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

func maybeBuildNativeDeltaPlan(node *logicalDeltaPlan, ctx PlanContext, analysis *nativeplan.Analysis) (Plan, bool, error) {
	if ctx.Mode == EvalModeRange {
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

func maybeBuildNativeChangesPlan(node *logicalChangesPlan, ctx PlanContext, analysis *nativeplan.Analysis) (Plan, bool, error) {
	if ctx.Mode == EvalModeRange {
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

func maybeBuildNativeDerivPlan(node *logicalDerivPlan, ctx PlanContext, analysis *nativeplan.Analysis) (Plan, bool, error) {
	if ctx.Mode == EvalModeRange {
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

func maybeBuildNativeQuantileOverTimePlan(node *logicalQuantileOverTimePlan, ctx PlanContext, analysis *nativeplan.Analysis) (Plan, bool, error) {
	if ctx.Mode == EvalModeRange {
		info := analysis.InfoFor(node)
		if info == nil || info.Fragment == nil || info.Fragment.RangeFunction == nil {
			return nil, false, nil
		}
		if !nativeplan.IsSupportedNativeRangeModeForDirectSelector(info.Fragment) && !nativeplan.IsSupportedNativeRangeModeForWindowedArraysSubquery(info.Fragment) {
			return nil, false, nil
		}
		return maybeBuildNativeRangeLikePlanAllowRange(node, "quantile_over_time", node.ExprString(), ctx, analysis, true)
	}
	return maybeBuildNativeRangeLikePlan(node, "quantile_over_time", node.ExprString(), ctx, analysis)
}

func maybeBuildNativeRangeLikePlan(node logicalpkg.Node, kind, expr string, ctx PlanContext, analysis *nativeplan.Analysis) (Plan, bool, error) {
	return maybeBuildNativeRangeLikePlanAllowRange(node, kind, expr, ctx, analysis, false)
}

func maybeBuildNativeRangeLikePlanAllowRange(node logicalpkg.Node, kind, expr string, ctx PlanContext, analysis *nativeplan.Analysis, allowRange bool) (Plan, bool, error) {
	if ctx.Mode != EvalModeInstant && !(allowRange && ctx.Mode == EvalModeRange) {
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
	if err := preRenderNativeSubtreePlanSQL(node, analysis, optimized, ctx); err != nil {
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
		Node:               node,
		Analysis:           analysis,
	}, true, nil
}

func maybeBuildNativeAbsentPlan(node *logicalAbsentPlan, ctx PlanContext, analysis *nativeplan.Analysis) (Plan, bool, error) {
	return maybeBuildNativeAbsentLikePlan(node, "absent", node.ExprString(), ctx, analysis)
}

func maybeBuildNativeAbsentOverTimePlan(node *logicalAbsentOverTimePlan, ctx PlanContext, analysis *nativeplan.Analysis) (Plan, bool, error) {
	return maybeBuildNativeAbsentLikePlan(node, "absent_over_time", node.ExprString(), ctx, analysis)
}

func maybeBuildNativeAbsentLikePlan(node logicalpkg.Node, kind, expr string, ctx PlanContext, analysis *nativeplan.Analysis) (Plan, bool, error) {
	if ctx.Mode != EvalModeInstant && ctx.Mode != EvalModeRange {
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
	if err := preRenderNativeSubtreePlanSQL(node, analysis, optimized, ctx); err != nil {
		return nil, false, err
	}
	children := []ExplainNode{}
	for _, child := range info.Children {
		if child == nil {
			continue
		}
		children = append(children, explainNativeAggregationSource(child))
	}
	return &nativeSubtreePlan{Kind: kind, Expr: expr, Reason: info.NativeReason, Estimate: estimateRangePlan(ctx), Children: children, Fragment: optimized.Fragment, OptimizationReport: optimized.Report, Info: info, Node: node, Analysis: analysis}, true, nil
}
