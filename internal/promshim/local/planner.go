package local

import (
	"context"
	"time"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/local/exec"
	logicalpkg "github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
	logicalopt "github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical/opt"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/model"
	nativeplan "github.com/BadLiveware/promshim-clickhouse/internal/promshim/native"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/native/renderer"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/storage"

	"github.com/prometheus/prometheus/promql/parser"
)

type EvalMode string

const (
	EvalModeInstant EvalMode = "instant"
	EvalModeRange   EvalMode = "range"
)

type EvalParams struct {
	Mode           EvalMode
	EvaluationTime time.Time
	Start          time.Time
	End            time.Time
	Step           time.Duration
}

type Plan interface {
	execute(ctx context.Context, Evaluator *Evaluator, params EvalParams) (model.RuntimeValue, error)
	explain() ExplainNode
}

func annotateQueryPlan(plan Plan, _ *nativeplan.LoweringInfo) Plan {
	return plan
}

func histogramChildPlanContext(ctx PlanContext, child logicalpkg.Node) PlanContext {
	// In local_pushdown mode histogram roots stay local while their immediate
	// aggregation child may render as a native subtree. Thread the histogram
	// renderer's existing tag-narrowing contract to that child only; generic
	// aggregation-over-rate shapes must still preserve full labels.
	if !NormalizeNativeLoweringMode(ctx.NativeLoweringMode).ForcesLocalRoot() {
		return ctx
	}
	agg, ok := child.(*logicalAggregationPlan)
	if !ok {
		return ctx
	}
	requireFullTags, requiredLabels := renderer.HistogramChildTagNarrowing(agg)
	if requireFullTags || len(requiredLabels) == 0 {
		return ctx
	}
	out := ctx
	out.NativeSubtreeRenderTagHint = true
	out.NativeSubtreeRequireFullTags = requireFullTags
	out.NativeSubtreeRequiredTagLabels = append([]string(nil), requiredLabels...)
	return out
}

func clearNativeSubtreeRenderTagHint(ctx PlanContext) PlanContext {
	ctx.NativeSubtreeRenderTagHint = false
	ctx.NativeSubtreeRequireFullTags = false
	ctx.NativeSubtreeRequiredTagLabels = nil
	return ctx
}

type delegatedExprPlan struct {
	Expr parser.Expr
}

func (p *delegatedExprPlan) execute(ctx context.Context, Evaluator *Evaluator, params EvalParams) (model.RuntimeValue, error) {
	value, err := Evaluator.executeDelegated(ctx, p.Expr, params)
	if err != nil {
		return nil, WithInternalContext(err, "executing delegated expression %q in %s mode", p.Expr.String(), params.Mode)
	}
	return value, nil
}

func (p *delegatedExprPlan) explain() ExplainNode {
	return ExplainNode{Kind: "leaf", Strategy: "delegated_promql", Expr: p.Expr.String()}
}

type Evaluator struct {
	database                    string
	table                       string
	promotedTagColumns          map[string]struct{}
	enableNativeGridFunctions   bool
	enableCumulativeAvgOverTime bool
	client                      *storage.Client
	localMemo                   map[string]model.RuntimeValue
}

func NewEvaluator(database, table string, client *storage.Client) *Evaluator {
	return &Evaluator{database: database, table: table, client: client}
}

func (e *Evaluator) WithPromotedTagColumns(columns map[string]struct{}) *Evaluator {
	e.promotedTagColumns = columns
	return e
}

func (e *Evaluator) WithNativeGridFunctions(enabled bool) *Evaluator {
	e.enableNativeGridFunctions = enabled
	return e
}

func (e *Evaluator) WithCumulativeAvgOverTime(enabled bool) *Evaluator {
	e.enableCumulativeAvgOverTime = enabled
	return e
}

func (e *Evaluator) queryConfig() storage.QueryConfig {
	return storage.QueryConfig{Database: e.database, Table: e.table, PromotedTagColumns: e.promotedTagColumns, EnableNativeGridFunctions: e.enableNativeGridFunctions, EnableCumulativeAvgOverTime: e.enableCumulativeAvgOverTime}
}

func (e *Evaluator) Evaluate(ctx context.Context, plan Plan, params EvalParams) (model.RuntimeValue, error) {
	requestEvaluator := *e
	requestEvaluator.localMemo = map[string]model.RuntimeValue{}
	value, err := plan.execute(ctx, &requestEvaluator, params)
	if err != nil {
		return nil, WithInternalContext(err, "evaluating query plan in %s mode", params.Mode)
	}
	return value, nil
}

