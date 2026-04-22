package promshim

import (
	"context"
	"time"

	"ch-observability/internal/promshim/exec"
	"ch-observability/internal/promshim/model"
	nativeplan "ch-observability/internal/promshim/native"
	"ch-observability/internal/promshim/storage"

	"github.com/prometheus/prometheus/promql/parser"
)

type evalMode string

const (
	evalModeInstant evalMode = "instant"
	evalModeRange   evalMode = "range"
)

type evalParams struct {
	Mode           evalMode
	EvaluationTime time.Time
	Start          time.Time
	End            time.Time
	Step           time.Duration
}

type queryPlan interface {
	execute(ctx context.Context, evaluator *evaluator, params evalParams) (model.RuntimeValue, error)
	explain() ExplainNode
}

type annotatedQueryPlan struct {
	Inner    queryPlan
	Lowering *nativeplan.LoweringInfo
}

func (p *annotatedQueryPlan) execute(ctx context.Context, evaluator *evaluator, params evalParams) (model.RuntimeValue, error) {
	return p.Inner.execute(ctx, evaluator, params)
}

func (p *annotatedQueryPlan) explain() ExplainNode {
	explain := p.Inner.explain()
	if p.Lowering != nil {
		explain.Lowering = p.Lowering.ExplainInfo()
	}
	return explain
}

func annotateQueryPlan(plan queryPlan, _ *nativeplan.LoweringInfo) queryPlan {
	return plan
}

type delegatedExprPlan struct {
	Expr parser.Expr
}

func (p *delegatedExprPlan) execute(ctx context.Context, evaluator *evaluator, params evalParams) (model.RuntimeValue, error) {
	value, err := evaluator.executeDelegated(ctx, p.Expr, params)
	if err != nil {
		return nil, withInternalContext(err, "executing delegated expression %q in %s mode", p.Expr.String(), params.Mode)
	}
	return value, nil
}

func (p *delegatedExprPlan) explain() ExplainNode {
	return ExplainNode{Kind: "leaf", Strategy: "delegated_promql", Expr: p.Expr.String()}
}

type evaluator struct {
	opts   Options
	client *storage.Client
}

func newEvaluator(opts Options, client *storage.Client) *evaluator {
	return &evaluator{opts: opts, client: client}
}

func (e *evaluator) evaluate(ctx context.Context, plan queryPlan, params evalParams) (model.RuntimeValue, error) {
	value, err := plan.execute(ctx, e, params)
	if err != nil {
		return nil, withInternalContext(err, "evaluating query plan in %s mode", params.Mode)
	}
	return value, nil
}

func (e *evaluator) executeDelegated(ctx context.Context, expr parser.Expr, params evalParams) (model.RuntimeValue, error) {
	promQL, err := resolveDelegatedPromQL(expr, params)
	if err != nil {
		return nil, withInternalContext(err, "resolving delegated PromQL for %q", expr.String())
	}

	switch params.Mode {
	case evalModeInstant:
		sql, queryParams := storage.BuildInstantQuerySQL(storage.QueryConfig{Database: e.opts.Database, Table: e.opts.Table}, promQL, params.EvaluationTime.UnixMilli())
		response, err := e.client.Execute(ctx, sql, queryParams)
		if err != nil {
			return nil, withInternalContext(normalizeInternalError(err), "executing delegated ClickHouse instant query for %q", expr.String())
		}
		defer response.Body.Close()

		switch expr.Type() {
		case parser.ValueTypeVector:
			samples, err := decodeInstantSamples(response.Body)
			if err != nil {
				return nil, withInternalContext(err, "decoding delegated instant vector result for %q", expr.String())
			}
			if isBareSelectorExpr(expr) {
				samples = dropNaNInstantSamples(samples)
			}
			return model.VectorValue{Samples: samples}, nil
		case parser.ValueTypeMatrix:
			series, err := decodeRangeSeries(response.Body)
			if err != nil {
				return nil, withInternalContext(err, "decoding delegated instant matrix result for %q", expr.String())
			}
			if isBareSelectorExpr(expr) {
				series = dropNaNRangePoints(series)
			}
			return model.MatrixValue{Series: series}, nil
		default:
			return nil, newUnsupportedErrorf("delegated instant result type %q for %q is not implemented yet", expr.Type(), expr.String())
		}
	case evalModeRange:
		sql, queryParams := storage.BuildRangeQuerySQL(storage.QueryConfig{Database: e.opts.Database, Table: e.opts.Table}, promQL, params.Start.UnixMilli(), params.End.UnixMilli(), params.Step.Milliseconds())
		response, err := e.client.Execute(ctx, sql, queryParams)
		if err != nil {
			return nil, withInternalContext(normalizeInternalError(err), "executing delegated ClickHouse range query for %q", expr.String())
		}
		defer response.Body.Close()

		series, err := decodeRangeSeries(response.Body)
		if err != nil {
			return nil, withInternalContext(err, "decoding delegated range matrix result for %q", expr.String())
		}
		if isBareSelectorExpr(expr) {
			series = dropNaNRangePoints(series)
		}
		return model.MatrixValue{Series: series}, nil
	default:
		return nil, newExecutionErrorf("unknown evaluation mode %q", params.Mode)
	}
}

