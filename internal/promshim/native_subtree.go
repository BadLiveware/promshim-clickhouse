package promshim

import (
	"context"

	"github.com/BadLiveware/promshim-ch/internal/promshim/model"
	nativeplan "github.com/BadLiveware/promshim-ch/internal/promshim/native"
	planpkg "github.com/BadLiveware/promshim-ch/internal/promshim/plan"
	"github.com/BadLiveware/promshim-ch/internal/promshim/storage"
	"github.com/prometheus/prometheus/promql/parser"
)

type nativeAggregationSource struct {
	PromQLLeaf  parser.Expr
	ValueExpr   string
	TagsExpr    string
	Explain     ExplainNode
	DropsMetric bool
}

type nativeSubtreePlan struct {
	Kind               string
	Expr               string
	Reason             string
	Estimate           *planEstimate
	Children           []ExplainNode
	Fragment           *nativeplan.NativeFragment
	OptimizationReport *nativeplan.OptimizationReport
}

func (p *nativeSubtreePlan) execute(ctx context.Context, evaluator *evaluator, params evalParams) (model.RuntimeValue, error) {
	renderMode := nativeplan.RenderModeInstant
	if params.Mode == evalModeRange {
		renderMode = nativeplan.RenderModeRange
	}
	report := &nativeplan.OptimizationReport{}
	if p.OptimizationReport != nil {
		report = p.OptimizationReport
	}
	rendered, err := nativeplan.RenderFragment(storage.QueryConfig{Database: evaluator.opts.Database, Table: evaluator.opts.Table}, p.Fragment, nativeplan.RenderParams{
		Mode:             renderMode,
		EvaluationTimeMS: params.EvaluationTime.UnixMilli(),
		StartMS:          params.Start.UnixMilli(),
		EndMS:            params.End.UnixMilli(),
		StepMS:           params.Step.Milliseconds(),
		RequiredStartMS:  report.RequiredInputStartMS,
		RequiredEndMS:    report.RequiredInputEndMS,
		ResolveSourcePromQL: func(expr parser.Expr) (string, error) {
			return resolveDelegatedPromQL(expr, params)
		},
	})
	if err != nil {
		return nil, withInternalContext(err, "rendering native subtree SQL for %q", p.Expr)
	}
	response, err := evaluator.client.Execute(ctx, rendered.SQL, rendered.QueryParams)
	if err != nil {
		return nil, withInternalContext(normalizeInternalError(err), "executing native subtree query for %q", p.Expr)
	}
	defer response.Body.Close()

	switch {
	case params.Mode == evalModeInstant && p.Fragment != nil && p.Fragment.OutputKind == nativeplan.OutputKindRangeMatrix:
		series, err := decodeRangeSeries(response.Body)
		if err != nil {
			return nil, withInternalContext(err, "decoding native subtree instant matrix result for %q", p.Expr)
		}
		return model.MatrixValue{Series: series}, nil
	case params.Mode == evalModeInstant && p.Fragment != nil && p.Fragment.OutputKind == nativeplan.OutputKindScalar:
		samples, err := decodeInstantSamples(response.Body)
		if err != nil {
			return nil, withInternalContext(err, "decoding native subtree instant scalar result for %q", p.Expr)
		}
		if len(samples) != 1 {
			return nil, newExecutionErrorf("native subtree scalar result for %q returned %d samples, expected exactly 1", p.Expr, len(samples))
		}
		return model.ScalarValue{Timestamp: samples[0].Timestamp, Value: samples[0].Value}, nil
	case params.Mode == evalModeInstant:
		samples, err := decodeInstantSamples(response.Body)
		if err != nil {
			return nil, withInternalContext(err, "decoding native subtree instant result for %q", p.Expr)
		}
		return model.VectorValue{Samples: samples}, nil
	case params.Mode == evalModeRange:
		series, err := decodeRangeSeries(response.Body)
		if err != nil {
			return nil, withInternalContext(err, "decoding native subtree range result for %q", p.Expr)
		}
		return model.MatrixValue{Series: series}, nil
	default:
		return nil, newExecutionErrorf("unknown evaluation mode %q", params.Mode)
	}
}