func (e *Evaluator) executeDelegated(ctx context.Context, expr parser.Expr, params EvalParams) (model.RuntimeValue, error) {
	promQL, err := resolveDelegatedPromQL(expr, params)
	if err != nil {
		return nil, WithInternalContext(err, "resolving delegated PromQL for %q", expr.String())
	}

	switch params.Mode {
	case EvalModeInstant:
		sql, queryParams := storage.BuildInstantQuerySQL(e.queryConfig(), promQL, params.EvaluationTime.UnixMilli())
		switch expr.Type() {
		case parser.ValueTypeVector:
			samples, err := e.executeDelegatedInstantSamples(ctx, sql, queryParams)
			if err != nil {
				return nil, WithInternalContext(NormalizeInternalError(err), "executing/decoding delegated instant vector result for %q", expr.String())
			}
			if isBareSelectorExpr(expr) {
				samples = dropNaNInstantSamples(samples)
			}
			return model.VectorValue{Samples: samples}, nil
		case parser.ValueTypeMatrix:
			series, err := e.executeDelegatedRangeSeries(ctx, sql, queryParams)
			if err != nil {
				return nil, WithInternalContext(NormalizeInternalError(err), "executing/decoding delegated instant matrix result for %q", expr.String())
			}
			if isBareSelectorExpr(expr) {
				series = dropNaNRangePoints(series)
			}
			return model.MatrixValue{Series: series}, nil
		default:
			return nil, NewUnsupportedErrorf("delegated instant result type %q for %q is not implemented yet", expr.Type(), expr.String())
		}
	case EvalModeRange:
		sql, queryParams := storage.BuildRangeQuerySQL(e.queryConfig(), promQL, params.Start.UnixMilli(), params.End.UnixMilli(), params.Step.Milliseconds())
		series, err := e.executeDelegatedRangeSeries(ctx, sql, queryParams)
		if err != nil {
			return nil, WithInternalContext(NormalizeInternalError(err), "executing/decoding delegated range matrix result for %q", expr.String())
		}
		if isBareSelectorExpr(expr) {
			series = dropNaNRangePoints(series)
		}
		return model.MatrixValue{Series: series}, nil
	default:
		return nil, NewExecutionErrorf("unknown evaluation mode %q", params.Mode)
	}
}

func (e *Evaluator) executeDelegatedInstantSamples(ctx context.Context, sql string, params map[string]string) ([]model.InstantSample, error) {
	if e.client.TransportKind() == storage.TransportNative {
		return e.client.QueryInstantSamples(ctx, storage.QueryRequest{SQL: sql, Params: params, Purpose: storage.QueryPurposeDelegatedInstant})
	}
	response, err := e.client.Execute(ctx, sql, params)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	return DecodeInstantSamples(response.Body)
}

func (e *Evaluator) executeDelegatedRangeSeries(ctx context.Context, sql string, params map[string]string) ([]model.RangeSeries, error) {
	if e.client.TransportKind() == storage.TransportNative {
		return e.client.QueryRangeSeries(ctx, storage.QueryRequest{SQL: sql, Params: params, Purpose: storage.QueryPurposeDelegatedRange})
	}
	response, err := e.client.Execute(ctx, sql, params)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	return DecodeRangeSeries(response.Body)
}

func buildPlan(expr parser.Expr) (Plan, error) {
	return buildPlanWithContext(expr, DefaultPlanContext(EvalModeInstant))
}

func buildPlanWithContext(expr parser.Expr, ctx PlanContext) (Plan, error) {
	plan, _, err := BuildPlanWithContextAndAnalysis(expr, ctx)
	return plan, err
}

func BuildEntireQueryDelegatedPlan(expr parser.Expr) (Plan, *nativeplan.Analysis, error) {
	logicalRoot, err := BuildLogicalPlan(expr)
	if err != nil {
		return nil, nil, err
	}
	logicalRoot, logicalAnalysis, trace, err := logicalopt.OptimizeWithTrace(logicalRoot, logicalopt.DefaultPassesForEnv())
	if err != nil {
		return nil, nil, err
	}
	_ = trace
	_ = logicalAnalysis
	analysis := nativeplan.Analyze(logicalRoot)
	return annotateQueryPlan(&delegatedExprPlan{Expr: expr}, analysis.Root), analysis, nil
}