func buildPlan(expr parser.Expr) (queryPlan, error) {
	return buildPlanWithContext(expr, defaultPlanContext(evalModeInstant))
}

func buildPlanWithContext(expr parser.Expr, ctx planContext) (queryPlan, error) {
	plan, _, err := buildPlanWithContextAndAnalysis(expr, ctx)
	return plan, err
}

func buildEntireQueryDelegatedPlan(expr parser.Expr) (queryPlan, *nativeplan.Analysis, error) {
	logical, err := buildLogicalPlan(expr)
	if err != nil {
		return nil, nil, err
	}
	analysis := nativeplan.Analyze(logical)
	return annotateQueryPlan(&delegatedExprPlan{Expr: expr}, analysis.Root), analysis, nil
}

func buildPlanWithContextAndAnalysis(expr parser.Expr, ctx planContext) (queryPlan, *nativeplan.Analysis, error) {
	logical, err := buildLogicalPlan(expr)
	if err != nil {
		return nil, nil, err
	}
	analysis := nativeplan.Analyze(logical)
	plan, err := buildExecPlanWithAnalysis(logical, ctx, analysis)
	if err != nil {
		return nil, nil, err
	}
	plan, err = applyRangeExecutionStrategy(plan, ctx)
	if err != nil {
		return nil, nil, withInternalContext(err, "applying range execution strategy for %q", expr.String())
	}
	return plan, analysis, nil
}

func buildExecPlan(plan logicalPlan) (queryPlan, error) {
	analysis := nativeplan.Analyze(plan)
	return buildExecPlanWithAnalysis(plan, defaultPlanContext(evalModeInstant), analysis)
}

func buildExecPlanWithContext(plan logicalPlan, ctx planContext) (queryPlan, error) {
	analysis := nativeplan.Analyze(plan)
	return buildExecPlanWithAnalysis(plan, ctx, analysis)
}