func (p *nativeSubtreePlan) explain() ExplainNode {
	children := append([]ExplainNode(nil), p.Children...)
	report := &nativeplan.OptimizationReport{}
	if p.OptimizationReport != nil {
		report = p.OptimizationReport
	}
	explain := ExplainNode{
		Kind:                p.Kind,
		Strategy:            "native_sql",
		Expr:                p.Expr,
		Reason:              p.Reason,
		Estimate:            p.Estimate,
		Children:            children,
		RulesApplied:        append([]string(nil), report.RulesApplied...),
		PushedPredicates:    append([]string(nil), report.PushedPredicates...),
		InferredPredicates:  append([]string(nil), report.InferredPredicates...),
		RequiredColumns:     append([]string(nil), report.RequiredColumns...),
		MaterializedColumns: append([]string(nil), report.MaterializedColumns...),
		SemanticBarriers:    append([]string(nil), report.SemanticBarriers...),
		RenderedSQL:         report.RenderedSQL,
	}
	if p.Fragment != nil && p.Fragment.BinaryJoin != nil {
		explain.JoinShape = p.Fragment.BinaryJoin.JoinShape
		if matching := p.Fragment.BinaryJoin.VectorMatching; matching != nil {
			explain.JoinLabels = append([]string(nil), matching.MatchingLabels...)
		}
	}
	return explain
}

