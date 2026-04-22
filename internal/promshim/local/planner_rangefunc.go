package promshim

import (
	"context"
	"sort"
	"time"

	"ch-observability/internal/promshim/exec"
	"ch-observability/internal/promshim/model"
)

type localRangeFunctionPlan struct {
	Expr         string
	Func         string
	ParamNumber  *float64
	ParamNumbers []*float64
	Child        queryPlan
}

func (p *localRangeFunctionPlan) execute(ctx context.Context, evaluator *evaluator, params evalParams) (model.RuntimeValue, error) {
	switch params.Mode {
	case evalModeInstant:
		childValue, err := p.Child.execute(ctx, evaluator, params)
		if err != nil {
			return nil, withInternalContext(err, "evaluating %s child in instant mode", p.Func)
		}
		var (
			vector model.VectorValue
		)
		if p.Func == "predict_linear" {
			if p.ParamNumber == nil {
				return nil, newExecutionErrorf("predict_linear requires a duration parameter")
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
				return nil, newExecutionErrorf("%s requires smoothing and trend parameters", p.Func)
			}
			vector, err = exec.ApplyDoubleExponentialSmoothing(*p.ParamNumbers[0], *p.ParamNumbers[1], childValue)
		} else {
			vector, err = exec.ApplyRangeFunctionInstant(p.Func, childValue)
		}
		if err != nil {
			return nil, withInternalContext(fromExecError(err), "applying %s in instant mode", p.Func)
		}
		evalTimestamp := float64(params.EvaluationTime.UnixNano()) / float64(time.Second)
		for i := range vector.Samples {
			vector.Samples[i].Timestamp = evalTimestamp
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

func executeRangeScalarPlan(ctx context.Context, evaluator *evaluator, params evalParams, kind string, executeInstant func(context.Context, *evaluator, evalParams) (model.RuntimeValue, error)) (model.RuntimeValue, error) {
	if params.Step <= 0 {
		return nil, newBadDataErrorf("step must be greater than zero for %q", kind)
	}
	series := model.RangeSeries{Metric: map[string]string{}, Values: make([]model.RangePoint, 0, 16)}
	for ts := params.Start; !ts.After(params.End); ts = ts.Add(params.Step) {
		instantValue, err := executeInstant(ctx, evaluator, evalParams{Mode: evalModeInstant, EvaluationTime: ts})
		if err != nil {
			return nil, withInternalContext(err, "evaluating %s at range step %s", kind, ts.UTC().Format(time.RFC3339Nano))
		}
		scalar, ok := instantValue.(model.ScalarValue)
		if !ok {
			return nil, newExecutionErrorf("%s instant step returned %T, expected scalar", kind, instantValue)
		}
		series.Values = append(series.Values, model.RangePoint{Timestamp: float64(ts.UnixNano()) / float64(time.Second), Value: scalar.Value})
	}
	return model.MatrixValue{Series: []model.RangeSeries{series}}, nil
}
