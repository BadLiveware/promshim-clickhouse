package local

import (
	"context"
	"sort"
	"time"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/model"
)

type chunkedRangePlan struct {
	Child                Plan
	ChunkPointsPerSeries int64
	Reason               string
	Estimate             *planEstimate
}

func (p *chunkedRangePlan) execute(ctx context.Context, Evaluator *Evaluator, params EvalParams) (model.RuntimeValue, error) {
	if params.Mode != EvalModeRange {
		return p.Child.execute(ctx, Evaluator, params)
	}
	chunks, err := splitRangeIntoChunks(params, p.ChunkPointsPerSeries)
	if err != nil {
		return nil, WithInternalContext(err, "building range chunks")
	}
	merged := model.MatrixValue{Series: nil}
	for _, chunk := range chunks {
		value, err := p.Child.execute(ctx, Evaluator, chunk)
		if err != nil {
			return nil, WithInternalContext(err, "executing chunked range subquery start=%s end=%s", chunk.Start, chunk.End)
		}
		matrix, ok := value.(model.MatrixValue)
		if !ok {
			return nil, NewExecutionErrorf("chunked range execution requires matrix child results, got %T", value)
		}
		merged, err = mergeMatrixValues(merged, matrix)
		if err != nil {
			return nil, WithInternalContext(err, "merging chunked range subquery start=%s end=%s", chunk.Start, chunk.End)
		}
	}
	return merged, nil
}

func (p *chunkedRangePlan) explain() ExplainNode {
	child := p.Child.explain()
	strategy := "chunked_local"
	if _, ok := p.Child.(*nativeSubtreePlan); ok {
		strategy = "chunked_native"
	}
	return ExplainNode{
		Kind:                 "range_chunk",
		Strategy:             strategy,
		Reason:               p.Reason,
		Estimate:             p.Estimate,
		ChunkPointsPerSeries: p.ChunkPointsPerSeries,
		Children:             []ExplainNode{child},
	}
}

func splitRangeIntoChunks(params EvalParams, chunkPointsPerSeries int64) ([]EvalParams, error) {
	if params.Mode != EvalModeRange {
		return []EvalParams{params}, nil
	}
	if params.Step <= 0 {
		return nil, NewBadDataErrorf("step must be greater than zero for chunked range evaluation")
	}
	if chunkPointsPerSeries <= 0 {
		return nil, NewBadDataErrorf("chunk points per series must be greater than zero for chunked range evaluation")
	}
	chunks := make([]EvalParams, 0)
	currentStart := params.Start
	for !currentStart.After(params.End) {
		remainingPoints := int64(params.End.Sub(currentStart)/params.Step) + 1
		pointsThisChunk := chunkPointsPerSeries
		if remainingPoints < pointsThisChunk {
			pointsThisChunk = remainingPoints
		}
		currentEnd := currentStart.Add(params.Step * time.Duration(pointsThisChunk-1))
		if currentEnd.After(params.End) {
			currentEnd = params.End
		}
		chunks = append(chunks, EvalParams{
			Mode:           params.Mode,
			EvaluationTime: params.EvaluationTime,
			Start:          currentStart,
			End:            currentEnd,
			Step:           params.Step,
		})
		currentStart = currentEnd.Add(params.Step)
	}
	return chunks, nil
}

func mergeMatrixValues(left, right model.MatrixValue) (model.MatrixValue, error) {
	if len(left.Series) == 0 {
		return model.MatrixValue{Series: model.CloneSeries(right.Series)}, nil
	}
	merged := make(map[string]model.RangeSeries, len(left.Series)+len(right.Series))
	for _, series := range left.Series {
		merged[model.LabelsKey(series.Metric)] = model.RangeSeries{Metric: model.CloneMetric(series.Metric), Values: model.CloneRangePoints(series.Values)}
	}
	for _, series := range right.Series {
		key := model.LabelsKey(series.Metric)
		if existing, ok := merged[key]; ok {
			values, err := model.AppendRangePointsStrict(existing.Values, series.Values)
			if err != nil {
				return model.MatrixValue{}, NewExecutionErrorf("chunked range merge encountered non-increasing timestamps for labelset %q", key)
			}
			existing.Values = values
			merged[key] = existing
		} else {
			merged[key] = model.RangeSeries{Metric: model.CloneMetric(series.Metric), Values: model.CloneRangePoints(series.Values)}
		}
	}
	keys := make([]string, 0, len(merged))
	for key := range merged {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]model.RangeSeries, 0, len(keys))
	for _, key := range keys {
		result = append(result, merged[key])
	}
	return model.MatrixValue{Series: result}, nil
}
