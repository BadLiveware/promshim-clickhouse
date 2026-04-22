package local

import (
	"context"
	"sort"
	"time"

	"github.com/BadLiveware/promshim-ch/internal/promshim/local/exec"
	"github.com/BadLiveware/promshim-ch/internal/promshim/model"
	planpkg "github.com/BadLiveware/promshim-ch/internal/promshim/plan"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

type localInfoPlan struct {
	Expr             string
	SelectorMatchers []*labels.Matcher
	Child            Plan
}

func (p *localInfoPlan) execute(ctx context.Context, Evaluator *Evaluator, params EvalParams) (model.RuntimeValue, error) {
	switch params.Mode {
	case EvalModeInstant:
		childValue, err := p.Child.execute(ctx, Evaluator, params)
		if err != nil {
			return nil, WithInternalContext(err, "evaluating info child in instant mode")
		}
		base, ok := childValue.(model.VectorValue)
		if !ok {
			return nil, NewExecutionErrorf("info child returned %T, expected vector", childValue)
		}
		infoVector, err := Evaluator.fetchInfoVector(ctx, base, p.SelectorMatchers, params)
		if err != nil {
			return nil, WithInternalContext(err, "fetching info series for %q", p.Expr)
		}
		result, err := exec.ApplyInfo(base, infoVector, p.SelectorMatchers)
		if err != nil {
			return nil, WithInternalContext(FromExecError(err), "applying info()")
		}
		return result, nil
	case EvalModeRange:
		return executeRangeVectorPlan(ctx, Evaluator, params, "info", p.execute)
	default:
		return nil, NewExecutionErrorf("unknown evaluation mode %q", params.Mode)
	}
}

func (p *localInfoPlan) explain() ExplainNode {
	return ExplainNode{Kind: "info", Strategy: "local", Expr: p.Expr, Children: []ExplainNode{p.Child.explain()}}
}

func (e *Evaluator) fetchInfoVector(ctx context.Context, base model.VectorValue, selectorMatchers []*labels.Matcher, params EvalParams) (model.VectorValue, error) {
	query, err := exec.BuildInfoFetchExprString(base, selectorMatchers)
	if err != nil {
		return model.VectorValue{}, WithInternalContext(err, "building info fetch selector")
	}
	if query == "" {
		return model.VectorValue{}, nil
	}
	expr, err := planpkg.ParseExpression(query)
	if err != nil {
		return model.VectorValue{}, WithInternalContext(err, "parsing info fetch selector %q", query)
	}
	value, err := e.executeDelegated(ctx, expr, EvalParams{Mode: EvalModeInstant, EvaluationTime: params.EvaluationTime})
	if err != nil {
		return model.VectorValue{}, err
	}
	vector, ok := value.(model.VectorValue)
	if !ok {
		return model.VectorValue{}, NewExecutionErrorf("info fetch returned %T, expected vector", value)
	}
	return vector, nil
}

type localAbsentPlan struct {
	Expr         string
	OutputMetric map[string]string
	Child        Plan
}

func (p *localAbsentPlan) execute(ctx context.Context, Evaluator *Evaluator, params EvalParams) (model.RuntimeValue, error) {
	switch params.Mode {
	case EvalModeInstant:
		childValue, err := p.Child.execute(ctx, Evaluator, params)
		if err != nil {
			return nil, WithInternalContext(err, "evaluating absent child in instant mode")
		}
		vector, err := exec.ApplyAbsent(childValue, p.OutputMetric, float64(params.EvaluationTime.UnixNano())/float64(time.Second))
		if err != nil {
			return nil, WithInternalContext(FromExecError(err), "applying absent in instant mode")
		}
		return vector, nil
	case EvalModeRange:
		return executeRangeVectorPlan(ctx, Evaluator, params, "absent", p.execute)
	default:
		return nil, NewExecutionErrorf("unknown evaluation mode %q", params.Mode)
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
	Child              Plan
}

func (p *localAbsentOverTimePlan) execute(ctx context.Context, Evaluator *Evaluator, params EvalParams) (model.RuntimeValue, error) {
	switch params.Mode {
	case EvalModeInstant:
		childValue, err := p.Child.execute(ctx, Evaluator, params)
		if err != nil {
			return nil, WithInternalContext(err, "evaluating absent_over_time child in instant mode")
		}
		vector, err := exec.ApplyAbsentOverTime(childValue, p.OutputMetric, float64(params.EvaluationTime.UnixNano())/float64(time.Second))
		if err != nil {
			return nil, WithInternalContext(FromExecError(err), "applying absent_over_time in instant mode")
		}
		if len(vector.Samples) > 0 && p.BoundaryProbeExpr != nil && p.BoundaryProbeRange > 0 {
			boundaryTime := params.EvaluationTime.Add(-p.BoundaryProbeRange)
			boundaryValue, err := Evaluator.executeDelegated(ctx, p.BoundaryProbeExpr, EvalParams{Mode: EvalModeInstant, EvaluationTime: boundaryTime})
			if err != nil {
				return nil, WithInternalContext(err, "probing absent_over_time left boundary at %s", boundaryTime.UTC().Format(time.RFC3339Nano))
			}
			probeMatrix, ok := boundaryValue.(model.MatrixValue)
			if !ok {
				return nil, NewExecutionErrorf("absent_over_time boundary probe returned %T, expected matrix", boundaryValue)
			}
			for _, series := range probeMatrix.Series {
				if len(series.Values) > 0 {
					return model.VectorValue{}, nil
				}
			}
		}
		return vector, nil
	case EvalModeRange:
		return executeRangeVectorPlan(ctx, Evaluator, params, "absent_over_time", p.execute)
	default:
		return nil, NewExecutionErrorf("unknown evaluation mode %q", params.Mode)
	}
}

func (p *localAbsentOverTimePlan) explain() ExplainNode {
	return ExplainNode{Kind: "absent_over_time", Strategy: "local", Expr: p.Expr, Children: []ExplainNode{p.Child.explain()}}
}

type localSubqueryPlan struct {
	Expr                    parser.Expr
	Range                   time.Duration
	Step                    time.Duration
	Offset                  time.Duration
	Timestamp               *int64
	StartOrEnd              parser.ItemType
	DelegatedLeafCompatible bool
	Child                   Plan
}

func (p *localSubqueryPlan) execute(ctx context.Context, Evaluator *Evaluator, params EvalParams) (model.RuntimeValue, error) {
	useDelegatedPath := p.DelegatedLeafCompatible && !(params.Mode == EvalModeInstant && p.Expr != nil && p.Expr.Type() == parser.ValueTypeMatrix)
	if useDelegatedPath {
		value, err := Evaluator.executeDelegated(ctx, p.Expr, params)
		if err != nil {
			return nil, WithInternalContext(err, "executing delegated subquery expression %q", p.Expr.String())
		}
		return value, nil
	}

	start, end, step, err := p.executionWindow(params)
	if err != nil {
		return nil, WithInternalContext(err, "preparing local subquery window for %q", p.Expr.String())
	}
	seriesByKey := map[string]*model.RangeSeries{}
	seriesOrder := make([]string, 0)

	for ts := start; !ts.After(end); ts = ts.Add(step) {
		childValue, err := p.Child.execute(ctx, Evaluator, EvalParams{Mode: EvalModeInstant, EvaluationTime: ts})
		if err != nil {
			return nil, WithInternalContext(err, "evaluating local subquery child at %s for %q", ts.UTC().Format(time.RFC3339Nano), p.Expr.String())
		}
		vector, ok := childValue.(model.VectorValue)
		if !ok {
			return nil, NewExecutionErrorf("subquery child must evaluate to vector at %s for %q, got %T", ts.UTC().Format(time.RFC3339Nano), p.Expr.String(), childValue)
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

func (p *localSubqueryPlan) executionWindow(params EvalParams) (time.Time, time.Time, time.Duration, error) {
	if p.Range <= 0 {
		return time.Time{}, time.Time{}, 0, NewBadDataErrorf("subquery range must be greater than zero in %q", p.Expr.String())
	}
	step := p.Step
	if step <= 0 {
		step = defaultSubqueryStep(params)
	}
	if step <= 0 {
		return time.Time{}, time.Time{}, 0, NewBadDataErrorf("subquery step must be greater than zero in %q", p.Expr.String())
	}

	end := params.EvaluationTime
	if params.Mode == EvalModeRange {
		end = params.End
	}
	if p.Timestamp != nil {
		end = time.UnixMilli(*p.Timestamp).UTC()
	} else if resolved := resolveStartEndMillis(p.StartOrEnd, params); resolved != nil {
		end = time.UnixMilli(*resolved).UTC()
	}
	if p.Offset != 0 {
		end = end.Add(-p.Offset)
	}

	windowStart := end.Add(-p.Range)
	if params.Mode == EvalModeRange && p.Timestamp == nil && p.StartOrEnd == 0 {
		windowStart = params.Start.Add(-p.Offset).Add(-p.Range)
	}
	startMS := alignLocalSubqueryStepStart(windowStart.UnixMilli(), step.Milliseconds())
	return time.UnixMilli(startMS).UTC(), end, step, nil
}

func alignLocalSubqueryStepStart(windowStartMS, stepMS int64) int64 {
	if stepMS <= 0 {
		return windowStartMS
	}
	alignedStartMS := (windowStartMS / stepMS) * stepMS
	if alignedStartMS < windowStartMS {
		alignedStartMS += stepMS
	}
	return alignedStartMS
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
	Child  Plan
}

func (p *localLabelReplacePlan) execute(ctx context.Context, Evaluator *Evaluator, params EvalParams) (model.RuntimeValue, error) {
	childValue, err := p.Child.execute(ctx, Evaluator, params)
	if err != nil {
		return nil, WithInternalContext(err, "evaluating label_replace child dst=%q src=%q", p.Config.Dst, p.Config.Src)
	}
	result, err := exec.ApplyLabelReplaceRuntimeValue(childValue, p.Config)
	if err != nil {
		return nil, WithInternalContext(FromExecError(err), "applying label_replace dst=%q src=%q", p.Config.Dst, p.Config.Src)
	}
	return result, nil
}

func (p *localLabelReplacePlan) explain() ExplainNode {
	return ExplainNode{Kind: "label_replace", Strategy: "local", Expr: p.Expr, Children: []ExplainNode{p.Child.explain()}}
}

type localLabelJoinPlan struct {
	Expr   string
	Config model.LabelJoinConfig
	Child  Plan
}

func (p *localLabelJoinPlan) execute(ctx context.Context, Evaluator *Evaluator, params EvalParams) (model.RuntimeValue, error) {
	childValue, err := p.Child.execute(ctx, Evaluator, params)
	if err != nil {
		return nil, WithInternalContext(err, "evaluating label_join child dst=%q src=%v", p.Config.Dst, p.Config.SrcLabels)
	}
	result, err := exec.ApplyLabelJoinRuntimeValue(childValue, p.Config)
	if err != nil {
		return nil, WithInternalContext(FromExecError(err), "applying label_join dst=%q src=%v", p.Config.Dst, p.Config.SrcLabels)
	}
	return result, nil
}

func (p *localLabelJoinPlan) explain() ExplainNode {
	return ExplainNode{Kind: "label_join", Strategy: "local", Expr: p.Expr, Children: []ExplainNode{p.Child.explain()}}
}