func maybeBuildNativeRangeFunctionPlan(node *logicalRangeFunctionPlan, ctx planContext, analysis *nativeplan.Analysis) (queryPlan, bool, error) {
	if ctx.Mode == evalModeRange {
		info := analysis.InfoFor(node)
		if info == nil || info.Fragment == nil || info.Fragment.RangeFunction == nil {
			return nil, false, nil
		}
		if !nativeplan.IsSupportedNativeRangeModeForDirectSelector(info.Fragment) && !nativeplan.IsSupportedNativeRangeModeForAggregateOverTimeSubquery(info.Fragment) {
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
	}, true, nil
}

func maybeBuildNativeSourcePlan(node *logicalPointwiseFunctionPlan, ctx planContext, analysis *nativeplan.Analysis) (queryPlan, bool, error) {
	if ctx.Mode != evalModeInstant && ctx.Mode != evalModeRange {
		return nil, false, nil
	}
	info := analysis.InfoFor(node)
	if info == nil || info.Fragment == nil {
		return nil, false, nil
	}
	switch info.Fragment.Kind {
	case nativeplan.FragmentKindLeafSource, nativeplan.FragmentKindUnarySourceExpr, nativeplan.FragmentKindBinaryScalarSourceExpr, nativeplan.FragmentKindSyntheticSeries:
	default:
		return nil, false, nil
	}
	optimized, err := nativeplan.BuildOptimizedFragmentWithContext(node, analysis, nativeplan.OptimizationContext{Mode: renderModeForPlanContext(ctx), EvaluationTimeMS: ctx.EvaluationTime.UnixMilli(), StartMS: ctx.Start.UnixMilli(), EndMS: ctx.End.UnixMilli(), StepMS: ctx.Step.Milliseconds()})
	if err != nil {
		return nil, false, err
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
	return &nativeSubtreePlan{Kind: node.Func, Expr: node.ExprString(), Reason: info.NativeReason, Estimate: estimateRangePlan(ctx), Children: children, Fragment: optimized.Fragment, OptimizationReport: optimized.Report}, true, nil
}

func maybeBuildNativeInfoPlan(node *logicalInfoPlan, ctx planContext, analysis *nativeplan.Analysis) (queryPlan, bool, error) {
	if ctx.Mode != evalModeInstant && ctx.Mode != evalModeRange {
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
	return &nativeSubtreePlan{Kind: "info", Expr: node.ExprString(), Reason: info.NativeReason, Estimate: estimateRangePlan(ctx), Children: children, Fragment: optimized.Fragment, OptimizationReport: optimized.Report}, true, nil
}

func maybeBuildNativeScalarConvertPlan(node *logicalScalarConvertPlan, ctx planContext, analysis *nativeplan.Analysis) (queryPlan, bool, error) {
	if ctx.Mode != evalModeInstant && ctx.Mode != evalModeRange {
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
	return &nativeSubtreePlan{Kind: "scalar", Expr: node.ExprString(), Reason: info.NativeReason, Estimate: estimateRangePlan(ctx), Children: children, Fragment: optimized.Fragment, OptimizationReport: optimized.Report}, true, nil
}

func maybeBuildNativeScalarBuiltinPlan(node *logicalScalarBuiltinPlan, ctx planContext, analysis *nativeplan.Analysis) (queryPlan, bool, error) {
	if ctx.Mode != evalModeInstant && ctx.Mode != evalModeRange {
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
	return &nativeSubtreePlan{Kind: node.Func, Expr: node.ExprString(), Reason: info.NativeReason, Estimate: estimateRangePlan(ctx), Fragment: optimized.Fragment, OptimizationReport: optimized.Report}, true, nil
}

func maybeBuildNativeBinaryPlan(node *logicalBinaryPlan, ctx planContext, analysis *nativeplan.Analysis) (queryPlan, bool, error) {
	info := analysis.InfoFor(node)
	if info == nil || info.Fragment == nil || info.Fragment.Kind != nativeplan.FragmentKindBinaryVectorJoin {
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
		Kind:               "binary",
		Expr:               node.ExprString(),
		Reason:             info.NativeReason,
		Estimate:           estimateRangePlan(ctx),
		Children:           children,
		Fragment:           optimized.Fragment,
		OptimizationReport: optimized.Report,
	}, true, nil
}

func maybeBuildNativeAggregationPlan(node *logicalAggregationPlan, ctx planContext, analysis *nativeplan.Analysis) (queryPlan, bool, error) {
	decision := decideNativeAggregationPushdownFromAnalysis(node, analysis, ctx)
	if !decision.Eligible {
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
	renderMode := nativeplan.RenderModeInstant
	if ctx.Mode == evalModeRange {
		renderMode = nativeplan.RenderModeRange
	}
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
	if decision.Source.Explain.Strategy != "" {
		children = append(children, decision.Source.Explain)
	}
	return &nativeSubtreePlan{
		Kind:               "aggregation",
		Expr:               node.ExprString(),
		Reason:             decision.Reason,
		Estimate:           estimateRangePlan(ctx),
		Children:           children,
		Fragment:           optimized.Fragment,
		OptimizationReport: optimized.Report,
	}, true, nil
}

func renderModeForPlanContext(ctx planContext) nativeplan.RenderMode {
	if ctx.Mode == evalModeRange {
		return nativeplan.RenderModeRange
	}
	return nativeplan.RenderModeInstant
}

func nativeAggregationSourceFromLowering(info *nativeplan.LoweringInfo) (nativeAggregationSource, bool) {
	if info == nil || info.Aggregation == nil || info.Aggregation.Source == nil || info.Aggregation.Source.SourcePromQL == nil {
		return nativeAggregationSource{}, false
	}
	source := info.Aggregation.Source
	return nativeAggregationSource{
		PromQLLeaf:  source.SourcePromQL,
		ValueExpr:   source.ValueExpr,
		TagsExpr:    source.TagsExpr,
		DropsMetric: source.DropsMetric,
		Explain:     explainNativeAggregationSource(firstNonNilChild(info.Children)),
	}, true
}

func firstNonNilChild(children []*nativeplan.LoweringInfo) *nativeplan.LoweringInfo {
	for _, child := range children {
		if child != nil {
			return child
		}
	}
	return nil
}

func explainNativeAggregationSource(info *nativeplan.LoweringInfo) ExplainNode {
	if info == nil {
		return ExplainNode{}
	}
	strategy := "native_sql_expression"
	if info.Fragment != nil && info.Fragment.Kind == nativeplan.FragmentKindLeafSource {
		strategy = "delegated_promql"
	}
	children := make([]ExplainNode, 0, len(info.Children))
	for _, child := range info.Children {
		if child == nil {
			continue
		}
		children = append(children, explainNativeAggregationSource(child))
	}
	return ExplainNode{
		Kind:     info.NodeType,
		Strategy: strategy,
		Expr:     info.Expr,
		Reason:   info.NativeReason,
		Lowering: info.ExplainInfo(),
		Children: children,
	}
}
