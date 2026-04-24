package local

import (
	"context"
	"fmt"
	"math"
	"time"

	logicalpkg "ch-observability/internal/promshim/logical"
	"ch-observability/internal/promshim/model"
	nativeplan "ch-observability/internal/promshim/native"
	"ch-observability/internal/promshim/native/renderer"
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
	OptimizationReport *nativeplan.OptimizationReport
	Info               *nativeplan.LoweringInfo
	// LogicalRoot and LogicalAnalysis are populated only for the
	// whole-query tier-2 plan. When set, execute() attempts
	// renderer.Lower(ctx, LogicalRoot) first and falls back to
	// renderer.RenderFragment on the errUnsupportedLowerNode sentinel.
	// Subtree-pushdown construction sites (tier 3a) leave these nil
	// and stay on the Fragment path unconditionally.
	LogicalRoot     logicalpkg.Node
	LogicalAnalysis *logicalpkg.Analysis
	// Node and Analysis carry the logical node and native analysis for
	// this subtree. Populated at construction by every
	// maybeBuildNative*Plan site so a later 13b-d port can flip the
	// renderSQL path to consume the logical plan directly via
	// renderer.RenderLogical instead of renderer.RenderFragment.
	// Distinct from LogicalRoot/LogicalAnalysis which are set only for
	// the whole-query tier-2 plan.
	Node     logicalpkg.Node
	Analysis *nativeplan.Analysis
}

// rootLogicalNode returns the logical plan root for this subtree: the
// per-subtree Node when set (tier-3 construction), else the whole-query
// LogicalRoot (tier-2 attachLogicalRootForLower). nil only in degenerate
// cases where neither field is populated.
func (p *nativeSubtreePlan) rootLogicalNode() logicalpkg.Node {
	if p == nil {
		return nil
	}
	if p.Node != nil {
		return p.Node
	}
	return p.LogicalRoot
}

func (p *nativeSubtreePlan) execute(ctx context.Context, Evaluator *Evaluator, params EvalParams) (model.RuntimeValue, error) {
	renderMode := nativeplan.RenderModeInstant
	if params.Mode == EvalModeRange {
		renderMode = nativeplan.RenderModeRange
	}
	report := &nativeplan.OptimizationReport{}
	if p.OptimizationReport != nil {
		report = p.OptimizationReport
	}
	requiredStartMS := report.RequiredInputStartMS
	requiredEndMS := report.RequiredInputEndMS
	if p.Info != nil {
		execCtx := nativeplan.OptimizationContext{
			Mode:             renderMode,
			EvaluationTimeMS: params.EvaluationTime.UnixMilli(),
			StartMS:          params.Start.UnixMilli(),
			EndMS:            params.End.UnixMilli(),
			StepMS:           params.Step.Milliseconds(),
		}
		if root := p.rootLogicalNode(); root != nil {
			if s, e, ok := renderer.LogicalRequiredInputBounds(root, execCtx); ok {
				requiredStartMS = s
				requiredEndMS = e
			}
		}
	}
	cfg := storage.QueryConfig{Database: Evaluator.database, Table: Evaluator.table}
	renderParams := renderer.RenderParams{
		Mode:             renderMode,
		EvaluationTimeMS: params.EvaluationTime.UnixMilli(),
		StartMS:          params.Start.UnixMilli(),
		EndMS:            params.End.UnixMilli(),
		StepMS:           params.Step.Milliseconds(),
		RequiredStartMS:  requiredStartMS,
		RequiredEndMS:    requiredEndMS,
		ResolveSourcePromQL: func(expr parser.Expr) (string, error) {
			return resolveDelegatedPromQL(expr, params)
		},
	}
	rendered, err := p.renderSQL(cfg, renderParams)
	if err != nil {
		return nil, err
	}
	response, err := Evaluator.client.Execute(ctx, rendered.SQL, rendered.QueryParams)
	if err != nil {
		return nil, WithInternalContext(NormalizeInternalError(err), "executing native subtree query for %q", p.Expr)
	}
	defer response.Body.Close()

	switch {
	case params.Mode == EvalModeInstant && p.Info != nil && p.Info.OutputKind == nativeplan.OutputKindRangeMatrix:
		series, err := DecodeRangeSeries(response.Body)
		if err != nil {
			return nil, WithInternalContext(err, "decoding native subtree instant matrix result for %q", p.Expr)
		}
		return model.MatrixValue{Series: series}, nil
	case params.Mode == EvalModeInstant && p.Info != nil && p.Info.OutputKind == nativeplan.OutputKindScalar:
		samples, err := DecodeInstantSamples(response.Body)
		if err != nil {
			return nil, WithInternalContext(err, "decoding native subtree instant scalar result for %q", p.Expr)
		}
		if len(samples) != 1 {
			return nil, NewExecutionErrorf("native subtree scalar result for %q returned %d samples, expected exactly 1", p.Expr, len(samples))
		}
		return model.ScalarValue{Timestamp: samples[0].Timestamp, Value: applyNativeRuntimeTransform(samples[0].Value, runtimeValueTransformForPlan(p))}, nil
	case params.Mode == EvalModeInstant:
		samples, err := DecodeInstantSamples(response.Body)
		if err != nil {
			return nil, WithInternalContext(err, "decoding native subtree instant result for %q", p.Expr)
		}
		evalTimestamp := float64(params.EvaluationTime.UnixNano()) / float64(time.Second)
		runtimeTransform := runtimeValueTransformForPlan(p)
		for i := range samples {
			samples[i].Timestamp = evalTimestamp
			samples[i].Value = applyNativeRuntimeTransform(samples[i].Value, runtimeTransform)
		}
		return model.VectorValue{Samples: samples}, nil
	case params.Mode == EvalModeRange:
		series, err := DecodeRangeSeries(response.Body)
		if err != nil {
			return nil, WithInternalContext(err, "decoding native subtree range result for %q", p.Expr)
		}
		runtimeTransform := runtimeValueTransformForPlan(p)
		for i := range series {
			for j := range series[i].Values {
				series[i].Values[j].Value = applyNativeRuntimeTransform(series[i].Values[j].Value, runtimeTransform)
			}
		}
		return model.MatrixValue{Series: series}, nil
	default:
		return nil, NewExecutionErrorf("unknown evaluation mode %q", params.Mode)
	}
}