func BuildPlanWithContextAndAnalysis(expr parser.Expr, ctx PlanContext) (Plan, *nativeplan.Analysis, error) {
	logicalRoot, err := BuildLogicalPlan(expr)
	if err != nil {
		return nil, nil, err
	}
	logicalRoot, logicalAnalysis, trace, err := logicalopt.OptimizeWithTrace(logicalRoot, logicalopt.DefaultPassesForEnv())
	if err != nil {
		return nil, nil, err
	}
	analysis := nativeplan.Analyze(logicalRoot)
	plan, err := buildExecPlanWithAnalysis(logicalRoot, ctx, analysis)
	if err != nil {
		return nil, nil, err
	}
	plan, err = applyRangeExecutionStrategy(plan, ctx)
	if err != nil {
		return nil, nil, WithInternalContext(err, "applying range execution strategy for %q", expr.String())
	}
	// Tier-2 whole-query router wiring: if the root plan is a
	// nativeSubtreePlan, thread the logical root + analysis in so
	// execute() can drive SQL via renderer.Lower. Subtree-pushdown
	// sites (tier 3a) leave these nil.
	attachLogicalRootForLower(plan, logicalRoot, logicalAnalysis, trace)
	return plan, analysis, nil
}

// attachLogicalRootForLower populates the LogicalRoot/LogicalAnalysis
// fields on a top-level nativeSubtreePlan so execute() can route
// through renderer.Lower. No-op if plan isn't a nativeSubtreePlan.
func attachLogicalRootForLower(plan Plan, logical logicalPlan, logicalAnalysis *logicalpkg.Analysis, trace *logicalopt.Trace) {
	switch typed := plan.(type) {
	case *nativeSubtreePlan:
		typed.LogicalRoot = logical
		typed.LogicalAnalysis = logicalAnalysis
		typed.LogicalOptimizationTrace = trace
	case *chunkedRangePlan:
		attachLogicalRootForLower(typed.Child, logical, logicalAnalysis, trace)
	}
}

func buildExecPlan(plan logicalPlan) (Plan, error) {
	optimized, _, err := logicalopt.Optimize(plan, logicalopt.DefaultPassesForEnv())
	if err != nil {
		return nil, err
	}
	analysis := nativeplan.Analyze(optimized)
	return buildExecPlanWithAnalysis(optimized, DefaultPlanContext(EvalModeInstant), analysis)
}

