package local

import (
	"context"
	"sort"
	"time"

	"ch-observability/internal/promshim/local/exec"
	"ch-observability/internal/promshim/model"
)

type localRangeFunctionPlan struct {
	Expr         string
	Func         string
	ParamNumber  *float64
	ParamNumbers []*float64
	Child        Plan
}

func (p *localRangeFunctionPlan) execute(ctx context.Context, Evaluator *Evaluator, params EvalParams) (model.RuntimeValue, error) {
	switch params.Mode {
	case EvalModeInstant:
		childValue, err := p.Child.execute(ctx, Evaluator, params)
		if err != nil {
			return nil, WithInternalContext(err, "evaluating %s child in instant mode", p.Func)
		}
		var (
			vector model.VectorValue
		)
		if p.Func == "predict_linear" {
			if p.ParamNumber == nil {
				return nil, NewExecutionErrorf("predict_linear requires a duration parameter")
			}
			vector, err = exec.ApplyPredictLinear(*p.ParamNumber, childValue, exec.EvalParams{
				Mode:           toExecEvalMode(params.Mode),
				EvaluationTime: params.EvaluationTime,
				Start:          params.Start,
				End:            params.End,
				Step:           params.Step,
			})
		} else if p.Func == "double_exponential_smoothing" || p.Func == "holt_winters" {
			if len(p.ParamNumbers) != 2 || p.ParamNumbers[0] == nil || p.ParamNumbers[1] == nil {
				return nil, NewExecutionErrorf("%s requires smoothing and trend parameters", p.Func)
			}
			vector, err = exec.ApplyDoubleExponentialSmoothing(*p.ParamNumbers[0], *p.ParamNumbers[1], childValue)
		} else {
			vector, err = exec.ApplyRangeFunctionInstant(p.Func, childValue)
		}
		if err != nil {
			return nil, WithInternalContext(FromExecError(err), "applying %s in instant mode", p.Func)
		}
		evalTimestamp := float64(params.EvaluationTime.UnixNano()) / float64(time.Second)
		for i := range vector.Samples {
			vector.Samples[i].Timestamp = evalTimestamp
		}
		return vector, nil
	case EvalModeRange:
		return executeRangeVectorPlan(ctx, Evaluator, params, p.Func, p.execute)
	default:
		return nil, NewExecutionErrorf("unknown evaluation mode %q", params.Mode)
	}
}

func (p *localRangeFunctionPlan) explain() ExplainNode {
	return ExplainNode{Kind: p.Func, Strategy: "local", Expr: p.Expr, Children: []ExplainNode{p.Child.explain()}}
}

type localRatePlan struct {
	Expr  string
	Func  string
	Child Plan
}

func (p *localRatePlan) execute(ctx context.Context, Evaluator *Evaluator, params EvalParams) (model.RuntimeValue, error) {
	switch params.Mode {
	case EvalModeInstant:
		childValue, err := p.Child.execute(ctx, Evaluator, params)
		if err != nil {
			return nil, WithInternalContext(err, "evaluating %s child in instant mode", p.Func)
		}
		var vector model.VectorValue
		switch p.Func {
		case "rate":
			vector, err = exec.ApplyRate(childValue)
		case "irate":
			vector, err = exec.ApplyIRate(childValue)
		default:
			return nil, NewExecutionErrorf("unknown local rate function %q", p.Func)
		}
		if err != nil {
			return nil, WithInternalContext(FromExecError(err), "applying %s", p.Func)
		}
		return vector, nil
	case EvalModeRange:
		return executeRangeVectorPlan(ctx, Evaluator, params, p.Func, p.execute)
	default:
		return nil, NewExecutionErrorf("unknown evaluation mode %q", params.Mode)
	}
}

func (p *localRatePlan) explain() ExplainNode {
	return ExplainNode{Kind: p.Func, Strategy: "local", Expr: p.Expr, Children: []ExplainNode{p.Child.explain()}}
}

type localIncreasePlan struct {
	Expr  string
	Child Plan
}