// renderSQL picks between the renderer.Lower dispatcher and the
// Fragment renderer. Two Lower entry points are supported:
//
//  1. Whole-query tier-2: LogicalRoot + LogicalAnalysis are populated
//     by attachLogicalRootForLower in the planner, so the logical
//     analysis is already cached by BuildPlanWithContextAndAnalysis.
//  2. Tier-3 subtree pushdown: Node + Analysis are populated at
//     construction in every maybeBuildNative*Plan site. No logical
//     analysis is cached upstream, so one is computed here lazily
//     via logicalpkg.Analyze(Node); the native analysis is reused
//     from p.Analysis.
//
// When Lower reports errUnsupportedLowerNode, the call falls through
// to the Fragment path so the query still renders correctly.
func (p *nativeSubtreePlan) renderSQL(cfg storage.QueryConfig, renderParams renderer.RenderParams) (renderer.RenderedQuery, error) {
	var (
		lowerNode        logicalpkg.Node
		lowerAnalysis    *logicalpkg.Analysis
		lowerNativeAnaly *nativeplan.Analysis
	)
	switch {
	case p.LogicalRoot != nil && p.LogicalAnalysis != nil:
		lowerNode = p.LogicalRoot
		lowerAnalysis = p.LogicalAnalysis
		lowerNativeAnaly = nativeplan.Analyze(p.LogicalRoot)
	case p.Node != nil && p.Analysis != nil:
		lowerNode = p.Node
		lowerAnalysis = logicalpkg.Analyze(p.Node)
		lowerNativeAnaly = p.Analysis
	}
	rq, err := renderNativeSubtreeSQL(cfg, renderParams, lowerNode, lowerAnalysis, lowerNativeAnaly)
	if err != nil {
		return renderer.RenderedQuery{}, WithInternalContext(err, "rendering native subtree SQL for %q", p.Expr)
	}
	return rq, nil
}

// renderNativeSubtreeSQL is the shared Lower-based render dispatch
// used by both construction-time pre-render (for explain metadata)
// and execute-time renderSQL. Requires (node, analysis, nativeAnalysis)
// to be non-nil — every tier-3 subtree plan and the tier-2 whole-query
// plan populate them. Errors from Lower — including the
// errUnsupportedLowerNode sentinel — surface to the caller as hard
// failures; there is no Fragment fallback.
func renderNativeSubtreeSQL(cfg storage.QueryConfig, renderParams renderer.RenderParams, node logicalpkg.Node, analysis *logicalpkg.Analysis, nativeAnalysis *nativeplan.Analysis) (renderer.RenderedQuery, error) {
	if node == nil || analysis == nil || nativeAnalysis == nil {
		return renderer.RenderedQuery{}, fmt.Errorf("native subtree render requires logical node, logical analysis, and native analysis")
	}
	return renderer.Lower(renderer.LoweringCtx{
		Config:         cfg,
		Analysis:       analysis,
		NativeAnalysis: nativeAnalysis,
		Params:         renderParams,
	}, node)
}