func buildExecPlanWithAnalysis(plan logicalPlan, ctx PlanContext, analysis *nativeplan.Analysis) (Plan, error) {
	ctx.nativePlanningDepth++
	switch node := plan.(type) {
	case *logicalLeafExprPlan:
		if ctx.AllowsNativePlanning() && ctx.NativeLoweringMode.ForcesNativeRoot() {
			if nativePlan, ok, err := maybeBuildNativeLeafPlan(node, ctx, analysis); err != nil {
				return nil, WithInternalContext(err, "building native subtree plan for leaf %q", node.ExprString())
			} else if ok {
				return annotateQueryPlan(nativePlan, analysis.InfoFor(node)), nil
			}
		}
		if err := ensureDelegatedExprSupportedForContext(node.Expr, ctx, "delegated leaf planning"); err != nil {
			return nil, err
		}
		return annotateQueryPlan(&delegatedExprPlan{Expr: node.Expr}, analysis.InfoFor(node)), nil
	case *logicalScalarLiteralPlan:
		if ctx.AllowsNativePlanning() {
			if nativePlan, ok, err := maybeBuildNativeScalarLiteralPlan(node, ctx, analysis); err != nil {
				return nil, WithInternalContext(err, "building native subtree plan for scalar literal %q", node.ExprString())
			} else if ok {
				return annotateQueryPlan(nativePlan, analysis.InfoFor(node)), nil
			}
		}
		return annotateQueryPlan(&scalarLiteralPlan{Expr: node.ExprString(), Value: node.Value}, analysis.InfoFor(node)), nil
	case *logicalUnaryPlan:
		if ctx.AllowsNativePlanning() {
			if nativePlan, ok, err := maybeBuildNativeUnaryPlan(node, ctx, analysis); err != nil {
				return nil, WithInternalContext(err, "building native subtree plan for unary expression %q", node.ExprString())
			} else if ok {
				return annotateQueryPlan(nativePlan, analysis.InfoFor(node)), nil
			}
		}
		child, err := buildExecPlanWithAnalysis(node.Child, ctx, analysis)
		if err != nil {
			return nil, WithInternalContext(err, "building execution child plan for unary expression %q", node.ExprString())
		}
		return annotateQueryPlan(&localUnaryPlan{Expr: node.ExprString(), Op: node.Op, Child: child}, analysis.InfoFor(node)), nil
	case *logicalBinaryPlan:
		if ctx.AllowsNativePlanning() {
			if nativePlan, ok, err := maybeBuildNativeBinaryPlan(node, ctx, analysis); err != nil {
				return nil, WithInternalContext(err, "building native subtree plan for binary expression %q", node.ExprString())
			} else if ok {
				return annotateQueryPlan(nativePlan, analysis.InfoFor(node)), nil
			}
		}
		lhs, err := buildExecPlanWithAnalysis(node.LHS, ctx, analysis)
		if err != nil {
			return nil, WithInternalContext(err, "building execution left operand plan for binary expression %q", node.ExprString())
		}
		rhs, err := buildExecPlanWithAnalysis(node.RHS, ctx, analysis)
		if err != nil {
			return nil, WithInternalContext(err, "building execution right operand plan for binary expression %q", node.ExprString())
		}
		lhsExpr := logicalExprString(node.LHS)
		rhsExpr := logicalExprString(node.RHS)
		if lhsExpr != "" && lhsExpr == rhsExpr && shouldMemoizeRepeatedLocalPlan(lhs) && shouldMemoizeRepeatedLocalPlan(rhs) {
			lhs = memoizeLocalPlan(lhsExpr, lhs)
			rhs = memoizeLocalPlan(lhsExpr, rhs)
		}
		return annotateQueryPlan(&localBinaryPlan{Expr: node.ExprString(), Op: node.Op, VectorMatching: cloneVectorMatching(node.VectorMatching), ReturnBool: node.ReturnBool, LHS: lhs, RHS: rhs}, analysis.InfoFor(node)), nil
	case *logicalAggregationPlan:
		if ctx.AllowsNativePlanning() {
			if pushdownPlan, ok, err := maybeBuildNativeAggregationPlan(node, ctx, analysis); err != nil {
				return nil, WithInternalContext(err, "building native subtree plan for aggregate %q", node.ExprString())
			} else if ok {
				return annotateQueryPlan(pushdownPlan, analysis.InfoFor(node)), nil
			}
		}
		child, err := buildExecPlanWithAnalysis(node.Child, clearNativeSubtreeRenderTagHint(ctx), analysis)
		if err != nil {
			return nil, WithInternalContext(err, "building execution child plan for aggregate %q", node.ExprString())
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
		if ctx.AllowsNativePlanning() {
			if nativePlan, ok, err := maybeBuildNativeHistogramQuantilePlan(node, ctx, analysis); err != nil {
				return nil, WithInternalContext(err, "building native subtree plan for histogram_quantile %q", node.ExprString())
			} else if ok {
				return annotateQueryPlan(nativePlan, analysis.InfoFor(node)), nil
			}
		}
		child, err := buildExecPlanWithAnalysis(node.Child, histogramChildPlanContext(ctx, node.Child), analysis)
		if err != nil {
			return nil, WithInternalContext(err, "building execution child plan for histogram_quantile %q", node.ExprString())
		}
		return annotateQueryPlan(&localHistogramQuantilePlan{Expr: node.ExprString(), Quantile: node.Quantile, Child: child}, analysis.InfoFor(node)), nil
	case *logicalHistogramFractionPlan:
		if ctx.AllowsNativePlanning() {
			if nativePlan, ok, err := maybeBuildNativeHistogramFractionPlan(node, ctx, analysis); err != nil {
				return nil, WithInternalContext(err, "building native subtree plan for histogram_fraction %q", node.ExprString())
			} else if ok {
				return annotateQueryPlan(nativePlan, analysis.InfoFor(node)), nil
			}
		}
		child, err := buildExecPlanWithAnalysis(node.Child, histogramChildPlanContext(ctx, node.Child), analysis)
		if err != nil {
			return nil, WithInternalContext(err, "building execution child plan for histogram_fraction %q", node.ExprString())
		}
		return annotateQueryPlan(&localHistogramFractionPlan{Expr: node.ExprString(), Lower: node.Lower, Upper: node.Upper, Child: child}, analysis.InfoFor(node)), nil
	case *logicalHistogramProjectionPlan:
		if ctx.AllowsNativePlanning() {
			if nativePlan, ok, err := maybeBuildNativeHistogramProjectionPlan(node, ctx, analysis); err != nil {
				return nil, WithInternalContext(err, "building native subtree plan for %s %q", node.Func, node.ExprString())
			} else if ok {
				return annotateQueryPlan(nativePlan, analysis.InfoFor(node)), nil
			}
		}
		child, err := buildExecPlanWithAnalysis(node.Child, histogramChildPlanContext(ctx, node.Child), analysis)
		if err != nil {
			return nil, WithInternalContext(err, "building execution child plan for %s %q", node.Func, node.ExprString())
		}
		return annotateQueryPlan(&localHistogramProjectionPlan{Expr: node.ExprString(), Func: node.Func, Child: child}, analysis.InfoFor(node)), nil
	case *logicalHistogramQuantilesPlan:
		if ctx.AllowsNativePlanning() {
			if nativePlan, ok, err := maybeBuildNativeHistogramQuantilesPlan(node, ctx, analysis); err != nil {
				return nil, WithInternalContext(err, "building native subtree plan for histogram_quantiles %q", node.ExprString())
			} else if ok {
				return annotateQueryPlan(nativePlan, analysis.InfoFor(node)), nil
			}
		}
		child, err := buildExecPlanWithAnalysis(node.Child, histogramChildPlanContext(ctx, node.Child), analysis)
		if err != nil {
			return nil, WithInternalContext(err, "building execution child plan for histogram_quantiles %q", node.ExprString())
		}
		paramChildren := make([]Plan, 0, len(node.ParamChildren))
		for _, paramChild := range node.ParamChildren {
			if paramChild == nil {
				paramChildren = append(paramChildren, nil)
				continue
			}
			builtParam, err := buildExecPlanWithAnalysis(paramChild, ctx, analysis)
			if err != nil {
				return nil, WithInternalContext(err, "building execution scalar parameter plan for histogram_quantiles %q", node.ExprString())
			}
			paramChildren = append(paramChildren, builtParam)
		}
		return annotateQueryPlan(&localHistogramQuantilesPlan{Expr: node.ExprString(), Label: node.Label, ParamNumbers: append([]*float64(nil), node.ParamNumbers...), ParamChildren: paramChildren, Child: child}, analysis.InfoFor(node)), nil
	case *logicalRangeFunctionPlan:
		if ctx.AllowsNativePlanning() {
			if nativePlan, ok, err := maybeBuildNativeRangeFunctionPlan(node, ctx, analysis); err != nil {
				return nil, WithInternalContext(err, "building native subtree plan for %s %q", node.Func, node.ExprString())
			} else if ok {
				return annotateQueryPlan(nativePlan, analysis.InfoFor(node)), nil
			}
		}
		child, err := buildExecPlanWithAnalysis(node.Child, ctx, analysis)
		if err != nil {
			return nil, WithInternalContext(err, "building execution child plan for %s %q", node.Func, node.ExprString())
		}
		return annotateQueryPlan(&localRangeFunctionPlan{Expr: node.ExprString(), Func: node.Func, ParamNumber: cloneFloat64Pointer(node.ParamNumber), ParamNumbers: cloneFloat64Pointers(node.ParamNumbers), Child: child}, analysis.InfoFor(node)), nil
	case *logicalVectorPlan:
		if ctx.AllowsNativePlanning() {
			if nativePlan, ok, err := maybeBuildNativeVectorPlan(node, ctx, analysis); err != nil {
				return nil, WithInternalContext(err, "building native subtree plan for vector %q", node.ExprString())
			} else if ok {
				return annotateQueryPlan(nativePlan, analysis.InfoFor(node)), nil
			}
		}
		child, err := buildExecPlanWithAnalysis(node.Child, ctx, analysis)
		if err != nil {
			return nil, WithInternalContext(err, "building execution child plan for vector %q", node.ExprString())
		}
		return annotateQueryPlan(&localVectorPlan{Expr: node.ExprString(), Child: child}, analysis.InfoFor(node)), nil
	case *logicalRoundPlan:
		if ctx.AllowsNativePlanning() {
			if nativePlan, ok, err := maybeBuildNativeRoundPlan(node, ctx, analysis); err != nil {
				return nil, WithInternalContext(err, "building native subtree plan for round %q", node.ExprString())
			} else if ok {
				return annotateQueryPlan(nativePlan, analysis.InfoFor(node)), nil
			}
		}
		child, err := buildExecPlanWithAnalysis(node.Child, ctx, analysis)
		if err != nil {
			return nil, WithInternalContext(err, "building execution child plan for round %q", node.ExprString())
		}
		return annotateQueryPlan(&localRoundPlan{Expr: node.ExprString(), Decimals: node.Decimals, Child: child}, analysis.InfoFor(node)), nil
	case *logicalSortPlan:
		if ctx.AllowsNativePlanning() {
			if nativePlan, ok, err := maybeBuildNativeSortPlan(node, ctx, analysis); err != nil {
				return nil, WithInternalContext(err, "building native subtree plan for %s %q", node.Func, node.ExprString())
			} else if ok {
				return annotateQueryPlan(nativePlan, analysis.InfoFor(node)), nil
			}
		}
		child, err := buildExecPlanWithAnalysis(node.Child, ctx, analysis)
		if err != nil {
			return nil, WithInternalContext(err, "building execution child plan for %s %q", node.Func, node.ExprString())
		}
		return annotateQueryPlan(&localSortPlan{Expr: node.ExprString(), Func: node.Func, Labels: append([]string(nil), node.Labels...), Child: child}, analysis.InfoFor(node)), nil
	case *logicalInfoPlan:
		if ctx.AllowsNativePlanning() {
			if nativePlan, ok, err := maybeBuildNativeInfoPlan(node, ctx, analysis); err != nil {
				return nil, WithInternalContext(err, "building native subtree plan for info %q", node.ExprString())
			} else if ok {
				return annotateQueryPlan(nativePlan, analysis.InfoFor(node)), nil
			}
		}
		child, err := buildExecPlanWithAnalysis(node.Child, ctx, analysis)
		if err != nil {
			return nil, WithInternalContext(err, "building execution child plan for info %q", node.ExprString())
		}
		return annotateQueryPlan(&localInfoPlan{Expr: node.ExprString(), SelectorMatchers: clonePromMatchers(node.SelectorMatchers), Child: child}, analysis.InfoFor(node)), nil
	case *logicalScalarConvertPlan:
		if ctx.AllowsNativePlanning() {
			if nativePlan, ok, err := maybeBuildNativeScalarConvertPlan(node, ctx, analysis); err != nil {
				return nil, WithInternalContext(err, "building native subtree plan for scalar %q", node.ExprString())
			} else if ok {
				return annotateQueryPlan(nativePlan, analysis.InfoFor(node)), nil
			}
		}
		child, err := buildExecPlanWithAnalysis(node.Child, ctx, analysis)
		if err != nil {
			return nil, WithInternalContext(err, "building execution child plan for scalar %q", node.ExprString())
		}
		return annotateQueryPlan(&localScalarConvertPlan{Expr: node.ExprString(), Child: child}, analysis.InfoFor(node)), nil
	case *logicalPointwiseFunctionPlan:
		if ctx.AllowsNativePlanning() {
			if nativePlan, ok, err := maybeBuildNativeSourcePlan(node, ctx, analysis); err != nil {
				return nil, WithInternalContext(err, "building native subtree plan for %s %q", node.Func, node.ExprString())
			} else if ok {
				return annotateQueryPlan(nativePlan, analysis.InfoFor(node)), nil
			}
		}
		var (
			child Plan
			err   error
		)
		if node.Child != nil {
			child, err = buildExecPlanWithAnalysis(node.Child, ctx, analysis)
			if err != nil {
				return nil, WithInternalContext(err, "building execution child plan for %s %q", node.Func, node.ExprString())
			}
		}
		paramChildren := make([]Plan, 0, len(node.ParamChildren))
		for _, paramChild := range node.ParamChildren {
			if paramChild == nil {
				paramChildren = append(paramChildren, nil)
				continue
			}
			builtParam, err := buildExecPlanWithAnalysis(paramChild, ctx, analysis)
			if err != nil {
				return nil, WithInternalContext(err, "building execution scalar parameter plan for %s %q", node.Func, node.ExprString())
			}
			paramChildren = append(paramChildren, builtParam)
		}
		return annotateQueryPlan(&localPointwiseFunctionPlan{Expr: node.ExprString(), Func: node.Func, ParamNumbers: append([]*float64(nil), node.ParamNumbers...), ParamChildren: paramChildren, Child: child}, analysis.InfoFor(node)), nil
	case *logicalScalarBuiltinPlan:
		if ctx.AllowsNativePlanning() {
			if nativePlan, ok, err := maybeBuildNativeScalarBuiltinPlan(node, ctx, analysis); err != nil {
				return nil, WithInternalContext(err, "building native subtree plan for %s %q", node.Func, node.ExprString())
			} else if ok {
				return annotateQueryPlan(nativePlan, analysis.InfoFor(node)), nil
			}
		}
		return annotateQueryPlan(&scalarBuiltinPlan{Expr: node.ExprString(), Func: node.Func}, analysis.InfoFor(node)), nil
	case *logicalRatePlan:
		if ctx.AllowsNativePlanning() {
			if nativePlan, ok, err := maybeBuildNativeRatePlan(node, ctx, analysis); err != nil {
				return nil, WithInternalContext(err, "building native subtree plan for %s %q", node.Func, node.ExprString())
			} else if ok {
				return annotateQueryPlan(nativePlan, analysis.InfoFor(node)), nil
			}
		}
		child, err := buildExecPlanWithAnalysis(node.Child, ctx, analysis)
		if err != nil {
			return nil, WithInternalContext(err, "building execution child plan for %s %q", node.Func, node.ExprString())
		}
		return annotateQueryPlan(&localRatePlan{Expr: node.ExprString(), Func: node.Func, Child: child}, analysis.InfoFor(node)), nil
	case *logicalIncreasePlan:
		if ctx.AllowsNativePlanning() {
			if nativePlan, ok, err := maybeBuildNativeIncreasePlan(node, ctx, analysis); err != nil {
				return nil, WithInternalContext(err, "building native subtree plan for increase %q", node.ExprString())
			} else if ok {
				return annotateQueryPlan(nativePlan, analysis.InfoFor(node)), nil
			}
		}
		child, err := buildExecPlanWithAnalysis(node.Child, ctx, analysis)
		if err != nil {
			return nil, WithInternalContext(err, "building execution child plan for increase %q", node.ExprString())
		}
		return annotateQueryPlan(&localIncreasePlan{Expr: node.ExprString(), Child: child}, analysis.InfoFor(node)), nil
	case *logicalDeltaPlan:
		if ctx.AllowsNativePlanning() {
			if nativePlan, ok, err := maybeBuildNativeDeltaPlan(node, ctx, analysis); err != nil {
				return nil, WithInternalContext(err, "building native subtree plan for %s %q", node.Func, node.ExprString())
			} else if ok {
				return annotateQueryPlan(nativePlan, analysis.InfoFor(node)), nil
			}
		}
		child, err := buildExecPlanWithAnalysis(node.Child, ctx, analysis)
		if err != nil {
			return nil, WithInternalContext(err, "building execution child plan for %s %q", node.Func, node.ExprString())
		}
		return annotateQueryPlan(&localDeltaPlan{Expr: node.ExprString(), Func: node.Func, Child: child}, analysis.InfoFor(node)), nil
	case *logicalChangesPlan:
		if ctx.AllowsNativePlanning() {
			if nativePlan, ok, err := maybeBuildNativeChangesPlan(node, ctx, analysis); err != nil {
				return nil, WithInternalContext(err, "building native subtree plan for changes %q", node.ExprString())
			} else if ok {
				return annotateQueryPlan(nativePlan, analysis.InfoFor(node)), nil
			}
		}
		child, err := buildExecPlanWithAnalysis(node.Child, ctx, analysis)
		if err != nil {
			return nil, WithInternalContext(err, "building execution child plan for changes %q", node.ExprString())
		}
		return annotateQueryPlan(&localChangesPlan{Expr: node.ExprString(), Child: child}, analysis.InfoFor(node)), nil
	case *logicalDerivPlan:
		if ctx.AllowsNativePlanning() {
			if nativePlan, ok, err := maybeBuildNativeDerivPlan(node, ctx, analysis); err != nil {
				return nil, WithInternalContext(err, "building native subtree plan for deriv %q", node.ExprString())
			} else if ok {
				return annotateQueryPlan(nativePlan, analysis.InfoFor(node)), nil
			}
		}
		child, err := buildExecPlanWithAnalysis(node.Child, ctx, analysis)
		if err != nil {
			return nil, WithInternalContext(err, "building execution child plan for deriv %q", node.ExprString())
		}
		return annotateQueryPlan(&localDerivPlan{Expr: node.ExprString(), Child: child}, analysis.InfoFor(node)), nil
	case *logicalQuantileOverTimePlan:
		if ctx.AllowsNativePlanning() {
			if nativePlan, ok, err := maybeBuildNativeQuantileOverTimePlan(node, ctx, analysis); err != nil {
				return nil, WithInternalContext(err, "building native subtree plan for quantile_over_time %q", node.ExprString())
			} else if ok {
				return annotateQueryPlan(nativePlan, analysis.InfoFor(node)), nil
			}
		}
		child, err := buildExecPlanWithAnalysis(node.Child, ctx, analysis)
		if err != nil {
			return nil, WithInternalContext(err, "building execution child plan for quantile_over_time %q", node.ExprString())
		}
		return annotateQueryPlan(&localQuantileOverTimePlan{Expr: node.ExprString(), Quantile: node.Quantile, Child: child}, analysis.InfoFor(node)), nil
	case *logicalAbsentPlan:
		if ctx.AllowsNativePlanning() {
			if nativePlan, ok, err := maybeBuildNativeAbsentPlan(node, ctx, analysis); err != nil {
				return nil, WithInternalContext(err, "building native subtree plan for absent %q", node.ExprString())
			} else if ok {
				return annotateQueryPlan(nativePlan, analysis.InfoFor(node)), nil
			}
		}
		child, err := buildExecPlanWithAnalysis(node.Child, ctx, analysis)
		if err != nil {
			return nil, WithInternalContext(err, "building execution child plan for absent %q", node.ExprString())
		}
		return annotateQueryPlan(&localAbsentPlan{Expr: node.ExprString(), OutputMetric: model.CloneMetric(node.OutputMetric), Child: child}, analysis.InfoFor(node)), nil
	case *logicalAbsentOverTimePlan:
		if ctx.AllowsNativePlanning() {
			if nativePlan, ok, err := maybeBuildNativeAbsentOverTimePlan(node, ctx, analysis); err != nil {
				return nil, WithInternalContext(err, "building native subtree plan for absent_over_time %q", node.ExprString())
			} else if ok {
				return annotateQueryPlan(nativePlan, analysis.InfoFor(node)), nil
			}
		}
		child, err := buildExecPlanWithAnalysis(node.Child, ctx, analysis)
		if err != nil {
			return nil, WithInternalContext(err, "building execution child plan for absent_over_time %q", node.ExprString())
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
		if ctx.AllowsNativePlanning() {
			if nativePlan, ok, err := maybeBuildNativeSubqueryPlan(node, ctx, analysis); err != nil {
				return nil, WithInternalContext(err, "building native subtree plan for subquery %q", node.ExprString())
			} else if ok {
				return annotateQueryPlan(nativePlan, analysis.InfoFor(node)), nil
			}
		}
		child, err := buildExecPlanWithAnalysis(node.Child, ctx, analysis)
		if err != nil {
			return nil, WithInternalContext(err, "building execution child plan for subquery %q", node.ExprString())
		}
		return annotateQueryPlan(&localSubqueryPlan{Expr: node.Expr, Range: node.Range, Step: node.Step, Offset: node.Offset, Timestamp: cloneInt64Pointer(node.Timestamp), StartOrEnd: node.StartOrEnd, DelegatedLeafCompatible: node.DelegatedLeafCompatible, MaxPointsPerSeries: ctx.MaxRangePointsPerSeries, Child: child}, analysis.InfoFor(node)), nil
	case *logicalLabelReplacePlan:
		if ctx.AllowsNativePlanning() {
			if nativePlan, ok, err := maybeBuildNativeLabelReplacePlan(node, ctx, analysis); err != nil {
				return nil, WithInternalContext(err, "building native subtree plan for label_replace %q", node.ExprString())
			} else if ok {
				return annotateQueryPlan(nativePlan, analysis.InfoFor(node)), nil
			}
		}
		child, err := buildExecPlanWithAnalysis(node.Child, ctx, analysis)
		if err != nil {
			return nil, WithInternalContext(err, "building execution child plan for label_replace %q", node.ExprString())
		}
		return annotateQueryPlan(&localLabelReplacePlan{Expr: node.ExprString(), Config: node.Config, Child: child}, analysis.InfoFor(node)), nil
	case *logicalLabelJoinPlan:
		if ctx.AllowsNativePlanning() {
			if nativePlan, ok, err := maybeBuildNativeLabelJoinPlan(node, ctx, analysis); err != nil {
				return nil, WithInternalContext(err, "building native subtree plan for label_join %q", node.ExprString())
			} else if ok {
				return annotateQueryPlan(nativePlan, analysis.InfoFor(node)), nil
			}
		}
		child, err := buildExecPlanWithAnalysis(node.Child, ctx, analysis)
		if err != nil {
			return nil, WithInternalContext(err, "building execution child plan for label_join %q", node.ExprString())
		}
		return annotateQueryPlan(&localLabelJoinPlan{Expr: node.ExprString(), Config: node.Config, Child: child}, analysis.InfoFor(node)), nil
	default:
		return nil, NewExecutionErrorf("execution planner cannot lower logical node %T", plan)
	}
}

func toExecEvalMode(mode EvalMode) exec.EvalMode {
	if mode == EvalModeRange {
		return exec.EvalModeRange
	}
	return exec.EvalModeInstant
}
