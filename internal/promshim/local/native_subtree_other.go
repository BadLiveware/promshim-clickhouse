package local

import (
	nativeplan "ch-observability/internal/promshim/native"
	"ch-observability/internal/promshim/native/renderer"
	planpkg "ch-observability/internal/promshim/plan"
	"ch-observability/internal/promshim/storage"

	"github.com/prometheus/prometheus/promql/parser"
)

func maybeBuildNativeLeafPlan(node *logicalLeafExprPlan, ctx PlanContext, analysis *nativeplan.Analysis) (Plan, bool, error) {
	if ctx.Mode != EvalModeInstant && ctx.Mode != EvalModeRange {
		return nil, false, nil
	}
	info := analysis.InfoFor(node)
	if info == nil || info.Fragment == nil || info.Fragment.Kind != nativeplan.FragmentKindLeafSource {
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
	rendered, err := renderer.RenderFragment(storage.QueryConfig{Database: "preview", Table: "preview"}, optimized.Fragment, renderer.RenderParams{Mode: renderMode, EvaluationTimeMS: ctx.EvaluationTime.UnixMilli(), StartMS: ctx.Start.UnixMilli(), EndMS: ctx.End.UnixMilli(), StepMS: ctx.Step.Milliseconds(), RequiredStartMS: optimized.Report.RequiredInputStartMS, RequiredEndMS: optimized.Report.RequiredInputEndMS, ResolveSourcePromQL: func(expr parser.Expr) (string, error) {
		return resolveDelegatedPromQL(expr, EvalParams{Mode: ctx.Mode, EvaluationTime: ctx.EvaluationTime, Start: ctx.Start, End: ctx.End, Step: ctx.Step})
	}})
	if err != nil {
		return nil, false, err
	}
	if err := nativeplan.ApplyRenderedSQLMetadata(optimized.Report, renderMode, rendered.SQL); err != nil {
		return nil, false, err
	}
	return &nativeSubtreePlan{Kind: "leaf", Expr: node.ExprString(), Reason: info.NativeReason, Estimate: estimateRangePlan(ctx), Fragment: optimized.Fragment, OptimizationReport: optimized.Report, Info: info}, true, nil
}

func maybeBuildNativeSubqueryPlan(node *logicalSubqueryPlan, ctx PlanContext, analysis *nativeplan.Analysis) (Plan, bool, error) {
	if ctx.Mode != EvalModeInstant && ctx.Mode != EvalModeRange {
		return nil, false, nil
	}
	info := analysis.InfoFor(node)
	if info == nil || info.Fragment == nil || info.Fragment.Kind != nativeplan.FragmentKindSubquery || info.Fragment.Subquery == nil {
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
	rendered, err := renderer.RenderFragment(storage.QueryConfig{Database: "preview", Table: "preview"}, optimized.Fragment, renderer.RenderParams{Mode: renderMode, EvaluationTimeMS: ctx.EvaluationTime.UnixMilli(), StartMS: ctx.Start.UnixMilli(), EndMS: ctx.End.UnixMilli(), StepMS: ctx.Step.Milliseconds(), RequiredStartMS: optimized.Report.RequiredInputStartMS, RequiredEndMS: optimized.Report.RequiredInputEndMS, ResolveSourcePromQL: func(expr parser.Expr) (string, error) {
		return resolveDelegatedPromQL(expr, EvalParams{Mode: ctx.Mode, EvaluationTime: ctx.EvaluationTime, Start: ctx.Start, End: ctx.End, Step: ctx.Step})
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
	return &nativeSubtreePlan{Kind: "subquery", Expr: node.ExprString(), Reason: info.NativeReason, Estimate: estimateRangePlan(ctx), Children: children, Fragment: optimized.Fragment, OptimizationReport: optimized.Report, Info: info}, true, nil
}

func maybeBuildNativeGenericPlan(node planpkg.LogicalPlan, expr, kind string, ctx PlanContext, analysis *nativeplan.Analysis, allowedKinds ...nativeplan.FragmentKind) (Plan, bool, error) {
	if ctx.Mode != EvalModeInstant && ctx.Mode != EvalModeRange {
		return nil, false, nil
	}
	info := analysis.InfoFor(node)
	if info == nil || info.Fragment == nil {
		return nil, false, nil
	}
	allowed := false
	for _, fragmentKind := range allowedKinds {
		if info.Fragment.Kind == fragmentKind {
			allowed = true
			break
		}
	}
	if !allowed {
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
	rendered, err := renderer.RenderFragment(storage.QueryConfig{Database: "preview", Table: "preview"}, optimized.Fragment, renderer.RenderParams{Mode: renderMode, EvaluationTimeMS: ctx.EvaluationTime.UnixMilli(), StartMS: ctx.Start.UnixMilli(), EndMS: ctx.End.UnixMilli(), StepMS: ctx.Step.Milliseconds(), RequiredStartMS: optimized.Report.RequiredInputStartMS, RequiredEndMS: optimized.Report.RequiredInputEndMS, ResolveSourcePromQL: func(expr parser.Expr) (string, error) {
		return resolveDelegatedPromQL(expr, EvalParams{Mode: ctx.Mode, EvaluationTime: ctx.EvaluationTime, Start: ctx.Start, End: ctx.End, Step: ctx.Step})
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

func maybeBuildNativeSourcePlan(node *logicalPointwiseFunctionPlan, ctx PlanContext, analysis *nativeplan.Analysis) (Plan, bool, error) {
	if ctx.Mode != EvalModeInstant && ctx.Mode != EvalModeRange {
		return nil, false, nil
	}
	info := analysis.InfoFor(node)
	if info == nil || info.Fragment == nil {
		return nil, false, nil
	}
	switch info.Fragment.Kind {
	case nativeplan.FragmentKindLeafSource, nativeplan.FragmentKindUnarySourceExpr, nativeplan.FragmentKindBinaryScalarSourceExpr, nativeplan.FragmentKindSyntheticSeries, nativeplan.FragmentKindClampTransform:
	default:
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
	rendered, err := renderer.RenderFragment(storage.QueryConfig{Database: "preview", Table: "preview"}, optimized.Fragment, renderer.RenderParams{Mode: renderMode, EvaluationTimeMS: ctx.EvaluationTime.UnixMilli(), StartMS: ctx.Start.UnixMilli(), EndMS: ctx.End.UnixMilli(), StepMS: ctx.Step.Milliseconds(), RequiredStartMS: optimized.Report.RequiredInputStartMS, RequiredEndMS: optimized.Report.RequiredInputEndMS, ResolveSourcePromQL: func(expr parser.Expr) (string, error) {
		return resolveDelegatedPromQL(expr, EvalParams{Mode: ctx.Mode, EvaluationTime: ctx.EvaluationTime, Start: ctx.Start, End: ctx.End, Step: ctx.Step})
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
	return &nativeSubtreePlan{Kind: node.Func, Expr: node.ExprString(), Reason: info.NativeReason, Estimate: estimateRangePlan(ctx), Children: children, Fragment: optimized.Fragment, OptimizationReport: optimized.Report, Info: info}, true, nil
}

func maybeBuildNativeSortPlan(node *logicalSortPlan, ctx PlanContext, analysis *nativeplan.Analysis) (Plan, bool, error) {
	return maybeBuildNativeGenericPlan(node, node.ExprString(), node.Func, ctx, analysis, nativeplan.FragmentKindSortTransform)
}

func maybeBuildNativeLabelReplacePlan(node *logicalLabelReplacePlan, ctx PlanContext, analysis *nativeplan.Analysis) (Plan, bool, error) {
	return maybeBuildNativeGenericPlan(node, node.ExprString(), "label_replace", ctx, analysis, nativeplan.FragmentKindLabelTransform)
}

func maybeBuildNativeLabelJoinPlan(node *logicalLabelJoinPlan, ctx PlanContext, analysis *nativeplan.Analysis) (Plan, bool, error) {
	return maybeBuildNativeGenericPlan(node, node.ExprString(), "label_join", ctx, analysis, nativeplan.FragmentKindLabelTransform)
}

func maybeBuildNativeInfoPlan(node *logicalInfoPlan, ctx PlanContext, analysis *nativeplan.Analysis) (Plan, bool, error) {
	if ctx.Mode != EvalModeInstant && ctx.Mode != EvalModeRange {
		return nil, false, nil
	}
	info := analysis.InfoFor(node)
	if info == nil || info.Fragment == nil || info.Fragment.Kind != nativeplan.FragmentKindInfoJoin {
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
	rendered, err := renderer.RenderFragment(storage.QueryConfig{Database: "preview", Table: "preview"}, optimized.Fragment, renderer.RenderParams{Mode: renderMode, EvaluationTimeMS: ctx.EvaluationTime.UnixMilli(), StartMS: ctx.Start.UnixMilli(), EndMS: ctx.End.UnixMilli(), StepMS: ctx.Step.Milliseconds(), RequiredStartMS: optimized.Report.RequiredInputStartMS, RequiredEndMS: optimized.Report.RequiredInputEndMS, ResolveSourcePromQL: func(expr parser.Expr) (string, error) {
		return resolveDelegatedPromQL(expr, EvalParams{Mode: ctx.Mode, EvaluationTime: ctx.EvaluationTime, Start: ctx.Start, End: ctx.End, Step: ctx.Step})
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
	return &nativeSubtreePlan{Kind: "info", Expr: node.ExprString(), Reason: info.NativeReason, Estimate: estimateRangePlan(ctx), Children: children, Fragment: optimized.Fragment, OptimizationReport: optimized.Report, Info: info}, true, nil
}

func maybeBuildNativeScalarConvertPlan(node *logicalScalarConvertPlan, ctx PlanContext, analysis *nativeplan.Analysis) (Plan, bool, error) {
	if ctx.Mode != EvalModeInstant && ctx.Mode != EvalModeRange {
		return nil, false, nil
	}
	info := analysis.InfoFor(node)
	if info == nil || info.Fragment == nil || info.Fragment.Kind != nativeplan.FragmentKindScalarConvert {
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
	rendered, err := renderer.RenderFragment(storage.QueryConfig{Database: "preview", Table: "preview"}, optimized.Fragment, renderer.RenderParams{Mode: renderMode, EvaluationTimeMS: ctx.EvaluationTime.UnixMilli(), StartMS: ctx.Start.UnixMilli(), EndMS: ctx.End.UnixMilli(), StepMS: ctx.Step.Milliseconds(), RequiredStartMS: optimized.Report.RequiredInputStartMS, RequiredEndMS: optimized.Report.RequiredInputEndMS, ResolveSourcePromQL: func(expr parser.Expr) (string, error) {
		return resolveDelegatedPromQL(expr, EvalParams{Mode: ctx.Mode, EvaluationTime: ctx.EvaluationTime, Start: ctx.Start, End: ctx.End, Step: ctx.Step})
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
	return &nativeSubtreePlan{Kind: "scalar", Expr: node.ExprString(), Reason: info.NativeReason, Estimate: estimateRangePlan(ctx), Children: children, Fragment: optimized.Fragment, OptimizationReport: optimized.Report, Info: info}, true, nil
}

func maybeBuildNativeHistogramFractionPlan(node *logicalHistogramFractionPlan, ctx PlanContext, analysis *nativeplan.Analysis) (Plan, bool, error) {
	if ctx.Mode != EvalModeInstant && ctx.Mode != EvalModeRange {
		return nil, false, nil
	}
	info := analysis.InfoFor(node)
	if info == nil || info.Fragment == nil || info.Fragment.Kind != nativeplan.FragmentKindHistogramFunction || info.Fragment.HistogramFunction == nil {
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
	rendered, err := renderer.RenderFragment(storage.QueryConfig{Database: "preview", Table: "preview"}, optimized.Fragment, renderer.RenderParams{Mode: renderMode, EvaluationTimeMS: ctx.EvaluationTime.UnixMilli(), StartMS: ctx.Start.UnixMilli(), EndMS: ctx.End.UnixMilli(), StepMS: ctx.Step.Milliseconds(), RequiredStartMS: optimized.Report.RequiredInputStartMS, RequiredEndMS: optimized.Report.RequiredInputEndMS, ResolveSourcePromQL: func(expr parser.Expr) (string, error) {
		return resolveDelegatedPromQL(expr, EvalParams{Mode: ctx.Mode, EvaluationTime: ctx.EvaluationTime, Start: ctx.Start, End: ctx.End, Step: ctx.Step})
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
	return &nativeSubtreePlan{Kind: "histogram_fraction", Expr: node.ExprString(), Reason: info.NativeReason, Estimate: estimateRangePlan(ctx), Children: children, Fragment: optimized.Fragment, OptimizationReport: optimized.Report}, true, nil
}

func maybeBuildNativeHistogramQuantilePlan(node *logicalHistogramQuantilePlan, ctx PlanContext, analysis *nativeplan.Analysis) (Plan, bool, error) {
	if ctx.Mode != EvalModeInstant && ctx.Mode != EvalModeRange {
		return nil, false, nil
	}
	info := analysis.InfoFor(node)
	if info == nil || info.Fragment == nil || info.Fragment.Kind != nativeplan.FragmentKindHistogramFunction || info.Fragment.HistogramFunction == nil {
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
	rendered, err := renderer.RenderFragment(storage.QueryConfig{Database: "preview", Table: "preview"}, optimized.Fragment, renderer.RenderParams{Mode: renderMode, EvaluationTimeMS: ctx.EvaluationTime.UnixMilli(), StartMS: ctx.Start.UnixMilli(), EndMS: ctx.End.UnixMilli(), StepMS: ctx.Step.Milliseconds(), RequiredStartMS: optimized.Report.RequiredInputStartMS, RequiredEndMS: optimized.Report.RequiredInputEndMS, ResolveSourcePromQL: func(expr parser.Expr) (string, error) {
		return resolveDelegatedPromQL(expr, EvalParams{Mode: ctx.Mode, EvaluationTime: ctx.EvaluationTime, Start: ctx.Start, End: ctx.End, Step: ctx.Step})
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
	return &nativeSubtreePlan{Kind: "histogram_quantile", Expr: node.ExprString(), Reason: info.NativeReason, Estimate: estimateRangePlan(ctx), Children: children, Fragment: optimized.Fragment, OptimizationReport: optimized.Report}, true, nil
}

func maybeBuildNativeHistogramQuantilesPlan(node *logicalHistogramQuantilesPlan, ctx PlanContext, analysis *nativeplan.Analysis) (Plan, bool, error) {
	if ctx.Mode != EvalModeInstant && ctx.Mode != EvalModeRange {
		return nil, false, nil
	}
	info := analysis.InfoFor(node)
	if info == nil || info.Fragment == nil || info.Fragment.Kind != nativeplan.FragmentKindHistogramFunction || info.Fragment.HistogramFunction == nil || info.Fragment.HistogramFunction.Func != "histogram_quantiles" {
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
	rendered, err := renderer.RenderFragment(storage.QueryConfig{Database: "preview", Table: "preview"}, optimized.Fragment, renderer.RenderParams{Mode: renderMode, EvaluationTimeMS: ctx.EvaluationTime.UnixMilli(), StartMS: ctx.Start.UnixMilli(), EndMS: ctx.End.UnixMilli(), StepMS: ctx.Step.Milliseconds(), RequiredStartMS: optimized.Report.RequiredInputStartMS, RequiredEndMS: optimized.Report.RequiredInputEndMS, ResolveSourcePromQL: func(expr parser.Expr) (string, error) {
		return resolveDelegatedPromQL(expr, EvalParams{Mode: ctx.Mode, EvaluationTime: ctx.EvaluationTime, Start: ctx.Start, End: ctx.End, Step: ctx.Step})
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
	return &nativeSubtreePlan{Kind: "histogram_quantiles", Expr: node.ExprString(), Reason: info.NativeReason, Estimate: estimateRangePlan(ctx), Children: children, Fragment: optimized.Fragment, OptimizationReport: optimized.Report}, true, nil
}

func maybeBuildNativeHistogramProjectionPlan(node *logicalHistogramProjectionPlan, ctx PlanContext, analysis *nativeplan.Analysis) (Plan, bool, error) {
	if ctx.Mode != EvalModeInstant && ctx.Mode != EvalModeRange {
		return nil, false, nil
	}
	info := analysis.InfoFor(node)
	if info == nil || info.Fragment == nil || info.Fragment.Kind != nativeplan.FragmentKindHistogramProjection || info.Fragment.HistogramProjection == nil {
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
	rendered, err := renderer.RenderFragment(storage.QueryConfig{Database: "preview", Table: "preview"}, optimized.Fragment, renderer.RenderParams{Mode: renderMode, EvaluationTimeMS: ctx.EvaluationTime.UnixMilli(), StartMS: ctx.Start.UnixMilli(), EndMS: ctx.End.UnixMilli(), StepMS: ctx.Step.Milliseconds(), RequiredStartMS: optimized.Report.RequiredInputStartMS, RequiredEndMS: optimized.Report.RequiredInputEndMS, ResolveSourcePromQL: func(expr parser.Expr) (string, error) {
		return resolveDelegatedPromQL(expr, EvalParams{Mode: ctx.Mode, EvaluationTime: ctx.EvaluationTime, Start: ctx.Start, End: ctx.End, Step: ctx.Step})
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
	return &nativeSubtreePlan{Kind: node.Func, Expr: node.ExprString(), Reason: info.NativeReason, Estimate: estimateRangePlan(ctx), Children: children, Fragment: optimized.Fragment, OptimizationReport: optimized.Report}, true, nil
}

func maybeBuildNativeScalarLiteralPlan(node *logicalScalarLiteralPlan, ctx PlanContext, analysis *nativeplan.Analysis) (Plan, bool, error) {
	if !ctx.NativeLoweringMode.ForcesNativeRoot() {
		return nil, false, nil
	}
	return maybeBuildNativeGenericPlan(node, node.ExprString(), "scalar_literal", ctx, analysis, nativeplan.FragmentKindSyntheticSeries)
}

func maybeBuildNativeVectorPlan(node *logicalVectorPlan, ctx PlanContext, analysis *nativeplan.Analysis) (Plan, bool, error) {
	return maybeBuildNativeGenericPlan(node, node.ExprString(), "vector", ctx, analysis, nativeplan.FragmentKindSyntheticSeries, nativeplan.FragmentKindScalarConvert)
}

func maybeBuildNativeScalarBuiltinPlan(node *logicalScalarBuiltinPlan, ctx PlanContext, analysis *nativeplan.Analysis) (Plan, bool, error) {
	if ctx.Mode != EvalModeInstant && ctx.Mode != EvalModeRange {
		return nil, false, nil
	}
	info := analysis.InfoFor(node)
	if info == nil || info.Fragment == nil || info.Fragment.Kind != nativeplan.FragmentKindSyntheticSeries {
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
	rendered, err := renderer.RenderFragment(storage.QueryConfig{Database: "preview", Table: "preview"}, optimized.Fragment, renderer.RenderParams{Mode: renderMode, EvaluationTimeMS: ctx.EvaluationTime.UnixMilli(), StartMS: ctx.Start.UnixMilli(), EndMS: ctx.End.UnixMilli(), StepMS: ctx.Step.Milliseconds(), RequiredStartMS: optimized.Report.RequiredInputStartMS, RequiredEndMS: optimized.Report.RequiredInputEndMS, ResolveSourcePromQL: func(expr parser.Expr) (string, error) {
		return resolveDelegatedPromQL(expr, EvalParams{Mode: ctx.Mode, EvaluationTime: ctx.EvaluationTime, Start: ctx.Start, End: ctx.End, Step: ctx.Step})
	}})
	if err != nil {
		return nil, false, err
	}
	if err := nativeplan.ApplyRenderedSQLMetadata(optimized.Report, renderMode, rendered.SQL); err != nil {
		return nil, false, err
	}
	return &nativeSubtreePlan{Kind: node.Func, Expr: node.ExprString(), Reason: info.NativeReason, Estimate: estimateRangePlan(ctx), Fragment: optimized.Fragment, OptimizationReport: optimized.Report, Info: info}, true, nil
}

func maybeBuildNativeUnaryPlan(node *logicalUnaryPlan, ctx PlanContext, analysis *nativeplan.Analysis) (Plan, bool, error) {
	info := analysis.InfoFor(node)
	if info != nil && info.OutputKind == nativeplan.OutputKindScalar && !ctx.NativeLoweringMode.ForcesNativeRoot() {
		return nil, false, nil
	}
	return maybeBuildNativeGenericPlan(node, node.ExprString(), "unary", ctx, analysis, nativeplan.FragmentKindUnarySourceExpr, nativeplan.FragmentKindValueTransform, nativeplan.FragmentKindSyntheticSeries)
}

func maybeBuildNativeBinaryPlan(node *logicalBinaryPlan, ctx PlanContext, analysis *nativeplan.Analysis) (Plan, bool, error) {
	info := analysis.InfoFor(node)
	if info != nil && info.OutputKind == nativeplan.OutputKindScalar && !ctx.NativeLoweringMode.ForcesNativeRoot() {
		return nil, false, nil
	}
	return maybeBuildNativeGenericPlan(node, node.ExprString(), "binary", ctx, analysis, nativeplan.FragmentKindBinaryVectorJoin, nativeplan.FragmentKindBinaryScalarSourceExpr, nativeplan.FragmentKindValueTransform, nativeplan.FragmentKindSyntheticSeries)
}

func maybeBuildNativeRoundPlan(node *logicalRoundPlan, ctx PlanContext, analysis *nativeplan.Analysis) (Plan, bool, error) {
	return maybeBuildNativeGenericPlan(node, node.ExprString(), "round", ctx, analysis, nativeplan.FragmentKindValueTransform)
}

func maybeBuildNativeAggregationPlan(node *logicalAggregationPlan, ctx PlanContext, analysis *nativeplan.Analysis) (Plan, bool, error) {
	info := analysis.InfoFor(node)
	if info == nil || info.Fragment == nil || info.Fragment.Kind != nativeplan.FragmentKindAggregation || info.Fragment.Aggregation == nil {
		return nil, false, nil
	}
	decision := decideNativeAggregationPushdownFromAnalysis(node, analysis, ctx)
	if !decision.Eligible && !ctx.NativeLoweringMode.ForcesNativeRoot() {
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
	renderMode := nativeplan.RenderModeInstant
	if ctx.Mode == EvalModeRange {
		renderMode = nativeplan.RenderModeRange
	}
	rendered, err := renderer.RenderFragment(storage.QueryConfig{Database: "preview", Table: "preview"}, optimized.Fragment, renderer.RenderParams{
		Mode:             renderMode,
		EvaluationTimeMS: ctx.EvaluationTime.UnixMilli(),
		StartMS:          ctx.Start.UnixMilli(),
		EndMS:            ctx.End.UnixMilli(),
		StepMS:           ctx.Step.Milliseconds(),
		RequiredStartMS:  optimized.Report.RequiredInputStartMS,
		RequiredEndMS:    optimized.Report.RequiredInputEndMS,
		ResolveSourcePromQL: func(expr parser.Expr) (string, error) {
			return resolveDelegatedPromQL(expr, EvalParams{Mode: ctx.Mode, EvaluationTime: ctx.EvaluationTime, Start: ctx.Start, End: ctx.End, Step: ctx.Step})
		},
	})
	if err != nil {
		return nil, false, err
	}
	if err := nativeplan.ApplyRenderedSQLMetadata(optimized.Report, renderMode, rendered.SQL); err != nil {
		return nil, false, err
	}
	children := []ExplainNode{}
	if decision.Source.Explain.Strategy != "" {
		children = append(children, decision.Source.Explain)
	} else {
		for _, child := range info.Children {
			if child == nil {
				continue
			}
			children = append(children, explainNativeAggregationSource(child))
		}
	}
	reason := decision.Reason
	if reason == "" || ctx.NativeLoweringMode.ForcesNativeRoot() && !decision.Eligible {
		reason = info.NativeReason
	}
	return &nativeSubtreePlan{
		Kind:               "aggregation",
		Expr:               node.ExprString(),
		Reason:             reason,
		Estimate:           estimateRangePlan(ctx),
		Children:           children,
		Fragment:           optimized.Fragment,
		OptimizationReport: optimized.Report,
		Info:               analysis.InfoFor(node),
	}, true, nil
}