func (p *localIncreasePlan) execute(ctx context.Context, Evaluator *Evaluator, params EvalParams) (model.RuntimeValue, error) {
	switch params.Mode {
	case EvalModeInstant:
		childValue, err := p.Child.execute(ctx, Evaluator, params)
		if err != nil {
			return nil, WithInternalContext(err, "evaluating increase child in instant mode")
		}
		vector, err := exec.ApplyIncreaseInstant(childValue)
		if err != nil {
			return nil, WithInternalContext(FromExecError(err), "applying increase in instant mode")
		}
		return vector, nil
	case EvalModeRange:
		return executeRangeVectorPlan(ctx, Evaluator, params, "increase", p.execute)
	default:
		return nil, NewExecutionErrorf("unknown evaluation mode %q", params.Mode)
	}
}

func (p *localIncreasePlan) explain() ExplainNode {
	return ExplainNode{Kind: "increase", Strategy: "local", Expr: p.Expr, Children: []ExplainNode{p.Child.explain()}}
}

type localDeltaPlan struct {
	Expr  string
	Func  string
	Child Plan
}

func (p *localDeltaPlan) execute(ctx context.Context, Evaluator *Evaluator, params EvalParams) (model.RuntimeValue, error) {
	switch params.Mode {
	case EvalModeInstant:
		childValue, err := p.Child.execute(ctx, Evaluator, params)
		if err != nil {
			return nil, WithInternalContext(err, "evaluating %s child in instant mode", p.Func)
		}
		var vector model.VectorValue
		switch p.Func {
		case "delta":
			vector, err = exec.ApplyDelta(childValue)
		case "idelta":
			vector, err = exec.ApplyIDelta(childValue)
		default:
			return nil, NewExecutionErrorf("unknown local delta function %q", p.Func)
		}
		if err != nil {
			return nil, WithInternalContext(FromExecError(err), "applying %s", p.Func)
		}
		return vector, nil
	case EvalModeRange:
		return executeRangeVectorPlan(ctx, Evaluator, params, p.Func, p.execute)
	default:
		return nil, NewExecutionErrorf("unknown evaluation mode %q", params.Mode)
	}
}

func (p *localDeltaPlan) explain() ExplainNode {
	return ExplainNode{Kind: p.Func, Strategy: "local", Expr: p.Expr, Children: []ExplainNode{p.Child.explain()}}
}

type localChangesPlan struct {
	Expr  string
	Child Plan
}

func (p *localChangesPlan) execute(ctx context.Context, Evaluator *Evaluator, params EvalParams) (model.RuntimeValue, error) {
	switch params.Mode {
	case EvalModeInstant:
		childValue, err := p.Child.execute(ctx, Evaluator, params)
		if err != nil {
			return nil, WithInternalContext(err, "evaluating changes child in instant mode")
		}
		vector, err := exec.ApplyChanges(childValue)
		if err != nil {
			return nil, WithInternalContext(FromExecError(err), "applying changes")
		}
		return vector, nil
	case EvalModeRange:
		return executeRangeVectorPlan(ctx, Evaluator, params, "changes", p.execute)
	default:
		return nil, NewExecutionErrorf("unknown evaluation mode %q", params.Mode)
	}
}

func (p *localChangesPlan) explain() ExplainNode {
	return ExplainNode{Kind: "changes", Strategy: "local", Expr: p.Expr, Children: []ExplainNode{p.Child.explain()}}
}

type localDerivPlan struct {
	Expr  string
	Child Plan
}

func (p *localDerivPlan) execute(ctx context.Context, Evaluator *Evaluator, params EvalParams) (model.RuntimeValue, error) {
	switch params.Mode {
	case EvalModeInstant:
		childValue, err := p.Child.execute(ctx, Evaluator, params)
		if err != nil {
			return nil, WithInternalContext(err, "evaluating deriv child in instant mode")
		}
		vector, err := exec.ApplyDeriv(childValue)
		if err != nil {
			return nil, WithInternalContext(FromExecError(err), "applying deriv")
		}
		return vector, nil
	case EvalModeRange:
		return executeRangeVectorPlan(ctx, Evaluator, params, "deriv", p.execute)
	default:
		return nil, NewExecutionErrorf("unknown evaluation mode %q", params.Mode)
	}
}

