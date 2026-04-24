package local

import (
	logicalpkg "github.com/BadLiveware/promshim-ch/internal/promshim/logical"
	nativeplan "github.com/BadLiveware/promshim-ch/internal/promshim/native"
)

func maybeBuildNativeLeafPlan(node *logicalLeafExprPlan, ctx PlanContext, analysis *nativeplan.Analysis) (Plan, bool, error) {
	if ctx.Mode != EvalModeInstant && ctx.Mode != EvalModeRange {
		return nil, false, nil
	}
	info := analysis.InfoFor(node)
	if info == nil || info.SubtreeShape != nativeplan.SubtreeShapeLeafSource {
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
	return newNativeSubtreePlan("leaf", node.ExprString(), info.NativeReason, estimateRangePlan(ctx), nil, optimized, info, node, analysis), true, nil
}

func maybeBuildNativeSubqueryPlan(node *logicalSubqueryPlan, ctx PlanContext, analysis *nativeplan.Analysis) (Plan, bool, error) {
	if ctx.Mode != EvalModeInstant && ctx.Mode != EvalModeRange {
		return nil, false, nil
	}
	info := analysis.InfoFor(node)
	if info == nil || info.SubtreeShape != nativeplan.SubtreeShapeSubquery || !nativeplan.HasSubqueryFragmentFromInfo(info) {
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
	return newNativeSubtreePlan("subquery", node.ExprString(), info.NativeReason, estimateRangePlan(ctx), buildNativeSubtreeChildren(info), optimized, info, node, analysis), true, nil
}

func maybeBuildNativeGenericPlan(node logicalpkg.Node, expr, kind string, ctx PlanContext, analysis *nativeplan.Analysis, allowedShapes ...nativeplan.SubtreeShape) (Plan, bool, error) {
	if ctx.Mode != EvalModeInstant && ctx.Mode != EvalModeRange {
		return nil, false, nil
	}
	info := analysis.InfoFor(node)
	if info == nil {
		return nil, false, nil
	}
	allowed := false
	for _, shape := range allowedShapes {
		if info.SubtreeShape == shape {
			allowed = true
			break
		}
	}
	if !allowed {
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
	return newNativeSubtreePlan(kind, expr, info.NativeReason, estimateRangePlan(ctx), buildNativeSubtreeChildren(info), optimized, info, node, analysis), true, nil
}

func maybeBuildNativeSourcePlan(node *logicalPointwiseFunctionPlan, ctx PlanContext, analysis *nativeplan.Analysis) (Plan, bool, error) {
	if ctx.Mode != EvalModeInstant && ctx.Mode != EvalModeRange {
		return nil, false, nil
	}
	info := analysis.InfoFor(node)
	if info == nil {
		return nil, false, nil
	}
	switch info.SubtreeShape {
	case nativeplan.SubtreeShapeLeafSource, nativeplan.SubtreeShapeUnarySourceExpr, nativeplan.SubtreeShapeBinaryScalarSourceExpr, nativeplan.SubtreeShapeSyntheticSeries, nativeplan.SubtreeShapeClampTransform:
	default:
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
	return newNativeSubtreePlan(node.Func, node.ExprString(), info.NativeReason, estimateRangePlan(ctx), buildNativeSubtreeChildren(info), optimized, info, node, analysis), true, nil
}

func maybeBuildNativeSortPlan(node *logicalSortPlan, ctx PlanContext, analysis *nativeplan.Analysis) (Plan, bool, error) {
	return maybeBuildNativeGenericPlan(node, node.ExprString(), node.Func, ctx, analysis, nativeplan.SubtreeShapeSortTransform)
}

func maybeBuildNativeLabelReplacePlan(node *logicalLabelReplacePlan, ctx PlanContext, analysis *nativeplan.Analysis) (Plan, bool, error) {
	return maybeBuildNativeGenericPlan(node, node.ExprString(), "label_replace", ctx, analysis, nativeplan.SubtreeShapeLabelTransform)
}

func maybeBuildNativeLabelJoinPlan(node *logicalLabelJoinPlan, ctx PlanContext, analysis *nativeplan.Analysis) (Plan, bool, error) {
	return maybeBuildNativeGenericPlan(node, node.ExprString(), "label_join", ctx, analysis, nativeplan.SubtreeShapeLabelTransform)
}

func maybeBuildNativeInfoPlan(node *logicalInfoPlan, ctx PlanContext, analysis *nativeplan.Analysis) (Plan, bool, error) {
	if ctx.Mode != EvalModeInstant && ctx.Mode != EvalModeRange {
		return nil, false, nil
	}
	info := analysis.InfoFor(node)
	if info == nil || info.SubtreeShape != nativeplan.SubtreeShapeInfoJoin {
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
	return newNativeSubtreePlan("info", node.ExprString(), info.NativeReason, estimateRangePlan(ctx), buildNativeSubtreeChildren(info), optimized, info, node, analysis), true, nil
}

func maybeBuildNativeScalarConvertPlan(node *logicalScalarConvertPlan, ctx PlanContext, analysis *nativeplan.Analysis) (Plan, bool, error) {
	if ctx.Mode != EvalModeInstant && ctx.Mode != EvalModeRange {
		return nil, false, nil
	}
	info := analysis.InfoFor(node)
	if info == nil || info.SubtreeShape != nativeplan.SubtreeShapeScalarConvert {
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
	return newNativeSubtreePlan("scalar", node.ExprString(), info.NativeReason, estimateRangePlan(ctx), buildNativeSubtreeChildren(info), optimized, info, node, analysis), true, nil
}

func maybeBuildNativeHistogramFractionPlan(node *logicalHistogramFractionPlan, ctx PlanContext, analysis *nativeplan.Analysis) (Plan, bool, error) {
	if ctx.Mode != EvalModeInstant && ctx.Mode != EvalModeRange {
		return nil, false, nil
	}
	info := analysis.InfoFor(node)
	if info == nil || info.SubtreeShape != nativeplan.SubtreeShapeHistogramFunction || !nativeplan.HasHistogramFunctionFragmentFromInfo(info) {
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
	return newNativeSubtreePlan("histogram_fraction", node.ExprString(), info.NativeReason, estimateRangePlan(ctx), buildNativeSubtreeChildren(info), optimized, info, node, analysis), true, nil
}

func maybeBuildNativeHistogramQuantilePlan(node *logicalHistogramQuantilePlan, ctx PlanContext, analysis *nativeplan.Analysis) (Plan, bool, error) {
	if ctx.Mode != EvalModeInstant && ctx.Mode != EvalModeRange {
		return nil, false, nil
	}
	info := analysis.InfoFor(node)
	if info == nil || info.SubtreeShape != nativeplan.SubtreeShapeHistogramFunction || !nativeplan.HasHistogramFunctionFragmentFromInfo(info) {
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
	return newNativeSubtreePlan("histogram_quantile", node.ExprString(), info.NativeReason, estimateRangePlan(ctx), buildNativeSubtreeChildren(info), optimized, info, node, analysis), true, nil
}

func maybeBuildNativeHistogramQuantilesPlan(node *logicalHistogramQuantilesPlan, ctx PlanContext, analysis *nativeplan.Analysis) (Plan, bool, error) {
	if ctx.Mode != EvalModeInstant && ctx.Mode != EvalModeRange {
		return nil, false, nil
	}
	info := analysis.InfoFor(node)
	if info == nil || info.SubtreeShape != nativeplan.SubtreeShapeHistogramFunction || !nativeplan.HasHistogramFunctionFragmentFromInfo(info) || nativeplan.HistogramFunctionNameFromInfo(info) != "histogram_quantiles" {
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
	return newNativeSubtreePlan("histogram_quantiles", node.ExprString(), info.NativeReason, estimateRangePlan(ctx), buildNativeSubtreeChildren(info), optimized, info, node, analysis), true, nil
}

func maybeBuildNativeHistogramProjectionPlan(node *logicalHistogramProjectionPlan, ctx PlanContext, analysis *nativeplan.Analysis) (Plan, bool, error) {
	if ctx.Mode != EvalModeInstant && ctx.Mode != EvalModeRange {
		return nil, false, nil
	}
	info := analysis.InfoFor(node)
	if info == nil || info.SubtreeShape != nativeplan.SubtreeShapeHistogramProjection || !nativeplan.HasHistogramProjectionFragmentFromInfo(info) {
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
	return newNativeSubtreePlan(node.Func, node.ExprString(), info.NativeReason, estimateRangePlan(ctx), buildNativeSubtreeChildren(info), optimized, info, node, analysis), true, nil
}

func maybeBuildNativeScalarLiteralPlan(node *logicalScalarLiteralPlan, ctx PlanContext, analysis *nativeplan.Analysis) (Plan, bool, error) {
	if !ctx.NativeLoweringMode.ForcesNativeRoot() {
		return nil, false, nil
	}
	return maybeBuildNativeGenericPlan(node, node.ExprString(), "scalar_literal", ctx, analysis, nativeplan.SubtreeShapeSyntheticSeries)
}

func maybeBuildNativeVectorPlan(node *logicalVectorPlan, ctx PlanContext, analysis *nativeplan.Analysis) (Plan, bool, error) {
	return maybeBuildNativeGenericPlan(node, node.ExprString(), "vector", ctx, analysis, nativeplan.SubtreeShapeSyntheticSeries, nativeplan.SubtreeShapeScalarConvert)
}

func maybeBuildNativeScalarBuiltinPlan(node *logicalScalarBuiltinPlan, ctx PlanContext, analysis *nativeplan.Analysis) (Plan, bool, error) {
	if ctx.Mode != EvalModeInstant && ctx.Mode != EvalModeRange {
		return nil, false, nil
	}
	info := analysis.InfoFor(node)
	if info == nil || info.SubtreeShape != nativeplan.SubtreeShapeSyntheticSeries {
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
	return newNativeSubtreePlan(node.Func, node.ExprString(), info.NativeReason, estimateRangePlan(ctx), nil, optimized, info, node, analysis), true, nil
}

func maybeBuildNativeUnaryPlan(node *logicalUnaryPlan, ctx PlanContext, analysis *nativeplan.Analysis) (Plan, bool, error) {
	info := analysis.InfoFor(node)
	if info != nil && info.OutputKind == nativeplan.OutputKindScalar && !ctx.NativeLoweringMode.ForcesNativeRoot() {
		return nil, false, nil
	}
	return maybeBuildNativeGenericPlan(node, node.ExprString(), "unary", ctx, analysis, nativeplan.SubtreeShapeUnarySourceExpr, nativeplan.SubtreeShapeValueTransform, nativeplan.SubtreeShapeSyntheticSeries)
}

func maybeBuildNativeBinaryPlan(node *logicalBinaryPlan, ctx PlanContext, analysis *nativeplan.Analysis) (Plan, bool, error) {
	info := analysis.InfoFor(node)
	if info != nil && info.OutputKind == nativeplan.OutputKindScalar && !ctx.NativeLoweringMode.ForcesNativeRoot() {
		return nil, false, nil
	}
	return maybeBuildNativeGenericPlan(node, node.ExprString(), "binary", ctx, analysis, nativeplan.SubtreeShapeBinaryVectorJoin, nativeplan.SubtreeShapeBinaryScalarSourceExpr, nativeplan.SubtreeShapeValueTransform, nativeplan.SubtreeShapeSyntheticSeries)
}

func maybeBuildNativeRoundPlan(node *logicalRoundPlan, ctx PlanContext, analysis *nativeplan.Analysis) (Plan, bool, error) {
	return maybeBuildNativeGenericPlan(node, node.ExprString(), "round", ctx, analysis, nativeplan.SubtreeShapeValueTransform)
}

func maybeBuildNativeAggregationPlan(node *logicalAggregationPlan, ctx PlanContext, analysis *nativeplan.Analysis) (Plan, bool, error) {
	info := analysis.InfoFor(node)
	if info == nil || info.SubtreeShape != nativeplan.SubtreeShapeAggregation || !nativeplan.HasAggregationFragmentFromInfo(info) {
		return nil, false, nil
	}
	decision := decideNativeAggregationPushdownFromAnalysis(node, analysis, ctx)
	if !decision.Eligible && !ctx.NativeLoweringMode.ForcesNativeRoot() {
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
	var children []ExplainNode
	if decision.Source.Explain.Strategy != "" {
		children = append(children, decision.Source.Explain)
	} else {
		children = buildNativeSubtreeChildren(info)
	}
	reason := decision.Reason
	if reason == "" || ctx.NativeLoweringMode.ForcesNativeRoot() && !decision.Eligible {
		reason = info.NativeReason
	}
	return newNativeSubtreePlan("aggregation", node.ExprString(), reason, estimateRangePlan(ctx), children, optimized, analysis.InfoFor(node), node, analysis), true, nil
}