func buildExecPlanWithAnalysis(plan logicalPlan, ctx planContext, analysis *nativeplan.Analysis) (queryPlan, error) {
	switch node := plan.(type) {
	case *logicalLeafExprPlan:
		if ctx.allowsNativePlanning() && ctx.NativeLoweringMode.forcesNativeRoot() {
			if nativePlan, ok, err := maybeBuildNativeLeafPlan(node, ctx, analysis); err != nil {
				return nil, withInternalContext(err, "building native subtree plan for leaf %q", node.ExprString())
			} else if ok {
				return annotateQueryPlan(nativePlan, analysis.InfoFor(node)), nil
			}
		}
		if err := ensureDelegatedExprSupportedForContext(node.Expr, ctx, "delegated leaf planning"); err != nil {
			return nil, err
		}
		return annotateQueryPlan(&delegatedExprPlan{Expr: node.Expr}, analysis.InfoFor(node)), nil
	case *logicalScalarLiteralPlan:
		return annotateQueryPlan(&scalarLiteralPlan{Expr: node.ExprString(), Value: node.Value}, analysis.InfoFor(node)), nil
	case *logicalUnaryPlan:
		child, err := buildExecPlanWithAnalysis(node.Child, ctx, analysis)
		if err != nil {
			return nil, withInternalContext(err, "building execution child plan for unary expression %q", node.ExprString())
		}
		return annotateQueryPlan(&localUnaryPlan{Expr: node.ExprString(), Op: node.Op, Child: child}, analysis.InfoFor(node)), nil
	case *logicalBinaryPlan:
		if ctx.allowsNativePlanning() {
			if nativePlan, ok, err := maybeBuildNativeBinaryPlan(node, ctx, analysis); err != nil {
				return nil, withInternalContext(err, "building native subtree plan for binary expression %q", node.ExprString())
			} else if ok {
				return annotateQueryPlan(nativePlan, analysis.InfoFor(node)), nil
			}
		}
		lhs, err := buildExecPlanWithAnalysis(node.LHS, ctx, analysis)
		if err != nil {
			return nil, withInternalContext(err, "building execution left operand plan for binary expression %q", node.ExprString())
		}
		rhs, err := buildExecPlanWithAnalysis(node.RHS, ctx, analysis)
		if err != nil {
			return nil, withInternalContext(err, "building execution right operand plan for binary expression %q", node.ExprString())
		}
		return annotateQueryPlan(&localBinaryPlan{Expr: node.ExprString(), Op: node.Op, VectorMatching: cloneVectorMatching(node.VectorMatching), ReturnBool: node.ReturnBool, LHS: lhs, RHS: rhs}, analysis.InfoFor(node)), nil
	case *logicalAggregationPlan:
		if ctx.allowsNativePlanning() {
			if pushdownPlan, ok, err := maybeBuildNativeAggregationPlan(node, ctx, analysis); err != nil {
				return nil, withInternalContext(err, "building native subtree plan for aggregate %q", node.ExprString())
			} else if ok {
				return annotateQueryPlan(pushdownPlan, analysis.InfoFor(node)), nil
			}
		}
		child, err := buildExecPlanWithAnalysis(node.Child, ctx, analysis)
		if err != nil {
			return nil, withInternalContext(err, "building execution child plan for aggregate %q", node.ExprString())
		}
		decision := decideNativeAggregationPushdownFromAnalysis(node, analysis, ctx)
		return annotateQueryPlan(&localAggregationPlan{
			Expr:        node.ExprString(),
			Op:          node.Op,
			Grouping:    append([]string(nil), node.Grouping...),
			Without:     node.Without,
			ParamNumber: node.ParamNumber,
			ParamString: node.ParamString,
			Reason:      decision.Reason,
			Child:       child,
		}, analysis.InfoFor(node)), nil
	case *logicalHistogramQuantilePlan:
		if ctx.allowsNativePlanning() {
			if nativePlan, ok, err := maybeBuildNativeHistogramQuantilePlan(node, ctx, analysis); err != nil {
				return nil, withInternalContext(err, "building native subtree plan for histogram_quantile %q", node.ExprString())
			} else if ok {
				return annotateQueryPlan(nativePlan, analysis.InfoFor(node)), nil
			}
		}
		child, err := buildExecPlanWithAnalysis(node.Child, ctx, analysis)
		if err != nil {
			return nil, withInternalContext(err, "building execution child plan for histogram_quantile %q", node.ExprString())
		}
		return annotateQueryPlan(&localHistogramQuantilePlan{Expr: node.ExprString(), Quantile: node.Quantile, Child: child}, analysis.InfoFor(node)), nil
	case *logicalHistogramFractionPlan:
		if ctx.allowsNativePlanning() {
			if nativePlan, ok, err := maybeBuildNativeHistogramFractionPlan(node, ctx, analysis); err != nil {
				return nil, withInternalContext(err, "building native subtree plan for histogram_fraction %q", node.ExprString())
			} else if ok {
				return annotateQueryPlan(nativePlan, analysis.InfoFor(node)), nil
			}
		}
		child, err := buildExecPlanWithAnalysis(node.Child, ctx, analysis)
		if err != nil {
			return nil, withInternalContext(err, "building execution child plan for histogram_fraction %q", node.ExprString())
		}
		return annotateQueryPlan(&localHistogramFractionPlan{Expr: node.ExprString(), Lower: node.Lower, Upper: node.Upper, Child: child}, analysis.InfoFor(node)), nil
	case *logicalHistogramProjectionPlan:
		if ctx.allowsNativePlanning() {
			if nativePlan, ok, err := maybeBuildNativeHistogramProjectionPlan(node, ctx, analysis); err != nil {
				return nil, withInternalContext(err, "building native subtree plan for %s %q", node.Func, node.ExprString())
			} else if ok {
				return annotateQueryPlan(nativePlan, analysis.InfoFor(node)), nil
			}
		}
		child, err := buildExecPlanWithAnalysis(node.Child, ctx, analysis)
		if err != nil {
			return nil, withInternalContext(err, "building execution child plan for %s %q", node.Func, node.ExprString())
		}
		return annotateQueryPlan(&localHistogramProjectionPlan{Expr: node.ExprString(), Func: node.Func, Child: child}, analysis.InfoFor(node)), nil
	case *logicalRangeFunctionPlan:
		if ctx.allowsNativePlanning() {
			if nativePlan, ok, err := maybeBuildNativeRangeFunctionPlan(node, ctx, analysis); err != nil {
				return nil, withInternalContext(err, "building native subtree plan for %s %q", node.Func, node.ExprString())
			} else if ok {
				return annotateQueryPlan(nativePlan, analysis.InfoFor(node)), nil
			}
		}
		child, err := buildExecPlanWithAnalysis(node.Child, ctx, analysis)
		if err != nil {
			return nil, withInternalContext(err, "building execution child plan for %s %q", node.Func, node.ExprString())
		}
		return annotateQueryPlan(&localRangeFunctionPlan{Expr: node.ExprString(), Func: node.Func, ParamNumber: cloneFloat64Pointer(node.ParamNumber), ParamNumbers: cloneFloat64Pointers(node.ParamNumbers), Child: child}, analysis.InfoFor(node)), nil
	case *logicalVectorPlan:
		child, err := buildExecPlanWithAnalysis(node.Child, ctx, analysis)
		if err != nil {
			return nil, withInternalContext(err, "building execution child plan for vector %q", node.ExprString())
		}
		return annotateQueryPlan(&localVectorPlan{Expr: node.ExprString(), Child: child}, analysis.InfoFor(node)), nil
	case *logicalRoundPlan:
		child, err := buildExecPlanWithAnalysis(node.Child, ctx, analysis)
		if err != nil {
			return nil, withInternalContext(err, "building execution child plan for round %q", node.ExprString())
		}
		return annotateQueryPlan(&localRoundPlan{Expr: node.ExprString(), Decimals: node.Decimals, Child: child}, analysis.InfoFor(node)), nil
	case *logicalSortPlan:
		child, err := buildExecPlanWithAnalysis(node.Child, ctx, analysis)
		if err != nil {
			return nil, withInternalContext(err, "building execution child plan for %s %q", node.Func, node.ExprString())
		}
		return annotateQueryPlan(&localSortPlan{Expr: node.ExprString(), Func: node.Func, Labels: append([]string(nil), node.Labels...), Child: child}, analysis.InfoFor(node)), nil
	case *logicalInfoPlan:
		if ctx.allowsNativePlanning() {
			if nativePlan, ok, err := maybeBuildNativeInfoPlan(node, ctx, analysis); err != nil {
				return nil, withInternalContext(err, "building native subtree plan for info %q", node.ExprString())
			} else if ok {
				return annotateQueryPlan(nativePlan, analysis.InfoFor(node)), nil
			}
		}
		child, err := buildExecPlanWithAnalysis(node.Child, ctx, analysis)
		if err != nil {
			return nil, withInternalContext(err, "building execution child plan for info %q", node.ExprString())
		}
		return annotateQueryPlan(&localInfoPlan{Expr: node.ExprString(), SelectorMatchers: clonePromMatchers(node.SelectorMatchers), Child: child}, analysis.InfoFor(node)), nil
	case *logicalScalarConvertPlan:
		if ctx.allowsNativePlanning() {
			if nativePlan, ok, err := maybeBuildNativeScalarConvertPlan(node, ctx, analysis); err != nil {
				return nil, withInternalContext(err, "building native subtree plan for scalar %q", node.ExprString())
			} else if ok {
				return annotateQueryPlan(nativePlan, analysis.InfoFor(node)), nil
			}
		}
		child, err := buildExecPlanWithAnalysis(node.Child, ctx, analysis)
		if err != nil {
			return nil, withInternalContext(err, "building execution child plan for scalar %q", node.ExprString())
		}
		return annotateQueryPlan(&localScalarConvertPlan{Expr: node.ExprString(), Child: child}, analysis.InfoFor(node)), nil
	case *logicalPointwiseFunctionPlan:
		if ctx.allowsNativePlanning() {
			if nativePlan, ok, err := maybeBuildNativeSourcePlan(node, ctx, analysis); err != nil {
				return nil, withInternalContext(err, "building native subtree plan for %s %q", node.Func, node.ExprString())
			} else if ok {
				return annotateQueryPlan(nativePlan, analysis.InfoFor(node)), nil
			}
		}
		var (
			child queryPlan
			err   error
		)
		if node.Child != nil {
			child, err = buildExecPlanWithAnalysis(node.Child, ctx, analysis)
			if err != nil {
				return nil, withInternalContext(err, "building execution child plan for %s %q", node.Func, node.ExprString())
			}
		}
		return annotateQueryPlan(&localPointwiseFunctionPlan{Expr: node.ExprString(), Func: node.Func, ParamNumbers: append([]*float64(nil), node.ParamNumbers...), Child: child}, analysis.InfoFor(node)), nil
	case *logicalScalarBuiltinPlan:
		if ctx.allowsNativePlanning() {
			if nativePlan, ok, err := maybeBuildNativeScalarBuiltinPlan(node, ctx, analysis); err != nil {
				return nil, withInternalContext(err, "building native subtree plan for %s %q", node.Func, node.ExprString())
			} else if ok {
				return annotateQueryPlan(nativePlan, analysis.InfoFor(node)), nil
			}
		}
		return annotateQueryPlan(&scalarBuiltinPlan{Expr: node.ExprString(), Func: node.Func}, analysis.InfoFor(node)), nil
	case *logicalRatePlan:
		if ctx.allowsNativePlanning() {
			if nativePlan, ok, err := maybeBuildNativeRatePlan(node, ctx, analysis); err != nil {
				return nil, withInternalContext(err, "building native subtree plan for %s %q", node.Func, node.ExprString())
			} else if ok {
				return annotateQueryPlan(nativePlan, analysis.InfoFor(node)), nil
			}
		}
		child, err := buildExecPlanWithAnalysis(node.Child, ctx, analysis)
		if err != nil {
			return nil, withInternalContext(err, "building execution child plan for %s %q", node.Func, node.ExprString())
		}
		return annotateQueryPlan(&localRatePlan{Expr: node.ExprString(), Func: node.Func, Child: child}, analysis.InfoFor(node)), nil
	case *logicalIncreasePlan:
		if ctx.allowsNativePlanning() {
			if nativePlan, ok, err := maybeBuildNativeIncreasePlan(node, ctx, analysis); err != nil {
				return nil, withInternalContext(err, "building native subtree plan for increase %q", node.ExprString())
			} else if ok {
				return annotateQueryPlan(nativePlan, analysis.InfoFor(node)), nil
			}
		}
		child, err := buildExecPlanWithAnalysis(node.Child, ctx, analysis)
		if err != nil {
			return nil, withInternalContext(err, "building execution child plan for increase %q", node.ExprString())
		}
		return annotateQueryPlan(&localIncreasePlan{Expr: node.ExprString(), Child: child}, analysis.InfoFor(node)), nil
	case *logicalDeltaPlan:
		if ctx.allowsNativePlanning() {
			if nativePlan, ok, err := maybeBuildNativeDeltaPlan(node, ctx, analysis); err != nil {
				return nil, withInternalContext(err, "building native subtree plan for %s %q", node.Func, node.ExprString())
			} else if ok {
				return annotateQueryPlan(nativePlan, analysis.InfoFor(node)), nil
			}
		}
		child, err := buildExecPlanWithAnalysis(node.Child, ctx, analysis)
		if err != nil {
			return nil, withInternalContext(err, "building execution child plan for %s %q", node.Func, node.ExprString())
		}
		return annotateQueryPlan(&localDeltaPlan{Expr: node.ExprString(), Func: node.Func, Child: child}, analysis.InfoFor(node)), nil
	case *logicalChangesPlan:
		if ctx.allowsNativePlanning() {
			if nativePlan, ok, err := maybeBuildNativeChangesPlan(node, ctx, analysis); err != nil {
				return nil, withInternalContext(err, "building native subtree plan for changes %q", node.ExprString())
			} else if ok {
				return annotateQueryPlan(nativePlan, analysis.InfoFor(node)), nil
			}
		}
		child, err := buildExecPlanWithAnalysis(node.Child, ctx, analysis)
		if err != nil {
			return nil, withInternalContext(err, "building execution child plan for changes %q", node.ExprString())
		}
		return annotateQueryPlan(&localChangesPlan{Expr: node.ExprString(), Child: child}, analysis.InfoFor(node)), nil
	case *logicalDerivPlan:
		if ctx.allowsNativePlanning() {
			if nativePlan, ok, err := maybeBuildNativeDerivPlan(node, ctx, analysis); err != nil {
				return nil, withInternalContext(err, "building native subtree plan for deriv %q", node.ExprString())
			} else if ok {
				return annotateQueryPlan(nativePlan, analysis.InfoFor(node)), nil
			}
		}
		child, err := buildExecPlanWithAnalysis(node.Child, ctx, analysis)
		if err != nil {
			return nil, withInternalContext(err, "building execution child plan for deriv %q", node.ExprString())
		}
		return annotateQueryPlan(&localDerivPlan{Expr: node.ExprString(), Child: child}, analysis.InfoFor(node)), nil
	case *logicalQuantileOverTimePlan:
		child, err := buildExecPlanWithAnalysis(node.Child, ctx, analysis)
		if err != nil {
			return nil, withInternalContext(err, "building execution child plan for quantile_over_time %q", node.ExprString())
		}
		return annotateQueryPlan(&localQuantileOverTimePlan{Expr: node.ExprString(), Quantile: node.Quantile, Child: child}, analysis.InfoFor(node)), nil
	case *logicalAbsentPlan:
		if ctx.allowsNativePlanning() {
			if nativePlan, ok, err := maybeBuildNativeAbsentPlan(node, ctx, analysis); err != nil {
				return nil, withInternalContext(err, "building native subtree plan for absent %q", node.ExprString())
			} else if ok {
				return annotateQueryPlan(nativePlan, analysis.InfoFor(node)), nil
			}
		}
		child, err := buildExecPlanWithAnalysis(node.Child, ctx, analysis)
		if err != nil {
			return nil, withInternalContext(err, "building execution child plan for absent %q", node.ExprString())
		}
		return annotateQueryPlan(&localAbsentPlan{Expr: node.ExprString(), OutputMetric: model.CloneMetric(node.OutputMetric), Child: child}, analysis.InfoFor(node)), nil
	case *logicalAbsentOverTimePlan:
		if ctx.allowsNativePlanning() {
			if nativePlan, ok, err := maybeBuildNativeAbsentOverTimePlan(node, ctx, analysis); err != nil {
				return nil, withInternalContext(err, "building native subtree plan for absent_over_time %q", node.ExprString())
			} else if ok {
				return annotateQueryPlan(nativePlan, analysis.InfoFor(node)), nil
			}
		}
		child, err := buildExecPlanWithAnalysis(node.Child, ctx, analysis)
		if err != nil {
			return nil, withInternalContext(err, "building execution child plan for absent_over_time %q", node.ExprString())
		}
		plan := &localAbsentOverTimePlan{Expr: node.ExprString(), OutputMetric: model.CloneMetric(node.OutputMetric), Child: child}
		if leaf, ok := node.Child.(*logicalLeafExprPlan); ok {
			if matrixSelector, ok := leaf.Expr.(*parser.MatrixSelector); ok {
				if vectorSelector, ok := matrixSelector.VectorSelector.(*parser.VectorSelector); ok {
					plan.BoundaryProbeExpr = &parser.MatrixSelector{VectorSelector: vectorSelector, Range: time.Millisecond}
					plan.BoundaryProbeRange = matrixSelector.Range
				}
			}
		}
		return annotateQueryPlan(plan, analysis.InfoFor(node)), nil
	case *logicalSubqueryPlan:
		child, err := buildExecPlanWithAnalysis(node.Child, ctx, analysis)
		if err != nil {
			return nil, withInternalContext(err, "building execution child plan for subquery %q", node.ExprString())
		}
		return annotateQueryPlan(&localSubqueryPlan{Expr: node.Expr, Range: node.Range, Step: node.Step, Offset: node.Offset, Timestamp: cloneInt64Pointer(node.Timestamp), StartOrEnd: node.StartOrEnd, DelegatedLeafCompatible: node.DelegatedLeafCompatible, Child: child}, analysis.InfoFor(node)), nil
	case *logicalLabelReplacePlan:
		child, err := buildExecPlanWithAnalysis(node.Child, ctx, analysis)
		if err != nil {
			return nil, withInternalContext(err, "building execution child plan for label_replace %q", node.ExprString())
		}
		return annotateQueryPlan(&localLabelReplacePlan{Expr: node.ExprString(), Config: node.Config, Child: child}, analysis.InfoFor(node)), nil
	case *logicalLabelJoinPlan:
		child, err := buildExecPlanWithAnalysis(node.Child, ctx, analysis)
		if err != nil {
			return nil, withInternalContext(err, "building execution child plan for label_join %q", node.ExprString())
		}
		return annotateQueryPlan(&localLabelJoinPlan{Expr: node.ExprString(), Config: node.Config, Child: child}, analysis.InfoFor(node)), nil
	default:
		return nil, newExecutionErrorf("execution planner cannot lower logical node %T", plan)
	}
}

func toExecEvalMode(mode evalMode) exec.EvalMode {
	if mode == evalModeRange {
		return exec.EvalModeRange
	}
	return exec.EvalModeInstant
}
