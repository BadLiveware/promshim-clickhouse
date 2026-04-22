package local

import (
	"context"
	"math"
	"time"

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
	Fragment           *nativeplan.NativeFragment
	OptimizationReport *nativeplan.OptimizationReport
	Info               *nativeplan.LoweringInfo
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
		if s, e, ok := nativeplan.RequiredInputBounds(p.Fragment, p.Info, execCtx); ok {
			requiredStartMS = s
			requiredEndMS = e
		}
	}
	rendered, err := renderer.RenderFragment(storage.QueryConfig{Database: Evaluator.database, Table: Evaluator.table}, p.Fragment, renderer.RenderParams{
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
	})
	if err != nil {
		return nil, WithInternalContext(err, "rendering native subtree SQL for %q", p.Expr)
	}
	response, err := Evaluator.client.Execute(ctx, rendered.SQL, rendered.QueryParams)
	if err != nil {
		return nil, WithInternalContext(NormalizeInternalError(err), "executing native subtree query for %q", p.Expr)
	}
	defer response.Body.Close()

	switch {
	case params.Mode == EvalModeInstant && p.Fragment != nil && p.Fragment.OutputKind == nativeplan.OutputKindRangeMatrix:
		series, err := DecodeRangeSeries(response.Body)
		if err != nil {
			return nil, WithInternalContext(err, "decoding native subtree instant matrix result for %q", p.Expr)
		}
		return model.MatrixValue{Series: series}, nil
	case params.Mode == EvalModeInstant && p.Fragment != nil && p.Fragment.OutputKind == nativeplan.OutputKindScalar:
		samples, err := DecodeInstantSamples(response.Body)
		if err != nil {
			return nil, WithInternalContext(err, "decoding native subtree instant scalar result for %q", p.Expr)
		}
		if len(samples) != 1 {
			return nil, NewExecutionErrorf("native subtree scalar result for %q returned %d samples, expected exactly 1", p.Expr, len(samples))
		}
		return model.ScalarValue{Timestamp: samples[0].Timestamp, Value: applyNativeRuntimeTransform(samples[0].Value, runtimeValueTransformForFragment(p.Fragment))}, nil
	case params.Mode == EvalModeInstant:
		samples, err := DecodeInstantSamples(response.Body)
		if err != nil {
			return nil, WithInternalContext(err, "decoding native subtree instant result for %q", p.Expr)
		}
		evalTimestamp := float64(params.EvaluationTime.UnixNano()) / float64(time.Second)
		runtimeTransform := runtimeValueTransformForFragment(p.Fragment)
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
		runtimeTransform := runtimeValueTransformForFragment(p.Fragment)
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

func renderModeForPlanContext(ctx PlanContext) nativeplan.RenderMode {
	if ctx.Mode == EvalModeRange {
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

func runtimeValueTransformForFragment(fragment *nativeplan.NativeFragment) *nativeplan.RuntimeValueTransform {
	if fragment == nil || fragment.ValueTransform == nil {
		return nil
	}
	return fragment.ValueTransform.RuntimeTransform
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
