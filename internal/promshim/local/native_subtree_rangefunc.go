package local

import (
	logicalpkg "github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
	nativeplan "github.com/BadLiveware/promshim-clickhouse/internal/promshim/native"
)

func maybeBuildNativeRangeFunctionPlan(node *logicalRangeFunctionPlan, ctx PlanContext, analysis *nativeplan.Analysis) (Plan, bool, error) {
	if ctx.Mode == EvalModeRange {
		info := analysis.InfoFor(node)
		if !nativeplan.HasRangeFunctionFragmentFromInfo(info) {
			return nil, false, nil
		}
		if !nativeplan.IsSupportedNativeRangeModeForDirectSelectorFromInfo(info) && !nativeplan.IsSupportedNativeRangeModeForWindowedArraysSubqueryFromInfo(info) {
			return nil, false, nil
		}
		return maybeBuildNativeRangeLikePlanAllowRange(node, node.Func, node.ExprString(), ctx, analysis, true)
	}
	return maybeBuildNativeRangeLikePlan(node, node.Func, node.ExprString(), ctx, analysis)
}

func maybeBuildNativeRatePlan(node *logicalRatePlan, ctx PlanContext, analysis *nativeplan.Analysis) (Plan, bool, error) {
	if ctx.Mode == EvalModeRange {
		info := analysis.InfoFor(node)
		if !nativeplan.HasRangeFunctionFragmentFromInfo(info) {
			return nil, false, nil
		}
		if !nativeplan.IsSupportedNativeRangeModeForDirectSelectorFromInfo(info) && !nativeplan.IsSupportedNativeRangeModeForCounterSubqueryFromInfo(info) {
			return nil, false, nil
		}
		return maybeBuildNativeRangeLikePlanAllowRange(node, node.Func, node.ExprString(), ctx, analysis, true)
	}
	return maybeBuildNativeRangeLikePlan(node, node.Func, node.ExprString(), ctx, analysis)
}

func maybeBuildNativeIncreasePlan(node *logicalIncreasePlan, ctx PlanContext, analysis *nativeplan.Analysis) (Plan, bool, error) {
	if ctx.Mode == EvalModeRange {
		info := analysis.InfoFor(node)
		if !nativeplan.HasRangeFunctionFragmentFromInfo(info) {
			return nil, false, nil
		}
		if !nativeplan.IsSupportedNativeRangeModeForDirectSelectorFromInfo(info) && !nativeplan.IsSupportedNativeRangeModeForCounterSubqueryFromInfo(info) {
			return nil, false, nil
		}
		return maybeBuildNativeRangeLikePlanAllowRange(node, "increase", node.ExprString(), ctx, analysis, true)
	}
	return maybeBuildNativeRangeLikePlan(node, "increase", node.ExprString(), ctx, analysis)
}

func maybeBuildNativeDeltaPlan(node *logicalDeltaPlan, ctx PlanContext, analysis *nativeplan.Analysis) (Plan, bool, error) {
	if ctx.Mode == EvalModeRange {
		info := analysis.InfoFor(node)
		if !nativeplan.HasRangeFunctionFragmentFromInfo(info) {
			return nil, false, nil
		}
		if !nativeplan.IsSupportedNativeRangeModeForDirectSelectorFromInfo(info) && !nativeplan.IsSupportedNativeRangeModeForCounterSubqueryFromInfo(info) {
			return nil, false, nil
		}
		return maybeBuildNativeRangeLikePlanAllowRange(node, node.Func, node.ExprString(), ctx, analysis, true)
	}
	return maybeBuildNativeRangeLikePlan(node, node.Func, node.ExprString(), ctx, analysis)
}

func maybeBuildNativeChangesPlan(node *logicalChangesPlan, ctx PlanContext, analysis *nativeplan.Analysis) (Plan, bool, error) {
	if ctx.Mode == EvalModeRange {
		info := analysis.InfoFor(node)
		if !nativeplan.HasRangeFunctionFragmentFromInfo(info) {
			return nil, false, nil
		}
		if !nativeplan.IsSupportedNativeRangeModeForDirectSelectorFromInfo(info) && !nativeplan.IsSupportedNativeRangeModeForCounterSubqueryFromInfo(info) {
			return nil, false, nil
		}
		return maybeBuildNativeRangeLikePlanAllowRange(node, "changes", node.ExprString(), ctx, analysis, true)
	}
	return maybeBuildNativeRangeLikePlan(node, "changes", node.ExprString(), ctx, analysis)
}

