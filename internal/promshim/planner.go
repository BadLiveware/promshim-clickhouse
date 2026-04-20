package promshim

import (
	"context"
	"time"

	"github.com/BadLiveware/promshim-ch/internal/promshim/exec"
	"github.com/BadLiveware/promshim-ch/internal/promshim/model"
	"github.com/BadLiveware/promshim-ch/internal/promshim/storage"
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

type nativeAggregationSource struct {
	PromQLLeaf  parser.Expr
	ValueExpr   string
	TagsExpr    string
	Explain     ExplainNode
	DropsMetric bool
}

type nativeAggregationPlan struct {
	Expr         string
	Source       nativeAggregationSource
	Op           parser.ItemType
	Grouping     []string
	Without      bool
	Reason       string
	Estimate     *planEstimate
	ChildExplain ExplainNode
}

func (p *nativeAggregationPlan) execute(ctx context.Context, evaluator *evaluator, params evalParams) (model.RuntimeValue, error) {
	switch params.Mode {
	case evalModeInstant:
		sql, queryParams, err := storage.BuildInstantAggregationQuerySQL(storage.QueryConfig{Database: evaluator.opts.Database, Table: evaluator.opts.Table}, storage.AggregationSource{PromQLLeaf: p.Source.PromQLLeaf.String(), ValueExpr: p.Source.ValueExpr, TagsExpr: p.Source.TagsExpr}, params.EvaluationTime.UnixMilli(), p.Op, p.Grouping, p.Without)
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
		return model.VectorValue{Samples: samples}, nil
	case evalModeRange:
		sql, queryParams, err := storage.BuildRangeAggregationQuerySQL(storage.QueryConfig{Database: evaluator.opts.Database, Table: evaluator.opts.Table}, storage.AggregationSource{PromQLLeaf: p.Source.PromQLLeaf.String(), ValueExpr: p.Source.ValueExpr, TagsExpr: p.Source.TagsExpr}, params.Start.UnixMilli(), params.End.UnixMilli(), params.Step.Milliseconds(), p.Op, p.Grouping, p.Without)
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
		return model.MatrixValue{Series: series}, nil
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

func (p *scalarLiteralPlan) execute(_ context.Context, _ *evaluator, params evalParams) (model.RuntimeValue, error) {
	timestamp := float64(params.EvaluationTime.UnixNano()) / float64(time.Second)
	return model.ScalarValue{Timestamp: timestamp, Value: p.Value}, nil
}

func (p *scalarLiteralPlan) explain() ExplainNode {
	return ExplainNode{Kind: "scalar", Strategy: "local", Expr: p.Expr}
}

type localUnaryPlan struct {
	Expr  string
	Op    parser.ItemType
	Child queryPlan
}

func (p *localUnaryPlan) execute(ctx context.Context, evaluator *evaluator, params evalParams) (model.RuntimeValue, error) {
	childValue, err := p.Child.execute(ctx, evaluator, params)
	if err != nil {
		return nil, withInternalContext(err, "evaluating unary expression op=%s", p.Op.String())
	}
	result, err := exec.ApplyUnaryRuntimeValue(p.Op, childValue, exec.EvalParams{
		Mode:           toExecEvalMode(params.Mode),
		EvaluationTime: params.EvaluationTime,
		Start:          params.Start,
		End:            params.End,
		Step:           params.Step,
	})
	if err != nil {
		return nil, withInternalContext(fromExecError(err), "applying unary expression op=%s", p.Op.String())
	}
	return result, nil
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

func (p *localBinaryPlan) execute(ctx context.Context, evaluator *evaluator, params evalParams) (model.RuntimeValue, error) {
	lhsValue, err := p.LHS.execute(ctx, evaluator, params)
	if err != nil {
		return nil, withInternalContext(err, "evaluating left operand for binary op=%s", p.Op.String())
	}
	rhsValue, err := p.RHS.execute(ctx, evaluator, params)
	if err != nil {
		return nil, withInternalContext(err, "evaluating right operand for binary op=%s", p.Op.String())
	}
	result, err := exec.ApplyBinaryRuntimeValue(p.Op, lhsValue, rhsValue, p.ReturnBool, exec.EvalParams{
		Mode:           toExecEvalMode(params.Mode),
		EvaluationTime: params.EvaluationTime,
		Start:          params.Start,
		End:            params.End,
		Step:           params.Step,
	})
	if err != nil {
		return nil, withInternalContext(fromExecError(err), "applying binary expression op=%s returnBool=%t", p.Op.String(), p.ReturnBool)
	}
	return result, nil
}

func (p *localBinaryPlan) explain() ExplainNode {
	return ExplainNode{Kind: "binary", Strategy: "local", Expr: p.Expr, Children: []ExplainNode{p.LHS.explain(), p.RHS.explain()}}
}

type localAggregationPlan struct {
	Expr     string
	Op       parser.ItemType
	Grouping []string
	Without  bool
	Reason   string
	Child    queryPlan
}

func (p *localAggregationPlan) execute(ctx context.Context, evaluator *evaluator, params evalParams) (model.RuntimeValue, error) {
	childValue, err := p.Child.execute(ctx, evaluator, params)
	if err != nil {
		return nil, withInternalContext(err, "evaluating local aggregation op=%s grouping=%v without=%t", p.Op.String(), p.Grouping, p.Without)
	}
	result, err := exec.AggregateRuntimeValue(p.Op, childValue, p.Grouping, p.Without, params.EvaluationTime)
	if err != nil {
		return nil, withInternalContext(fromExecError(err), "aggregating local result op=%s grouping=%v without=%t", p.Op.String(), p.Grouping, p.Without)
	}
	return result, nil
}

func (p *localAggregationPlan) explain() ExplainNode {
	return ExplainNode{Kind: "aggregation", Strategy: "local", Expr: p.Expr, Reason: p.Reason, Children: []ExplainNode{p.Child.explain()}}
}

type localLabelReplacePlan struct {
	Expr   string
	Config model.LabelReplaceConfig
	Child  queryPlan
}

func (p *localLabelReplacePlan) execute(ctx context.Context, evaluator *evaluator, params evalParams) (model.RuntimeValue, error) {
	childValue, err := p.Child.execute(ctx, evaluator, params)
	if err != nil {
		return nil, withInternalContext(err, "evaluating label_replace child dst=%q src=%q", p.Config.Dst, p.Config.Src)
	}
	result, err := exec.ApplyLabelReplaceRuntimeValue(childValue, p.Config)
	if err != nil {
		return nil, withInternalContext(fromExecError(err), "applying label_replace dst=%q src=%q", p.Config.Dst, p.Config.Src)
	}
	return result, nil
}

func (p *localLabelReplacePlan) explain() ExplainNode {
	return ExplainNode{Kind: "label_replace", Strategy: "local", Expr: p.Expr, Children: []ExplainNode{p.Child.explain()}}
}

type localLabelJoinPlan struct {
	Expr   string
	Config model.LabelJoinConfig
	Child  queryPlan
}

func (p *localLabelJoinPlan) execute(ctx context.Context, evaluator *evaluator, params evalParams) (model.RuntimeValue, error) {
	childValue, err := p.Child.execute(ctx, evaluator, params)
	if err != nil {
		return nil, withInternalContext(err, "evaluating label_join child dst=%q src=%v", p.Config.Dst, p.Config.SrcLabels)
	}
	result, err := exec.ApplyLabelJoinRuntimeValue(childValue, p.Config)
	if err != nil {
		return nil, withInternalContext(fromExecError(err), "applying label_join dst=%q src=%v", p.Config.Dst, p.Config.SrcLabels)
	}
	return result, nil
}

func (p *localLabelJoinPlan) explain() ExplainNode {
	return ExplainNode{Kind: "label_join", Strategy: "local", Expr: p.Expr, Children: []ExplainNode{p.Child.explain()}}
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
	switch params.Mode {
	case evalModeInstant:
		sql, queryParams := storage.BuildInstantQuerySQL(storage.QueryConfig{Database: e.opts.Database, Table: e.opts.Table}, expr.String(), params.EvaluationTime.UnixMilli())
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
			return model.VectorValue{Samples: samples}, nil
		case parser.ValueTypeMatrix:
			series, err := decodeRangeSeries(response.Body)
			if err != nil {
				return nil, withInternalContext(err, "decoding delegated instant matrix result for %q", expr.String())
			}
			return model.MatrixValue{Series: series}, nil
		default:
			return nil, newUnsupportedErrorf("delegated instant result type %q for %q is not implemented yet", expr.Type(), expr.String())
		}
	case evalModeRange:
		sql, queryParams := storage.BuildRangeQuerySQL(storage.QueryConfig{Database: e.opts.Database, Table: e.opts.Table}, expr.String(), params.Start.UnixMilli(), params.End.UnixMilli(), params.Step.Milliseconds())
		response, err := e.client.Execute(ctx, sql, queryParams)
		if err != nil {
			return nil, withInternalContext(normalizeInternalError(err), "executing delegated ClickHouse range query for %q", expr.String())
		}
		defer response.Body.Close()

		series, err := decodeRangeSeries(response.Body)
		if err != nil {
			return nil, withInternalContext(err, "decoding delegated range matrix result for %q", expr.String())
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
		return &scalarLiteralPlan{Expr: node.ExprString(), Value: node.Value}, nil
	case *logicalUnaryPlan:
		child, err := buildExecPlanWithContext(node.Child, ctx)
		if err != nil {
			return nil, withInternalContext(err, "building execution child plan for unary expression %q", node.ExprString())
		}
		return &localUnaryPlan{Expr: node.ExprString(), Op: node.Op, Child: child}, nil
	case *logicalBinaryPlan:
		lhs, err := buildExecPlanWithContext(node.LHS, ctx)
		if err != nil {
			return nil, withInternalContext(err, "building execution left operand plan for binary expression %q", node.ExprString())
		}
		rhs, err := buildExecPlanWithContext(node.RHS, ctx)
		if err != nil {
			return nil, withInternalContext(err, "building execution right operand plan for binary expression %q", node.ExprString())
		}
		return &localBinaryPlan{Expr: node.ExprString(), Op: node.Op, ReturnBool: node.ReturnBool, LHS: lhs, RHS: rhs}, nil
	case *logicalAggregationPlan:
		if pushdownPlan, ok := maybeBuildNativeAggregationPlan(node, ctx); ok {
			return pushdownPlan, nil
		}
		child, err := buildExecPlanWithContext(node.Child, ctx)
		if err != nil {
			return nil, withInternalContext(err, "building execution child plan for aggregate %q", node.ExprString())
		}
		decision := decideNativeAggregationPushdown(node, ctx)
		return &localAggregationPlan{
			Expr:     node.ExprString(),
			Op:       node.Op,
			Grouping: append([]string(nil), node.Grouping...),
			Without:  node.Without,
			Reason:   decision.Reason,
			Child:    child,
		}, nil
	case *logicalLabelReplacePlan:
		child, err := buildExecPlanWithContext(node.Child, ctx)
		if err != nil {
			return nil, withInternalContext(err, "building execution child plan for label_replace %q", node.ExprString())
		}
		return &localLabelReplacePlan{Expr: node.ExprString(), Config: node.Config, Child: child}, nil
	case *logicalLabelJoinPlan:
		child, err := buildExecPlanWithContext(node.Child, ctx)
		if err != nil {
			return nil, withInternalContext(err, "building execution child plan for label_join %q", node.ExprString())
		}
		return &localLabelJoinPlan{Expr: node.ExprString(), Config: node.Config, Child: child}, nil
	default:
		return nil, newExecutionErrorf("execution planner cannot lower logical node %T", plan)
	}
}

func maybeBuildNativeAggregationPlan(node *logicalAggregationPlan, ctx planContext) (queryPlan, bool) {
	decision := decideNativeAggregationPushdown(node, ctx)
	if !decision.Eligible {
		return nil, false
	}
	return &nativeAggregationPlan{
		Expr:         node.ExprString(),
		Source:       decision.Source,
		Op:           node.Op,
		Grouping:     append([]string(nil), node.Grouping...),
		Without:      node.Without,
		Reason:       decision.Reason,
		Estimate:     estimateRangePlan(ctx),
		ChildExplain: decision.Source.Explain,
	}, true
}

func buildNativeAggregationSource(plan logicalPlan) (nativeAggregationSource, bool) {
	switch node := plan.(type) {
	case *logicalLeafExprPlan:
		return nativeAggregationSource{
			PromQLLeaf:  node.Expr,
			ValueExpr:   "{value}",
			TagsExpr:    "{tags}",
			DropsMetric: false,
			Explain: ExplainNode{
				Kind:     "leaf",
				Strategy: "delegated_promql",
				Expr:     node.ExprString(),
				Reason:   "delegatable child expression embedded as the source for native SQL aggregation",
			},
		}, true
	case *logicalUnaryPlan:
		child, ok := buildNativeAggregationSource(node.Child)
		if !ok {
			return nativeAggregationSource{}, false
		}
		valueExpr, dropsMetric, ok := applyNativeUnaryValueTransform(node.Op, child.ValueExpr, child.DropsMetric)
		if !ok {
			return nativeAggregationSource{}, false
		}
		child.ValueExpr = valueExpr
		child.DropsMetric = dropsMetric
		child.TagsExpr = nativeAggregationTagsExpr(dropsMetric)
		child.Explain = ExplainNode{
			Kind:     "unary",
			Strategy: "native_sql_expression",
			Expr:     node.ExprString(),
			Reason:   "unary transform is applied inside the native SQL aggregation source",
			Children: []ExplainNode{child.Explain},
		}
		return child, true
	case *logicalBinaryPlan:
		return buildNativeBinaryAggregationSource(node)
	default:
		return nativeAggregationSource{}, false
	}
}

func buildNativeBinaryAggregationSource(node *logicalBinaryPlan) (nativeAggregationSource, bool) {
	lhsScalar, lhsIsScalar := node.LHS.(*logicalScalarLiteralPlan)
	rhsScalar, rhsIsScalar := node.RHS.(*logicalScalarLiteralPlan)

	switch {
	case lhsIsScalar:
		child, ok := buildNativeAggregationSource(node.RHS)
		if !ok {
			return nativeAggregationSource{}, false
		}
		valueExpr, dropsMetric, ok := applyNativeBinaryValueTransform(node.Op, child.ValueExpr, lhsScalar.Value, true)
		if !ok {
			return nativeAggregationSource{}, false
		}
		child.ValueExpr = valueExpr
		child.DropsMetric = dropsMetric
		child.TagsExpr = nativeAggregationTagsExpr(dropsMetric)
		child.Explain = ExplainNode{
			Kind:     "binary",
			Strategy: "native_sql_expression",
			Expr:     node.ExprString(),
			Reason:   "scalar-vector arithmetic is applied inside the native SQL aggregation source",
			Children: []ExplainNode{child.Explain},
		}
		return child, true
	case rhsIsScalar:
		child, ok := buildNativeAggregationSource(node.LHS)
		if !ok {
			return nativeAggregationSource{}, false
		}
		valueExpr, dropsMetric, ok := applyNativeBinaryValueTransform(node.Op, child.ValueExpr, rhsScalar.Value, false)
		if !ok {
			return nativeAggregationSource{}, false
		}
		child.ValueExpr = valueExpr
		child.DropsMetric = dropsMetric
		child.TagsExpr = nativeAggregationTagsExpr(dropsMetric)
		child.Explain = ExplainNode{
			Kind:     "binary",
			Strategy: "native_sql_expression",
			Expr:     node.ExprString(),
			Reason:   "vector-scalar arithmetic is applied inside the native SQL aggregation source",
			Children: []ExplainNode{child.Explain},
		}
		return child, true
	default:
		return nativeAggregationSource{}, false
	}
}

func applyNativeUnaryValueTransform(op parser.ItemType, valueExpr string, childDropsMetric bool) (string, bool, bool) {
	switch op {
	case parser.ADD:
		return valueExpr, childDropsMetric, true
	case parser.SUB:
		return "-" + nativeWrapValueExpr(valueExpr), true, true
	default:
		return "", false, false
	}
}

func applyNativeBinaryValueTransform(op parser.ItemType, valueExpr string, scalar float64, scalarOnLeft bool) (string, bool, bool) {
	valueExpr = nativeWrapValueExpr(valueExpr)
	scalarExpr := storage.NativeFloatLiteral(scalar)

	switch op {
	case parser.ADD:
		if scalarOnLeft {
			return scalarExpr + " + " + valueExpr, true, true
		}
		return valueExpr + " + " + scalarExpr, true, true
	case parser.SUB:
		if scalarOnLeft {
			return scalarExpr + " - " + valueExpr, true, true
		}
		return valueExpr + " - " + scalarExpr, true, true
	case parser.MUL:
		if scalarOnLeft {
			return scalarExpr + " * " + valueExpr, true, true
		}
		return valueExpr + " * " + scalarExpr, true, true
	case parser.DIV:
		if scalarOnLeft {
			return scalarExpr + " / " + valueExpr, true, true
		}
		return valueExpr + " / " + scalarExpr, true, true
	case parser.MOD:
		if scalarOnLeft {
			return "modulo(" + scalarExpr + ", " + valueExpr + ")", true, true
		}
		return "modulo(" + valueExpr + ", " + scalarExpr + ")", true, true
	case parser.POW:
		if scalarOnLeft {
			return "pow(" + scalarExpr + ", " + valueExpr + ")", true, true
		}
		return "pow(" + valueExpr + ", " + scalarExpr + ")", true, true
	default:
		return "", false, false
	}
}

func nativeAggregationTagsExpr(dropMetric bool) string {
	if !dropMetric {
		return "{tags}"
	}
	return "arrayFilter(tag -> tag.1 != '__name__', {tags})"
}

func nativeWrapValueExpr(expr string) string {
	return "(" + expr + ")"
}

func toExecEvalMode(mode evalMode) exec.EvalMode {
	if mode == evalModeRange {
		return exec.EvalModeRange
	}
	return exec.EvalModeInstant
}