// preRenderNativeSubtreePlanSQL pre-renders the optimized fragment
// at plan-construction time using preview db/table placeholders and
// populates the OptimizationReport's RenderedSQL + MaterializedColumns
// via ApplyRenderedSQLMetadata. This keeps explain-only endpoints
// (query_explain) able to surface the SQL without executing the query.
//
// The render dispatches through renderNativeSubtreeSQL so the Lower
// path is used when the node supports it, matching the execute-time
// dispatch in renderSQL.
func preRenderNativeSubtreePlanSQL(node logicalpkg.Node, analysis *nativeplan.Analysis, optimized *nativeplan.OptimizedFragment, ctx PlanContext) error {
	renderMode := renderModeForPlanContext(ctx)
	cfg := storage.QueryConfig{Database: "preview", Table: "preview"}
	renderParams := renderer.RenderParams{
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
	}
	var logicalAnalysis *logicalpkg.Analysis
	if node != nil {
		logicalAnalysis = logicalpkg.Analyze(node)
	}
	rendered, err := renderNativeSubtreeSQL(cfg, renderParams, node, logicalAnalysis, analysis)
	if err != nil {
		return err
	}
	return nativeplan.ApplyRenderedSQLMetadata(optimized.Report, renderMode, rendered.SQL)
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
	if p.Info != nil && p.Info.JoinShape != "" {
		explain.JoinShape = p.Info.JoinShape
		explain.JoinLabels = append([]string(nil), p.Info.JoinLabels...)
	}
	return explain
}

func renderModeForPlanContext(ctx PlanContext) nativeplan.RenderMode {
	if ctx.Mode == EvalModeRange {
		return nativeplan.RenderModeRange
	}
	return nativeplan.RenderModeInstant
}

// rejectRangeModeFixedTemporalAnchor is a no-op hook reserved for future
// range-mode plan gating. It accepts the OptimizedFragment so tier-3
// construction callers can drive range-mode rejection without reading
// info.Fragment themselves.
func rejectRangeModeFixedTemporalAnchor(ctx PlanContext, optimized *nativeplan.OptimizedFragment) bool {
	_ = ctx
	_ = optimized
	return false
}

// buildNativeSubtreeChildren builds the ExplainNode children list for a
// tier-3 subtree plan from the node's analysis-time children. Factored
// out of every maybeBuildNative* dispatcher so construction sites don't
// iterate info.Children inline.
func buildNativeSubtreeChildren(info *nativeplan.LoweringInfo) []ExplainNode {
	if info == nil {
		return nil
	}
	children := []ExplainNode{}
	for _, child := range info.Children {
		if child == nil {
			continue
		}
		children = append(children, explainNativeAggregationSource(child))
	}
	return children
}

// newNativeSubtreePlan constructs a nativeSubtreePlan from an
// OptimizedFragment and its LoweringInfo.
func newNativeSubtreePlan(kind, expr, reason string, estimate *planEstimate, children []ExplainNode, optimized *nativeplan.OptimizedFragment, info *nativeplan.LoweringInfo, node logicalpkg.Node, analysis *nativeplan.Analysis) *nativeSubtreePlan {
	return &nativeSubtreePlan{
		Kind:               kind,
		Expr:               expr,
		Reason:             reason,
		Estimate:           estimate,
		Children:           children,
		OptimizationReport: optimized.Report,
		Info:               info,
		Node:               node,
		Analysis:           analysis,
	}
}

func nativeAggregationSourceFromLowering(info *nativeplan.LoweringInfo) (nativeAggregationSource, bool) {
	if info == nil || info.Aggregation == nil || info.Aggregation.SourceView == nil {
		return nativeAggregationSource{}, false
	}
	view := info.Aggregation.SourceView
	return nativeAggregationSource{
		PromQLLeaf:  view.SourcePromQL,
		ValueExpr:   view.ValueExpr,
		TagsExpr:    view.TagsExpr,
		DropsMetric: view.DropsMetric,
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
	if info.SubtreeShape == nativeplan.SubtreeShapeLeafSource {
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

func runtimeValueTransformForPlan(p *nativeSubtreePlan) *nativeplan.RuntimeValueTransform {
	if p == nil || p.Info == nil {
		return nil
	}
	return p.Info.RuntimeValueTransform
}

func applyNativeRuntimeTransform(value float64, transform *nativeplan.RuntimeValueTransform) float64 {
	if transform == nil {
		return value
	}
	switch transform.Op {
	case nativeplan.RuntimeValueTransformPromQLModulo:
		if transform.Scalar == nil {
			return value
		}
		lhs, rhs := value, *transform.Scalar
		if transform.ScalarOnLeft {
			lhs, rhs = rhs, lhs
		}
		return math.Mod(lhs, rhs)
	default:
		return value
	}
}