func maybeBuildNativeDerivPlan(node *logicalDerivPlan, ctx PlanContext, analysis *nativeplan.Analysis) (Plan, bool, error) {
	if ctx.Mode == EvalModeRange {
		info := analysis.InfoFor(node)
		if !nativeplan.HasRangeFunctionFragmentFromInfo(info) {
			return nil, false, nil
		}
		if !nativeplan.IsSupportedNativeRangeModeForDirectSelectorFromInfo(info) && !nativeplan.IsSupportedNativeRangeModeForCounterSubqueryFromInfo(info) {
			return nil, false, nil
		}
		return maybeBuildNativeRangeLikePlanAllowRange(node, "deriv", node.ExprString(), ctx, analysis, true)
	}
	return maybeBuildNativeRangeLikePlan(node, "deriv", node.ExprString(), ctx, analysis)
}

func maybeBuildNativeQuantileOverTimePlan(node *logicalQuantileOverTimePlan, ctx PlanContext, analysis *nativeplan.Analysis) (Plan, bool, error) {
	if ctx.Mode == EvalModeRange {
		info := analysis.InfoFor(node)
		if !nativeplan.HasRangeFunctionFragmentFromInfo(info) {
			return nil, false, nil
		}
		if !nativeplan.IsSupportedNativeRangeModeForDirectSelectorFromInfo(info) && !nativeplan.IsSupportedNativeRangeModeForWindowedArraysSubqueryFromInfo(info) {
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
	if ctx.Mode != EvalModeInstant && (!allowRange || ctx.Mode != EvalModeRange) {
		return nil, false, nil
	}
	info := analysis.InfoFor(node)
	if info == nil || info.SubtreeShape != nativeplan.SubtreeShapeRangeFunction {
		return nil, false, nil
	}
	optimized, err := nativeplan.OptimizeFromInfo(info, node, analysis, nativeplan.OptimizationContext{
		Mode:             renderModeForPlanContext(ctx),
		EvaluationTimeMS: ctx.EvaluationTime.UnixMilli(),
		StartMS:          ctx.Start.UnixMilli(),
		EndMS:            ctx.End.UnixMilli(),
		StepMS:           ctx.Step.Milliseconds(),
	})
	if err != nil {
		return nil, false, err
	}
	if rejectRangeModeFixedTemporalAnchor(ctx, optimized) {
		return nil, false, nil
	}
	if err := preRenderNativeSubtreePlanSQL(node, analysis, optimized, ctx); err != nil {
		return nil, false, err
	}
	children := buildNativeSubtreeChildren(info)
	return newNativeSubtreePlan(kind, expr, info.NativeReason, estimateRangePlan(ctx), children, optimized, info, node, analysis, ctx), true, nil
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
	if info == nil || info.SubtreeShape != nativeplan.SubtreeShapeAbsent || !nativeplan.HasAbsentFragmentFromInfo(info) {
		return nil, false, nil
	}
	optimized, err := nativeplan.OptimizeFromInfo(info, node, analysis, nativeplan.OptimizationContext{Mode: renderModeForPlanContext(ctx), EvaluationTimeMS: ctx.EvaluationTime.UnixMilli(), StartMS: ctx.Start.UnixMilli(), EndMS: ctx.End.UnixMilli(), StepMS: ctx.Step.Milliseconds()})
	if err != nil {
		return nil, false, err
	}
	if rejectRangeModeFixedTemporalAnchor(ctx, optimized) {
		return nil, false, nil
	}
	if err := preRenderNativeSubtreePlanSQL(node, analysis, optimized, ctx); err != nil {
		return nil, false, err
	}
	children := buildNativeSubtreeChildren(info)
	return newNativeSubtreePlan(kind, expr, info.NativeReason, estimateRangePlan(ctx), children, optimized, info, node, analysis, ctx), true, nil
}
