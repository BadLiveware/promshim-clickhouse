package promshim

import (
	"context"
	"time"

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

type runtimeValue interface {
	runtimeValue()
}

type scalarValue struct {
	Timestamp float64
	Value     float64
}

func (scalarValue) runtimeValue() {}

type vectorValue struct {
	Samples []instantSample
}

func (vectorValue) runtimeValue() {}

type matrixValue struct {
	Series []rangeSeries
}

func (matrixValue) runtimeValue() {}

type queryPlan interface {
	execute(ctx context.Context, evaluator *evaluator, params evalParams) (runtimeValue, error)
	explain() ExplainNode
}

type delegatedExprPlan struct {
	Expr parser.Expr
}

func (p *delegatedExprPlan) execute(ctx context.Context, evaluator *evaluator, params evalParams) (runtimeValue, error) {
	value, err := evaluator.executeDelegated(ctx, p.Expr, params)
	if err != nil {
		return nil, withInternalContext(err, "executing delegated expression %q in %s mode", p.Expr.String(), params.Mode)
	}
	return value, nil
}

func (p *delegatedExprPlan) explain() ExplainNode {
	return ExplainNode{Kind: "leaf", Strategy: "delegated_promql", Expr: p.Expr.String()}
}

type nativeAggregationPlan struct {
	Expr         string
	ChildExpr    parser.Expr
	Op           parser.ItemType
	Grouping     []string
	Without      bool
	Reason       string
	Estimate     *planEstimate
	ChildExplain ExplainNode
}

func (p *nativeAggregationPlan) execute(ctx context.Context, evaluator *evaluator, params evalParams) (runtimeValue, error) {
	switch params.Mode {
	case evalModeInstant:
		sql, queryParams, err := buildInstantAggregationQuerySQL(evaluator.opts, p.ChildExpr.String(), params.EvaluationTime.UnixMilli(), p.Op, p.Grouping, p.Without)
		if err != nil {
			return nil, withInternalContext(err, "building native aggregation instant SQL for %q", p.Expr)
		}
		response, err := evaluator.client.Execute(ctx, sql, queryParams)
		if err != nil {
			return nil, withInternalContext(normalizeInternalError(err), "executing native aggregation instant query for %q", p.Expr)
		}
		defer response.Body.Close()
		samples, err := decodeInstantSamples(response.Body)
		if err != nil {
			return nil, withInternalContext(err, "decoding native aggregation instant result for %q", p.Expr)
		}
		return vectorValue{Samples: samples}, nil
	case evalModeRange:
		sql, queryParams, err := buildRangeAggregationQuerySQL(evaluator.opts, p.ChildExpr.String(), params.Start.UnixMilli(), params.End.UnixMilli(), params.Step.Milliseconds(), p.Op, p.Grouping, p.Without)
		if err != nil {
			return nil, withInternalContext(err, "building native aggregation range SQL for %q", p.Expr)
		}
		response, err := evaluator.client.Execute(ctx, sql, queryParams)
		if err != nil {
			return nil, withInternalContext(normalizeInternalError(err), "executing native aggregation range query for %q", p.Expr)
		}
		defer response.Body.Close()
		series, err := decodeRangeSeries(response.Body)
		if err != nil {
			return nil, withInternalContext(err, "decoding native aggregation range result for %q", p.Expr)
		}
		return matrixValue{Series: series}, nil
	default:
		return nil, newExecutionErrorf("unknown evaluation mode %q", params.Mode)
	}
}

func (p *nativeAggregationPlan) explain() ExplainNode {
	children := []ExplainNode{}
	if p.ChildExplain.Strategy != "" {
		children = append(children, p.ChildExplain)
	}
	return ExplainNode{
		Kind:     "aggregation",
		Strategy: "native_sql",
		Expr:     p.Expr,
		Reason:   p.Reason,
		Estimate: p.Estimate,
		Children: children,
	}
}

type scalarLiteralPlan struct {
	Expr  string
	Value float64
}

func (p *scalarLiteralPlan) execute(_ context.Context, _ *evaluator, params evalParams) (runtimeValue, error) {
	timestamp := float64(params.EvaluationTime.UnixNano()) / float64(time.Second)
	return scalarValue{Timestamp: timestamp, Value: p.Value}, nil
}

func (p *scalarLiteralPlan) explain() ExplainNode {
	return ExplainNode{Kind: "scalar", Strategy: "local", Expr: p.Expr}
}

type localUnaryPlan struct {
	Expr  string
	Op    parser.ItemType
	Child queryPlan
}

func (p *localUnaryPlan) execute(ctx context.Context, evaluator *evaluator, params evalParams) (runtimeValue, error) {
	childValue, err := p.Child.execute(ctx, evaluator, params)
	if err != nil {
		return nil, withInternalContext(err, "evaluating unary expression op=%s", p.Op.String())
	}
	value, err := applyUnaryRuntimeValue(p.Op, childValue, params)
	if err != nil {
		return nil, withInternalContext(err, "applying unary expression op=%s", p.Op.String())
	}
	return value, nil
}

func (p *localUnaryPlan) explain() ExplainNode {
	return ExplainNode{Kind: "unary", Strategy: "local", Expr: p.Expr, Children: []ExplainNode{p.Child.explain()}}
}

type localBinaryPlan struct {
	Expr       string
	Op         parser.ItemType
	ReturnBool bool
	LHS        queryPlan
	RHS        queryPlan
}

func (p *localBinaryPlan) execute(ctx context.Context, evaluator *evaluator, params evalParams) (runtimeValue, error) {
	lhsValue, err := p.LHS.execute(ctx, evaluator, params)
	if err != nil {
		return nil, withInternalContext(err, "evaluating left operand for binary op=%s", p.Op.String())
	}
	rhsValue, err := p.RHS.execute(ctx, evaluator, params)
	if err != nil {
		return nil, withInternalContext(err, "evaluating right operand for binary op=%s", p.Op.String())
	}
	value, err := applyBinaryRuntimeValue(p.Op, lhsValue, rhsValue, p.ReturnBool, params)
	if err != nil {
		return nil, withInternalContext(err, "applying binary expression op=%s returnBool=%t", p.Op.String(), p.ReturnBool)
	}
	return value, nil
}

func (p *localBinaryPlan) explain() ExplainNode {
	return ExplainNode{Kind: "binary", Strategy: "local", Expr: p.Expr, Children: []ExplainNode{p.LHS.explain(), p.RHS.explain()}}
}

type localAggregationPlan struct {
	Expr     string
	Op       parser.ItemType
	Grouping []string
	Without  bool
	Child    queryPlan
}

func (p *localAggregationPlan) execute(ctx context.Context, evaluator *evaluator, params evalParams) (runtimeValue, error) {
	childValue, err := p.Child.execute(ctx, evaluator, params)
	if err != nil {
		return nil, withInternalContext(err, "evaluating local aggregation op=%s grouping=%v without=%t", p.Op.String(), p.Grouping, p.Without)
	}
	value, err := aggregateRuntimeValue(p.Op, childValue, p.Grouping, p.Without, params.EvaluationTime)
	if err != nil {
		return nil, withInternalContext(err, "aggregating local result op=%s grouping=%v without=%t", p.Op.String(), p.Grouping, p.Without)
	}
	return value, nil
}

func (p *localAggregationPlan) explain() ExplainNode {
	return ExplainNode{Kind: "aggregation", Strategy: "local", Expr: p.Expr, Children: []ExplainNode{p.Child.explain()}}
}

type localLabelReplacePlan struct {
	Expr   string
	Config localLabelReplaceConfig
	Child  queryPlan
}

func (p *localLabelReplacePlan) execute(ctx context.Context, evaluator *evaluator, params evalParams) (runtimeValue, error) {
	childValue, err := p.Child.execute(ctx, evaluator, params)
	if err != nil {
		return nil, withInternalContext(err, "evaluating label_replace child dst=%q src=%q", p.Config.Dst, p.Config.Src)
	}
	value, err := applyLabelReplaceRuntimeValue(childValue, p.Config)
	if err != nil {
		return nil, withInternalContext(err, "applying label_replace dst=%q src=%q", p.Config.Dst, p.Config.Src)
	}
	return value, nil
}

func (p *localLabelReplacePlan) explain() ExplainNode {
	return ExplainNode{Kind: "label_replace", Strategy: "local", Expr: p.Expr, Children: []ExplainNode{p.Child.explain()}}
}

type localLabelJoinPlan struct {
	Expr   string
	Config localLabelJoinConfig
	Child  queryPlan
}

func (p *localLabelJoinPlan) execute(ctx context.Context, evaluator *evaluator, params evalParams) (runtimeValue, error) {
	childValue, err := p.Child.execute(ctx, evaluator, params)
	if err != nil {
		return nil, withInternalContext(err, "evaluating label_join child dst=%q src=%v", p.Config.Dst, p.Config.SrcLabels)
	}
	value, err := applyLabelJoinRuntimeValue(childValue, p.Config)
	if err != nil {
		return nil, withInternalContext(err, "applying label_join dst=%q src=%v", p.Config.Dst, p.Config.SrcLabels)
	}
	return value, nil
}

func (p *localLabelJoinPlan) explain() ExplainNode {
	return ExplainNode{Kind: "label_join", Strategy: "local", Expr: p.Expr, Children: []ExplainNode{p.Child.explain()}}
}

type evaluator struct {
	opts   Options
	client *ClickHouseClient
}

func newEvaluator(opts Options, client *ClickHouseClient) *evaluator {
	return &evaluator{opts: opts, client: client}
}

func (e *evaluator) evaluate(ctx context.Context, plan queryPlan, params evalParams) (runtimeValue, error) {
	value, err := plan.execute(ctx, e, params)
	if err != nil {
		return nil, withInternalContext(err, "evaluating query plan in %s mode", params.Mode)
	}
	return value, nil
}

func (e *evaluator) executeDelegated(ctx context.Context, expr parser.Expr, params evalParams) (runtimeValue, error) {
	switch params.Mode {
	case evalModeInstant:
		sql, queryParams := buildInstantQuerySQL(e.opts, expr.String(), params.EvaluationTime.UnixMilli())
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
			return vectorValue{Samples: samples}, nil
		case parser.ValueTypeMatrix:
			series, err := decodeRangeSeries(response.Body)
			if err != nil {
				return nil, withInternalContext(err, "decoding delegated instant matrix result for %q", expr.String())
			}
			return matrixValue{Series: series}, nil
		default:
			return nil, newUnsupportedErrorf("delegated instant result type %q for %q is not implemented yet", expr.Type(), expr.String())
		}
	case evalModeRange:
		sql, queryParams := buildRangeQuerySQL(e.opts, expr.String(), params.Start.UnixMilli(), params.End.UnixMilli(), params.Step.Milliseconds())
		response, err := e.client.Execute(ctx, sql, queryParams)
		if err != nil {
			return nil, withInternalContext(normalizeInternalError(err), "executing delegated ClickHouse range query for %q", expr.String())
		}
		defer response.Body.Close()

		series, err := decodeRangeSeries(response.Body)
		if err != nil {
			return nil, withInternalContext(err, "decoding delegated range matrix result for %q", expr.String())
		}
		return matrixValue{Series: series}, nil
	default:
		return nil, newExecutionErrorf("unknown evaluation mode %q", params.Mode)
	}
}