func (p *localDerivPlan) explain() ExplainNode {
	return ExplainNode{Kind: "deriv", Strategy: "local", Expr: p.Expr, Children: []ExplainNode{p.Child.explain()}}
}

type localQuantileOverTimePlan struct {
	Expr     string
	Quantile float64
	Child    Plan
}

func (p *localQuantileOverTimePlan) execute(ctx context.Context, Evaluator *Evaluator, params EvalParams) (model.RuntimeValue, error) {
	switch params.Mode {
	case EvalModeInstant:
		childValue, err := p.Child.execute(ctx, Evaluator, params)
		if err != nil {
			return nil, WithInternalContext(err, "evaluating quantile_over_time child in instant mode")
		}
		vector, err := exec.ApplyQuantileOverTime(p.Quantile, childValue)
		if err != nil {
			return nil, WithInternalContext(FromExecError(err), "applying quantile_over_time in instant mode")
		}
		return vector, nil
	case EvalModeRange:
		return executeRangeVectorPlan(ctx, Evaluator, params, "quantile_over_time", p.execute)
	default:
		return nil, NewExecutionErrorf("unknown evaluation mode %q", params.Mode)
	}
}

func (p *localQuantileOverTimePlan) explain() ExplainNode {
	return ExplainNode{Kind: "quantile_over_time", Strategy: "local", Expr: p.Expr, Children: []ExplainNode{p.Child.explain()}}
}

func executeRangeVectorPlan(ctx context.Context, Evaluator *Evaluator, params EvalParams, kind string, executeInstant func(context.Context, *Evaluator, EvalParams) (model.RuntimeValue, error)) (model.RuntimeValue, error) {
	if params.Step <= 0 {
		return nil, NewBadDataErrorf("step must be greater than zero for %q", kind)
	}
	seriesByKey := map[string]*model.RangeSeries{}
	seriesOrder := make([]string, 0)
	for ts := params.Start; !ts.After(params.End); ts = ts.Add(params.Step) {
		instantValue, err := executeInstant(ctx, Evaluator, EvalParams{Mode: EvalModeInstant, EvaluationTime: ts})
		if err != nil {
			return nil, WithInternalContext(err, "evaluating %s at range step %s", kind, ts.UTC().Format(time.RFC3339Nano))
		}
		vector, ok := instantValue.(model.VectorValue)
		if !ok {
			return nil, NewExecutionErrorf("%s instant step returned %T, expected vector", kind, instantValue)
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

func executeRangeScalarPlan(ctx context.Context, Evaluator *Evaluator, params EvalParams, kind string, executeInstant func(context.Context, *Evaluator, EvalParams) (model.RuntimeValue, error)) (model.RuntimeValue, error) {
	if params.Step <= 0 {
		return nil, NewBadDataErrorf("step must be greater than zero for %q", kind)
	}
	series := model.RangeSeries{Metric: map[string]string{}, Values: make([]model.RangePoint, 0, 16)}
	for ts := params.Start; !ts.After(params.End); ts = ts.Add(params.Step) {
		instantValue, err := executeInstant(ctx, Evaluator, EvalParams{Mode: EvalModeInstant, EvaluationTime: ts})
		if err != nil {
			return nil, WithInternalContext(err, "evaluating %s at range step %s", kind, ts.UTC().Format(time.RFC3339Nano))
		}
		scalar, ok := instantValue.(model.ScalarValue)
		if !ok {
			return nil, NewExecutionErrorf("%s instant step returned %T, expected scalar", kind, instantValue)
		}
		series.Values = append(series.Values, model.RangePoint{Timestamp: float64(ts.UnixNano()) / float64(time.Second), Value: scalar.Value})
	}
	return model.MatrixValue{Series: []model.RangeSeries{series}}, nil
}
