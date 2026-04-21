package promshim

import (
	"context"

	"ch-observability/internal/promshim/model"
	nativeplan "ch-observability/internal/promshim/native"
	"ch-observability/internal/promshim/storage"
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

	switch params.Mode {
	case evalModeInstant:
		samples, err := decodeInstantSamples(response.Body)
		if err != nil {
			return nil, withInternalContext(err, "decoding native subtree instant result for %q", p.Expr)
		}
		return model.VectorValue{Samples: samples}, nil
	case evalModeRange:
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
	return ExplainNode{
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