func buildPlan(expr parser.Expr) (queryPlan, error) {
	return buildPlanWithContext(expr, defaultPlanContext(evalModeInstant))
}

func buildPlanWithContext(expr parser.Expr, ctx planContext) (queryPlan, error) {
	logical, err := buildLogicalPlan(expr)
	if err != nil {
		return nil, err
	}
	plan, err := buildExecPlanWithContext(logical, ctx)
	if err != nil {
		return nil, err
	}
	plan, err = applyRangeExecutionStrategy(plan, ctx)
	if err != nil {
		return nil, withInternalContext(err, "applying range execution strategy for %q", expr.String())
	}
	return plan, nil
}

func buildExecPlan(plan logicalPlan) (queryPlan, error) {
	return buildExecPlanWithContext(plan, defaultPlanContext(evalModeInstant))
}

func buildExecPlanWithContext(plan logicalPlan, ctx planContext) (queryPlan, error) {
	switch node := plan.(type) {
	case *logicalLeafExprPlan:
		return &delegatedExprPlan{Expr: node.Expr}, nil
	case *logicalScalarLiteralPlan:
		return &scalarLiteralPlan{Expr: node.exprString(), Value: node.Value}, nil
	case *logicalUnaryPlan:
		child, err := buildExecPlanWithContext(node.Child, ctx)
		if err != nil {
			return nil, withInternalContext(err, "building execution child plan for unary expression %q", node.exprString())
		}
		return &localUnaryPlan{Expr: node.exprString(), Op: node.Op, Child: child}, nil
	case *logicalBinaryPlan:
		lhs, err := buildExecPlanWithContext(node.LHS, ctx)
		if err != nil {
			return nil, withInternalContext(err, "building execution left operand plan for binary expression %q", node.exprString())
		}
		rhs, err := buildExecPlanWithContext(node.RHS, ctx)
		if err != nil {
			return nil, withInternalContext(err, "building execution right operand plan for binary expression %q", node.exprString())
		}
		return &localBinaryPlan{Expr: node.exprString(), Op: node.Op, ReturnBool: node.ReturnBool, LHS: lhs, RHS: rhs}, nil
	case *logicalAggregationPlan:
		if pushdownPlan, ok := maybeBuildNativeAggregationPlan(node, ctx); ok {
			return pushdownPlan, nil
		}
		child, err := buildExecPlanWithContext(node.Child, ctx)
		if err != nil {
			return nil, withInternalContext(err, "building execution child plan for aggregate %q", node.exprString())
		}
		return &localAggregationPlan{
			Expr:     node.exprString(),
			Op:       node.Op,
			Grouping: append([]string(nil), node.Grouping...),
			Without:  node.Without,
			Child:    child,
		}, nil
	case *logicalLabelReplacePlan:
		child, err := buildExecPlanWithContext(node.Child, ctx)
		if err != nil {
			return nil, withInternalContext(err, "building execution child plan for label_replace %q", node.exprString())
		}
		return &localLabelReplacePlan{Expr: node.exprString(), Config: node.Config, Child: child}, nil
	case *logicalLabelJoinPlan:
		child, err := buildExecPlanWithContext(node.Child, ctx)
		if err != nil {
			return nil, withInternalContext(err, "building execution child plan for label_join %q", node.exprString())
		}
		return &localLabelJoinPlan{Expr: node.exprString(), Config: node.Config, Child: child}, nil
	default:
		return nil, newExecutionErrorf("execution planner cannot lower logical node %T", plan)
	}
}

func maybeBuildNativeAggregationPlan(node *logicalAggregationPlan, ctx planContext) (queryPlan, bool) {
	if !ctx.PreferNativeAggregationPushdown {
		return nil, false
	}
	child, ok := node.Child.(*logicalLeafExprPlan)
	if !ok {
		return nil, false
	}
	reason := "pushing aggregation into native ClickHouse SQL over a delegatable leaf to avoid materializing the full child result in Go"
	return &nativeAggregationPlan{
		Expr:         node.exprString(),
		ChildExpr:    child.Expr,
		Op:           node.Op,
		Grouping:     append([]string(nil), node.Grouping...),
		Without:      node.Without,
		Reason:       reason,
		Estimate:     estimateRangePlan(ctx),
		ChildExplain: ExplainNode{Kind: "leaf", Strategy: "delegated_promql", Expr: child.Expr.String(), Reason: "delegatable child expression embedded as the source for native SQL aggregation"},
	}, true
}
