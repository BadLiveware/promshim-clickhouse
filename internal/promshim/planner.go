package promshim

import (
	"context"
	"sort"
	"time"

	"ch-observability/internal/promshim/exec"
	"ch-observability/internal/promshim/model"
	nativeplan "ch-observability/internal/promshim/native"
	planpkg "ch-observability/internal/promshim/plan"
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
	sourcePromQL, err := resolveDelegatedPromQL(p.Source.PromQLLeaf, params)
	if err != nil {
		return nil, withInternalContext(err, "resolving native aggregation source PromQL for %q", p.Expr)
	}

	switch params.Mode {
	case evalModeInstant:
		sql, queryParams, err := storage.BuildInstantAggregationQuerySQL(storage.QueryConfig{Database: evaluator.opts.Database, Table: evaluator.opts.Table}, storage.AggregationSource{PromQLLeaf: sourcePromQL, ValueExpr: p.Source.ValueExpr, TagsExpr: p.Source.TagsExpr}, params.EvaluationTime.UnixMilli(), p.Op, p.Grouping, p.Without)
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
		sql, queryParams, err := storage.BuildRangeAggregationQuerySQL(storage.QueryConfig{Database: evaluator.opts.Database, Table: evaluator.opts.Table}, storage.AggregationSource{PromQLLeaf: sourcePromQL, ValueExpr: p.Source.ValueExpr, TagsExpr: p.Source.TagsExpr}, params.Start.UnixMilli(), params.End.UnixMilli(), params.Step.Milliseconds(), p.Op, p.Grouping, p.Without)
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
	Expr           string
	Op             parser.ItemType
	VectorMatching *parser.VectorMatching
	ReturnBool     bool
	LHS            queryPlan
	RHS            queryPlan
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
	result, err := exec.ApplyBinaryRuntimeValue(p.Op, lhsValue, rhsValue, p.VectorMatching, p.ReturnBool, exec.EvalParams{
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
	Expr        string
	Op          parser.ItemType
	Grouping    []string
	Without     bool
	ParamNumber *float64
	ParamString string
	Reason      string
	Child       queryPlan
}

func (p *localAggregationPlan) execute(ctx context.Context, evaluator *evaluator, params evalParams) (model.RuntimeValue, error) {
	childValue, err := p.Child.execute(ctx, evaluator, params)
	if err != nil {
		return nil, withInternalContext(err, "evaluating local aggregation op=%s grouping=%v without=%t", p.Op.String(), p.Grouping, p.Without)
	}
	result, err := exec.AggregateRuntimeValue(p.Op, childValue, exec.AggregationOptions{
		Grouping:       p.Grouping,
		Without:        p.Without,
		EvaluationTime: params.EvaluationTime,
		ParamNumber:    p.ParamNumber,
		ParamString:    p.ParamString,
	})
	if err != nil {
		return nil, withInternalContext(fromExecError(err), "aggregating local result op=%s grouping=%v without=%t", p.Op.String(), p.Grouping, p.Without)
	}
	return result, nil
}

func (p *localAggregationPlan) explain() ExplainNode {
	return ExplainNode{Kind: "aggregation", Strategy: "local", Expr: p.Expr, Reason: p.Reason, Children: []ExplainNode{p.Child.explain()}}
}

type localHistogramQuantilePlan struct {
	Expr     string
	Quantile float64
	Child    queryPlan
}

func (p *localHistogramQuantilePlan) execute(ctx context.Context, evaluator *evaluator, params evalParams) (model.RuntimeValue, error) {
	childValue, err := p.Child.execute(ctx, evaluator, params)
	if err != nil {
		return nil, withInternalContext(err, "evaluating histogram_quantile child quantile=%v", p.Quantile)
	}
	result, err := exec.ApplyHistogramQuantileRuntimeValue(p.Quantile, childValue)
	if err != nil {
		return nil, withInternalContext(fromExecError(err), "applying histogram_quantile quantile=%v", p.Quantile)
	}
	return result, nil
}

func (p *localHistogramQuantilePlan) explain() ExplainNode {
	return ExplainNode{Kind: "histogram_quantile", Strategy: "local", Expr: p.Expr, Children: []ExplainNode{p.Child.explain()}}
}

type localHistogramFractionPlan struct {
	Expr  string
	Lower float64
	Upper float64
	Child queryPlan
}

func (p *localHistogramFractionPlan) execute(ctx context.Context, evaluator *evaluator, params evalParams) (model.RuntimeValue, error) {
	childValue, err := p.Child.execute(ctx, evaluator, params)
	if err != nil {
		return nil, withInternalContext(err, "evaluating histogram_fraction child bounds=[%v,%v]", p.Lower, p.Upper)
	}
	result, err := exec.ApplyHistogramFractionRuntimeValue(p.Lower, p.Upper, childValue)
	if err != nil {
		return nil, withInternalContext(fromExecError(err), "applying histogram_fraction bounds=[%v,%v]", p.Lower, p.Upper)
	}
	return result, nil
}

func (p *localHistogramFractionPlan) explain() ExplainNode {
	return ExplainNode{Kind: "histogram_fraction", Strategy: "local", Expr: p.Expr, Children: []ExplainNode{p.Child.explain()}}
}

type localHistogramProjectionPlan struct {
	Expr  string
	Func  string
	Child queryPlan
}

func (p *localHistogramProjectionPlan) execute(ctx context.Context, evaluator *evaluator, params evalParams) (model.RuntimeValue, error) {
	childValue, err := p.Child.execute(ctx, evaluator, params)
	if err != nil {
		return nil, withInternalContext(err, "evaluating %s child", p.Func)
	}
	var result model.RuntimeValue
	switch p.Func {
	case "histogram_count":
		result, err = exec.ApplyHistogramCountRuntimeValue(childValue)
	case "histogram_sum":
		result, err = exec.ApplyHistogramSumRuntimeValue(childValue)
	case "histogram_avg":
		result, err = exec.ApplyHistogramAvgRuntimeValue(childValue)
	default:
		return nil, newExecutionErrorf("unknown histogram projection function %q", p.Func)
	}
	if err != nil {
		return nil, withInternalContext(fromExecError(err), "applying %s", p.Func)
	}
	return result, nil
}

func (p *localHistogramProjectionPlan) explain() ExplainNode {
	return ExplainNode{Kind: p.Func, Strategy: "local", Expr: p.Expr, Children: []ExplainNode{p.Child.explain()}}
}

type localRangeFunctionPlan struct {
	Expr  string
	Func  string
	Child queryPlan
}

func (p *localRangeFunctionPlan) execute(ctx context.Context, evaluator *evaluator, params evalParams) (model.RuntimeValue, error) {
	switch params.Mode {
	case evalModeInstant:
		childValue, err := p.Child.execute(ctx, evaluator, params)
		if err != nil {
			return nil, withInternalContext(err, "evaluating %s child in instant mode", p.Func)
		}
		vector, err := exec.ApplyRangeFunctionInstant(p.Func, childValue)
		if err != nil {
			return nil, withInternalContext(fromExecError(err), "applying %s in instant mode", p.Func)
		}
		return vector, nil
	case evalModeRange:
		return executeRangeVectorPlan(ctx, evaluator, params, p.Func, p.execute)
	default:
		return nil, newExecutionErrorf("unknown evaluation mode %q", params.Mode)
	}
}

func (p *localRangeFunctionPlan) explain() ExplainNode {
	return ExplainNode{Kind: p.Func, Strategy: "local", Expr: p.Expr, Children: []ExplainNode{p.Child.explain()}}
}

type localVectorPlan struct {
	Expr  string
	Child queryPlan
}

func (p *localVectorPlan) execute(ctx context.Context, evaluator *evaluator, params evalParams) (model.RuntimeValue, error) {
	switch params.Mode {
	case evalModeInstant:
		childValue, err := p.Child.execute(ctx, evaluator, params)
		if err != nil {
			return nil, withInternalContext(err, "evaluating vector child in instant mode")
		}
		vector, err := exec.ApplyVector(childValue)
		if err != nil {
			return nil, withInternalContext(fromExecError(err), "applying vector()")
		}
		return vector, nil
	case evalModeRange:
		return executeRangeVectorPlan(ctx, evaluator, params, "vector", p.execute)
	default:
		return nil, newExecutionErrorf("unknown evaluation mode %q", params.Mode)
	}
}

func (p *localVectorPlan) explain() ExplainNode {
	return ExplainNode{Kind: "vector", Strategy: "local", Expr: p.Expr, Children: []ExplainNode{p.Child.explain()}}
}

type localRoundPlan struct {
	Expr     string
	Decimals *float64
	Child    queryPlan
}

func (p *localRoundPlan) execute(ctx context.Context, evaluator *evaluator, params evalParams) (model.RuntimeValue, error) {
	switch params.Mode {
	case evalModeInstant:
		childValue, err := p.Child.execute(ctx, evaluator, params)
		if err != nil {
			return nil, withInternalContext(err, "evaluating round child in instant mode")
		}
		vector, err := exec.ApplyRound(childValue, p.Decimals)
		if err != nil {
			return nil, withInternalContext(fromExecError(err), "applying round")
		}
		return vector, nil
	case evalModeRange:
		return executeRangeVectorPlan(ctx, evaluator, params, "round", p.execute)
	default:
		return nil, newExecutionErrorf("unknown evaluation mode %q", params.Mode)
	}
}

func (p *localRoundPlan) explain() ExplainNode {
	return ExplainNode{Kind: "round", Strategy: "local", Expr: p.Expr, Children: []ExplainNode{p.Child.explain()}}
}

type localRatePlan struct {
	Expr  string
	Func  string
	Child queryPlan
}

func (p *localRatePlan) execute(ctx context.Context, evaluator *evaluator, params evalParams) (model.RuntimeValue, error) {
	switch params.Mode {
	case evalModeInstant:
		childValue, err := p.Child.execute(ctx, evaluator, params)
		if err != nil {
			return nil, withInternalContext(err, "evaluating %s child in instant mode", p.Func)
		}
		var vector model.VectorValue
		switch p.Func {
		case "rate":
			vector, err = exec.ApplyRate(childValue)
		case "irate":
			vector, err = exec.ApplyIRate(childValue)
		default:
			return nil, newExecutionErrorf("unknown local rate function %q", p.Func)
		}
		if err != nil {
			return nil, withInternalContext(fromExecError(err), "applying %s", p.Func)
		}
		return vector, nil
	case evalModeRange:
		return executeRangeVectorPlan(ctx, evaluator, params, p.Func, p.execute)
	default:
		return nil, newExecutionErrorf("unknown evaluation mode %q", params.Mode)
	}
}

func (p *localRatePlan) explain() ExplainNode {
	return ExplainNode{Kind: p.Func, Strategy: "local", Expr: p.Expr, Children: []ExplainNode{p.Child.explain()}}
}

type localIncreasePlan struct {
	Expr  string
	Child queryPlan
}

func (p *localIncreasePlan) execute(ctx context.Context, evaluator *evaluator, params evalParams) (model.RuntimeValue, error) {
	switch params.Mode {
	case evalModeInstant:
		childValue, err := p.Child.execute(ctx, evaluator, params)
		if err != nil {
			return nil, withInternalContext(err, "evaluating increase child in instant mode")
		}
		vector, err := exec.ApplyIncreaseInstant(childValue)
		if err != nil {
			return nil, withInternalContext(fromExecError(err), "applying increase in instant mode")
		}
		return vector, nil
	case evalModeRange:
		return executeRangeVectorPlan(ctx, evaluator, params, "increase", p.execute)
	default:
		return nil, newExecutionErrorf("unknown evaluation mode %q", params.Mode)
	}
}

func (p *localIncreasePlan) explain() ExplainNode {
	return ExplainNode{Kind: "increase", Strategy: "local", Expr: p.Expr, Children: []ExplainNode{p.Child.explain()}}
}

type localDeltaPlan struct {
	Expr  string
	Func  string
	Child queryPlan
}

func (p *localDeltaPlan) execute(ctx context.Context, evaluator *evaluator, params evalParams) (model.RuntimeValue, error) {
	switch params.Mode {
	case evalModeInstant:
		childValue, err := p.Child.execute(ctx, evaluator, params)
		if err != nil {
			return nil, withInternalContext(err, "evaluating %s child in instant mode", p.Func)
		}
		var vector model.VectorValue
		switch p.Func {
		case "delta":
			vector, err = exec.ApplyDelta(childValue)
		case "idelta":
			vector, err = exec.ApplyIDelta(childValue)
		default:
			return nil, newExecutionErrorf("unknown local delta function %q", p.Func)
		}
		if err != nil {
			return nil, withInternalContext(fromExecError(err), "applying %s", p.Func)
		}
		return vector, nil
	case evalModeRange:
		return executeRangeVectorPlan(ctx, evaluator, params, p.Func, p.execute)
	default:
		return nil, newExecutionErrorf("unknown evaluation mode %q", params.Mode)
	}
}

func (p *localDeltaPlan) explain() ExplainNode {
	return ExplainNode{Kind: p.Func, Strategy: "local", Expr: p.Expr, Children: []ExplainNode{p.Child.explain()}}
}

type localChangesPlan struct {
	Expr  string
	Child queryPlan
}

func (p *localChangesPlan) execute(ctx context.Context, evaluator *evaluator, params evalParams) (model.RuntimeValue, error) {
	switch params.Mode {
	case evalModeInstant:
		childValue, err := p.Child.execute(ctx, evaluator, params)
		if err != nil {
			return nil, withInternalContext(err, "evaluating changes child in instant mode")
		}
		vector, err := exec.ApplyChanges(childValue)
		if err != nil {
			return nil, withInternalContext(fromExecError(err), "applying changes")
		}
		return vector, nil
	case evalModeRange:
		return executeRangeVectorPlan(ctx, evaluator, params, "changes", p.execute)
	default:
		return nil, newExecutionErrorf("unknown evaluation mode %q", params.Mode)
	}
}

func (p *localChangesPlan) explain() ExplainNode {
	return ExplainNode{Kind: "changes", Strategy: "local", Expr: p.Expr, Children: []ExplainNode{p.Child.explain()}}
}

type localDerivPlan struct {
	Expr  string
	Child queryPlan
}

func (p *localDerivPlan) execute(ctx context.Context, evaluator *evaluator, params evalParams) (model.RuntimeValue, error) {
	switch params.Mode {
	case evalModeInstant:
		childValue, err := p.Child.execute(ctx, evaluator, params)
		if err != nil {
			return nil, withInternalContext(err, "evaluating deriv child in instant mode")
		}
		vector, err := exec.ApplyDeriv(childValue)
		if err != nil {
			return nil, withInternalContext(fromExecError(err), "applying deriv")
		}
		return vector, nil
	case evalModeRange:
		return executeRangeVectorPlan(ctx, evaluator, params, "deriv", p.execute)
	default:
		return nil, newExecutionErrorf("unknown evaluation mode %q", params.Mode)
	}
}

func (p *localDerivPlan) explain() ExplainNode {
	return ExplainNode{Kind: "deriv", Strategy: "local", Expr: p.Expr, Children: []ExplainNode{p.Child.explain()}}
}

type localQuantileOverTimePlan struct {
	Expr     string
	Quantile float64
	Child    queryPlan
}

func (p *localQuantileOverTimePlan) execute(ctx context.Context, evaluator *evaluator, params evalParams) (model.RuntimeValue, error) {
	switch params.Mode {
	case evalModeInstant:
		childValue, err := p.Child.execute(ctx, evaluator, params)
		if err != nil {
			return nil, withInternalContext(err, "evaluating quantile_over_time child in instant mode")
		}
		vector, err := exec.ApplyQuantileOverTime(p.Quantile, childValue)
		if err != nil {
			return nil, withInternalContext(fromExecError(err), "applying quantile_over_time in instant mode")
		}
		return vector, nil
	case evalModeRange:
		return executeRangeVectorPlan(ctx, evaluator, params, "quantile_over_time", p.execute)
	default:
		return nil, newExecutionErrorf("unknown evaluation mode %q", params.Mode)
	}
}

func (p *localQuantileOverTimePlan) explain() ExplainNode {
	return ExplainNode{Kind: "quantile_over_time", Strategy: "local", Expr: p.Expr, Children: []ExplainNode{p.Child.explain()}}
}

type localAbsentPlan struct {
	Expr         string
	OutputMetric map[string]string
	Child        queryPlan
}

func (p *localAbsentPlan) execute(ctx context.Context, evaluator *evaluator, params evalParams) (model.RuntimeValue, error) {
	switch params.Mode {
	case evalModeInstant:
		childValue, err := p.Child.execute(ctx, evaluator, params)
		if err != nil {
			return nil, withInternalContext(err, "evaluating absent child in instant mode")
		}
		vector, err := exec.ApplyAbsent(childValue, p.OutputMetric, float64(params.EvaluationTime.UnixNano())/float64(time.Second))
		if err != nil {
			return nil, withInternalContext(fromExecError(err), "applying absent in instant mode")
		}
		return vector, nil
	case evalModeRange:
		return executeRangeVectorPlan(ctx, evaluator, params, "absent", p.execute)
	default:
		return nil, newExecutionErrorf("unknown evaluation mode %q", params.Mode)
	}
}

func (p *localAbsentPlan) explain() ExplainNode {
	return ExplainNode{Kind: "absent", Strategy: "local", Expr: p.Expr, Children: []ExplainNode{p.Child.explain()}}
}

type localAbsentOverTimePlan struct {
	Expr               string
	OutputMetric       map[string]string
	BoundaryProbeExpr  parser.Expr
	BoundaryProbeRange time.Duration
	Child              queryPlan
}

func (p *localAbsentOverTimePlan) execute(ctx context.Context, evaluator *evaluator, params evalParams) (model.RuntimeValue, error) {
	switch params.Mode {
	case evalModeInstant:
		childValue, err := p.Child.execute(ctx, evaluator, params)
		if err != nil {
			return nil, withInternalContext(err, "evaluating absent_over_time child in instant mode")
		}
		vector, err := exec.ApplyAbsentOverTime(childValue, p.OutputMetric, float64(params.EvaluationTime.UnixNano())/float64(time.Second))
		if err != nil {
			return nil, withInternalContext(fromExecError(err), "applying absent_over_time in instant mode")
		}
		if len(vector.Samples) > 0 && p.BoundaryProbeExpr != nil && p.BoundaryProbeRange > 0 {
			boundaryTime := params.EvaluationTime.Add(-p.BoundaryProbeRange)
			boundaryValue, err := evaluator.executeDelegated(ctx, p.BoundaryProbeExpr, evalParams{Mode: evalModeInstant, EvaluationTime: boundaryTime})
			if err != nil {
				return nil, withInternalContext(err, "probing absent_over_time left boundary at %s", boundaryTime.UTC().Format(time.RFC3339Nano))
			}
			probeMatrix, ok := boundaryValue.(model.MatrixValue)
			if !ok {
				return nil, newExecutionErrorf("absent_over_time boundary probe returned %T, expected matrix", boundaryValue)
			}
			for _, series := range probeMatrix.Series {
				if len(series.Values) > 0 {
					return model.VectorValue{}, nil
				}
			}
		}
		return vector, nil
	case evalModeRange:
		return executeRangeVectorPlan(ctx, evaluator, params, "absent_over_time", p.execute)
	default:
		return nil, newExecutionErrorf("unknown evaluation mode %q", params.Mode)
	}
}

func (p *localAbsentOverTimePlan) explain() ExplainNode {
	return ExplainNode{Kind: "absent_over_time", Strategy: "local", Expr: p.Expr, Children: []ExplainNode{p.Child.explain()}}
}

func executeRangeVectorPlan(ctx context.Context, evaluator *evaluator, params evalParams, kind string, executeInstant func(context.Context, *evaluator, evalParams) (model.RuntimeValue, error)) (model.RuntimeValue, error) {
	if params.Step <= 0 {
		return nil, newBadDataErrorf("step must be greater than zero for %q", kind)
	}
	seriesByKey := map[string]*model.RangeSeries{}
	seriesOrder := make([]string, 0)
	for ts := params.Start; !ts.After(params.End); ts = ts.Add(params.Step) {
		instantValue, err := executeInstant(ctx, evaluator, evalParams{Mode: evalModeInstant, EvaluationTime: ts})
		if err != nil {
			return nil, withInternalContext(err, "evaluating %s at range step %s", kind, ts.UTC().Format(time.RFC3339Nano))
		}
		vector, ok := instantValue.(model.VectorValue)
		if !ok {
			return nil, newExecutionErrorf("%s instant step returned %T, expected vector", kind, instantValue)
		}
		stepTimestamp := float64(ts.UnixNano()) / float64(time.Second)
		for _, sample := range vector.Samples {
			key := model.LabelsKey(sample.Metric)
			item, ok := seriesByKey[key]
			if !ok {
				item = &model.RangeSeries{Metric: model.CloneMetric(sample.Metric), Values: make([]model.RangePoint, 0, 16)}
				seriesByKey[key] = item
				seriesOrder = append(seriesOrder, key)
			}
			item.Values = append(item.Values, model.RangePoint{Timestamp: stepTimestamp, Value: sample.Value})
		}
	}
	sort.Strings(seriesOrder)
	result := make([]model.RangeSeries, 0, len(seriesOrder))
	for _, key := range seriesOrder {
		item := seriesByKey[key]
		result = append(result, model.RangeSeries{Metric: model.CloneMetric(item.Metric), Values: model.CloneRangePoints(item.Values)})
	}
	return model.MatrixValue{Series: result}, nil
}

type localSubqueryPlan struct {
	Expr                    parser.Expr
	Range                   time.Duration
	Step                    time.Duration
	Offset                  time.Duration
	Timestamp               *int64
	StartOrEnd              parser.ItemType
	DelegatedLeafCompatible bool
	Child                   queryPlan
}

func (p *localSubqueryPlan) execute(ctx context.Context, evaluator *evaluator, params evalParams) (model.RuntimeValue, error) {
	useDelegatedPath := p.DelegatedLeafCompatible && !(params.Mode == evalModeInstant && p.Expr != nil && p.Expr.Type() == parser.ValueTypeMatrix)
	if useDelegatedPath {
		value, err := evaluator.executeDelegated(ctx, p.Expr, params)
		if err != nil {
			return nil, withInternalContext(err, "executing delegated subquery expression %q", p.Expr.String())
		}
		return value, nil
	}
	if params.Mode != evalModeInstant {
		return nil, newUnsupportedErrorf("local subquery execution in %s mode is not implemented yet for %q", params.Mode, p.Expr.String())
	}

	end := params.EvaluationTime
	if p.Timestamp != nil {
		end = time.UnixMilli(*p.Timestamp).UTC()
	} else if resolved := resolveStartEndMillis(p.StartOrEnd, params); resolved != nil {
		end = time.UnixMilli(*resolved).UTC()
	}
	if p.Offset != 0 {
		end = end.Add(-p.Offset)
	}
	if p.Range <= 0 {
		return nil, newBadDataErrorf("subquery range must be greater than zero in %q", p.Expr.String())
	}
	step := p.Step
	if step <= 0 {
		step = defaultSubqueryStep(params)
	}
	if step <= 0 {
		return nil, newBadDataErrorf("subquery step must be greater than zero in %q", p.Expr.String())
	}

	start := end.Add(-p.Range)
	seriesByKey := map[string]*model.RangeSeries{}
	seriesOrder := make([]string, 0)

	for ts := start; !ts.After(end); ts = ts.Add(step) {
		childValue, err := p.Child.execute(ctx, evaluator, evalParams{Mode: evalModeInstant, EvaluationTime: ts})
		if err != nil {
			return nil, withInternalContext(err, "evaluating local subquery child at %s for %q", ts.UTC().Format(time.RFC3339Nano), p.Expr.String())
		}
		vector, ok := childValue.(model.VectorValue)
		if !ok {
			return nil, newExecutionErrorf("subquery child must evaluate to vector at %s for %q, got %T", ts.UTC().Format(time.RFC3339Nano), p.Expr.String(), childValue)
		}
		for _, sample := range vector.Samples {
			key := model.LabelsKey(sample.Metric)
			item, ok := seriesByKey[key]
			if !ok {
				item = &model.RangeSeries{Metric: model.CloneMetric(sample.Metric), Values: make([]model.RangePoint, 0, 16)}
				seriesByKey[key] = item
				seriesOrder = append(seriesOrder, key)
			}
			item.Values = append(item.Values, model.RangePoint{Timestamp: sample.Timestamp, Value: sample.Value})
		}
	}

	sort.Strings(seriesOrder)
	result := make([]model.RangeSeries, 0, len(seriesOrder))
	for _, key := range seriesOrder {
		item := seriesByKey[key]
		sort.Slice(item.Values, func(i, j int) bool { return item.Values[i].Timestamp < item.Values[j].Timestamp })
		result = append(result, model.RangeSeries{Metric: model.CloneMetric(item.Metric), Values: model.CloneRangePoints(item.Values)})
	}
	return model.MatrixValue{Series: result}, nil
}

func (p *localSubqueryPlan) explain() ExplainNode {
	strategy := "local"
	reason := "subquery requires local execution"
	if p.DelegatedLeafCompatible {
		strategy = "delegated_promql"
		reason = "subquery is delegated because child expression is delegated-leaf-compatible"
		if p.Expr != nil && p.Expr.Type() == parser.ValueTypeMatrix {
			strategy = "local"
			reason = "matrix-root subquery is evaluated locally for compatibility"
		}
	}
	children := []ExplainNode{}
	if p.Child != nil {
		children = append(children, p.Child.explain())
	}
	return ExplainNode{Kind: "subquery", Strategy: strategy, Expr: p.Expr.String(), Reason: reason, Children: children}
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

// clickHouseRangePadding compensates for ClickHouse's (t-range, t] range selector
// semantics versus Prometheus's [t-range, t]: padding every matrix/subquery range
// by 1ms pulls the left-boundary sample back into the window without perturbing
// aligned scrapes on the right. See harness P1 findings for the root cause.
const clickHouseRangePadding = time.Millisecond

func resolveDelegatedPromQL(expr parser.Expr, params evalParams) (string, error) {
	parsed, err := planpkg.ParseExpression(expr.String())
	if err != nil {
		return "", newExecutionErrorf("re-parsing expression for delegation rewrite: %v", err)
	}
	if containsStartEndAtModifier(parsed) {
		resolveStartEndAtModifierRecursive(parsed, params)
	}
	padDelegatedRanges(parsed, clickHouseRangePadding)
	return parsed.String(), nil
}

func padDelegatedRanges(expr parser.Expr, delta time.Duration) {
	switch node := expr.(type) {
	case *parser.MatrixSelector:
		node.Range += delta
	case *parser.SubqueryExpr:
		node.Range += delta
		padDelegatedRanges(node.Expr, delta)
	case *parser.Call:
		for _, arg := range node.Args {
			padDelegatedRanges(arg, delta)
		}
	case *parser.AggregateExpr:
		if node.Param != nil {
			padDelegatedRanges(node.Param, delta)
		}
		padDelegatedRanges(node.Expr, delta)
	case *parser.BinaryExpr:
		padDelegatedRanges(node.LHS, delta)
		padDelegatedRanges(node.RHS, delta)
	case *parser.UnaryExpr:
		padDelegatedRanges(node.Expr, delta)
	case *parser.ParenExpr:
		padDelegatedRanges(node.Expr, delta)
	case *parser.StepInvariantExpr:
		padDelegatedRanges(node.Expr, delta)
	}
}

func containsStartEndAtModifier(expr parser.Expr) bool {
	switch node := expr.(type) {
	case *parser.VectorSelector:
		return node.StartOrEnd == parser.START || node.StartOrEnd == parser.END
	case *parser.MatrixSelector:
		return containsStartEndAtModifier(node.VectorSelector)
	case *parser.SubqueryExpr:
		if node.StartOrEnd == parser.START || node.StartOrEnd == parser.END {
			return true
		}
		return containsStartEndAtModifier(node.Expr)
	case *parser.Call:
		for _, arg := range node.Args {
			if containsStartEndAtModifier(arg) {
				return true
			}
		}
		return false
	case *parser.AggregateExpr:
		if node.Param != nil && containsStartEndAtModifier(node.Param) {
			return true
		}
		return containsStartEndAtModifier(node.Expr)
	case *parser.BinaryExpr:
		return containsStartEndAtModifier(node.LHS) || containsStartEndAtModifier(node.RHS)
	case *parser.UnaryExpr:
		return containsStartEndAtModifier(node.Expr)
	case *parser.ParenExpr:
		return containsStartEndAtModifier(node.Expr)
	case *parser.StepInvariantExpr:
		return containsStartEndAtModifier(node.Expr)
	default:
		return false
	}
}

func resolveStartEndAtModifierRecursive(expr parser.Expr, params evalParams) {
	switch node := expr.(type) {
	case *parser.VectorSelector:
		resolveSelectorStartEndAtModifier(node, params)
	case *parser.MatrixSelector:
		if selector, ok := node.VectorSelector.(*parser.VectorSelector); ok {
			resolveSelectorStartEndAtModifier(selector, params)
		}
		resolveStartEndAtModifierRecursive(node.VectorSelector, params)
	case *parser.SubqueryExpr:
		resolveSubqueryStartEndAtModifier(node, params)
		resolveStartEndAtModifierRecursive(node.Expr, params)
	case *parser.Call:
		for _, arg := range node.Args {
			resolveStartEndAtModifierRecursive(arg, params)
		}
	case *parser.AggregateExpr:
		if node.Param != nil {
			resolveStartEndAtModifierRecursive(node.Param, params)
		}
		resolveStartEndAtModifierRecursive(node.Expr, params)
	case *parser.BinaryExpr:
		resolveStartEndAtModifierRecursive(node.LHS, params)
		resolveStartEndAtModifierRecursive(node.RHS, params)
	case *parser.UnaryExpr:
		resolveStartEndAtModifierRecursive(node.Expr, params)
	case *parser.ParenExpr:
		resolveStartEndAtModifierRecursive(node.Expr, params)
	case *parser.StepInvariantExpr:
		resolveStartEndAtModifierRecursive(node.Expr, params)
	}
}

func resolveSelectorStartEndAtModifier(selector *parser.VectorSelector, params evalParams) {
	if selector == nil {
		return
	}
	resolved := resolveStartEndMillis(selector.StartOrEnd, params)
	if resolved == nil {
		return
	}
	selector.Timestamp = resolved
	selector.StartOrEnd = 0
}

func resolveSubqueryStartEndAtModifier(subquery *parser.SubqueryExpr, params evalParams) {
	if subquery == nil {
		return
	}
	resolved := resolveStartEndMillis(subquery.StartOrEnd, params)
	if resolved == nil {
		return
	}
	subquery.Timestamp = resolved
	subquery.StartOrEnd = 0
}

func resolveStartEndMillis(token parser.ItemType, params evalParams) *int64 {
	var resolved int64
	switch token {
	case parser.START:
		if params.Mode == evalModeRange {
			resolved = params.Start.UnixMilli()
		} else {
			resolved = params.EvaluationTime.UnixMilli()
		}
	case parser.END:
		if params.Mode == evalModeRange {
			resolved = params.End.UnixMilli()
		} else {
			resolved = params.EvaluationTime.UnixMilli()
		}
	default:
		return nil
	}
	value := resolved
	return &value
}

func cloneInt64Pointer(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func defaultSubqueryStep(params evalParams) time.Duration {
	if params.Step > 0 {
		return params.Step
	}
	return time.Minute
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
		if pushdownPlan, ok := maybeBuildNativeAggregationPlan(node, ctx, analysis); ok {
			return annotateQueryPlan(pushdownPlan, analysis.InfoFor(node)), nil
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
		child, err := buildExecPlanWithAnalysis(node.Child, ctx, analysis)
		if err != nil {
			return nil, withInternalContext(err, "building execution child plan for histogram_quantile %q", node.ExprString())
		}
		return annotateQueryPlan(&localHistogramQuantilePlan{Expr: node.ExprString(), Quantile: node.Quantile, Child: child}, analysis.InfoFor(node)), nil
	case *logicalHistogramFractionPlan:
		child, err := buildExecPlanWithAnalysis(node.Child, ctx, analysis)
		if err != nil {
			return nil, withInternalContext(err, "building execution child plan for histogram_fraction %q", node.ExprString())
		}
		return annotateQueryPlan(&localHistogramFractionPlan{Expr: node.ExprString(), Lower: node.Lower, Upper: node.Upper, Child: child}, analysis.InfoFor(node)), nil
	case *logicalHistogramProjectionPlan:
		child, err := buildExecPlanWithAnalysis(node.Child, ctx, analysis)
		if err != nil {
			return nil, withInternalContext(err, "building execution child plan for %s %q", node.Func, node.ExprString())
		}
		return annotateQueryPlan(&localHistogramProjectionPlan{Expr: node.ExprString(), Func: node.Func, Child: child}, analysis.InfoFor(node)), nil
	case *logicalRangeFunctionPlan:
		child, err := buildExecPlanWithAnalysis(node.Child, ctx, analysis)
		if err != nil {
			return nil, withInternalContext(err, "building execution child plan for %s %q", node.Func, node.ExprString())
		}
		return annotateQueryPlan(&localRangeFunctionPlan{Expr: node.ExprString(), Func: node.Func, Child: child}, analysis.InfoFor(node)), nil
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
	case *logicalRatePlan:
		child, err := buildExecPlanWithAnalysis(node.Child, ctx, analysis)
		if err != nil {
			return nil, withInternalContext(err, "building execution child plan for %s %q", node.Func, node.ExprString())
		}
		return annotateQueryPlan(&localRatePlan{Expr: node.ExprString(), Func: node.Func, Child: child}, analysis.InfoFor(node)), nil
	case *logicalIncreasePlan:
		child, err := buildExecPlanWithAnalysis(node.Child, ctx, analysis)
		if err != nil {
			return nil, withInternalContext(err, "building execution child plan for increase %q", node.ExprString())
		}
		return annotateQueryPlan(&localIncreasePlan{Expr: node.ExprString(), Child: child}, analysis.InfoFor(node)), nil
	case *logicalDeltaPlan:
		child, err := buildExecPlanWithAnalysis(node.Child, ctx, analysis)
		if err != nil {
			return nil, withInternalContext(err, "building execution child plan for %s %q", node.Func, node.ExprString())
		}
		return annotateQueryPlan(&localDeltaPlan{Expr: node.ExprString(), Func: node.Func, Child: child}, analysis.InfoFor(node)), nil
	case *logicalChangesPlan:
		child, err := buildExecPlanWithAnalysis(node.Child, ctx, analysis)
		if err != nil {
			return nil, withInternalContext(err, "building execution child plan for changes %q", node.ExprString())
		}
		return annotateQueryPlan(&localChangesPlan{Expr: node.ExprString(), Child: child}, analysis.InfoFor(node)), nil
	case *logicalDerivPlan:
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
		child, err := buildExecPlanWithAnalysis(node.Child, ctx, analysis)
		if err != nil {
			return nil, withInternalContext(err, "building execution child plan for absent %q", node.ExprString())
		}
		return annotateQueryPlan(&localAbsentPlan{Expr: node.ExprString(), OutputMetric: model.CloneMetric(node.OutputMetric), Child: child}, analysis.InfoFor(node)), nil
	case *logicalAbsentOverTimePlan:
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

func maybeBuildNativeAggregationPlan(node *logicalAggregationPlan, ctx planContext, analysis *nativeplan.Analysis) (queryPlan, bool) {
	decision := decideNativeAggregationPushdownFromAnalysis(node, analysis, ctx)
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

func toExecEvalMode(mode evalMode) exec.EvalMode {
	if mode == evalModeRange {
		return exec.EvalModeRange
	}
	return exec.EvalModeInstant
}
