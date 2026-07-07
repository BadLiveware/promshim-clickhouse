package exec

import (
	"math"
	"sort"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/model"
)

func ApplyDelta(input model.RuntimeValue) (model.VectorValue, error) {
	matrix, ok := input.(model.MatrixValue)
	if !ok {
		return model.VectorValue{}, unsupportedf("delta requires matrix input, got %T", input)
	}
	return applyDeltaMatrix(matrix)
}

// ApplyDeltaWithBounds applies Prometheus's extrapolating delta
// (extrapolatedRate with isCounter=false, isRate=false): the raw
// last-first difference is extrapolated to the [rangeStart, rangeEnd]
// window boundaries.
func ApplyDeltaWithBounds(input model.RuntimeValue, rangeStart, rangeEnd float64) (model.VectorValue, error) {
	matrix, ok := input.(model.MatrixValue)
	if !ok {
		return model.VectorValue{}, unsupportedf("delta requires matrix input, got %T", input)
	}
	return applyExtrapolatedMatrix(matrix, rangeStart, rangeEnd, false, false)
}

func ApplyIDelta(input model.RuntimeValue) (model.VectorValue, error) {
	matrix, ok := input.(model.MatrixValue)
	if !ok {
		return model.VectorValue{}, unsupportedf("idelta requires matrix input, got %T", input)
	}
	return applyIDeltaMatrix(matrix)
}

func applyDeltaMatrix(matrix model.MatrixValue) (model.VectorValue, error) {
	out := make([]model.InstantSample, 0, len(matrix.Series))
	for _, series := range matrix.Series {
		if len(series.Values) < 2 {
			continue
		}

		first := series.Values[0]
		last := series.Values[len(series.Values)-1]
		value := math.NaN()
		if !math.IsNaN(first.Value) && !math.IsNaN(last.Value) {
			value = last.Value - first.Value
		}
		out = append(out, model.InstantSample{Metric: model.DropMetricName(series.Metric), Timestamp: last.Timestamp, Value: value})
	}
	sort.Slice(out, func(i, j int) bool {
		left := model.LabelsKey(out[i].Metric)
		right := model.LabelsKey(out[j].Metric)
		if left == right {
			return out[i].Timestamp < out[j].Timestamp
		}
		return left < right
	})
	return model.VectorValue{Samples: out}, nil
}

func applyIDeltaMatrix(matrix model.MatrixValue) (model.VectorValue, error) {
	out := make([]model.InstantSample, 0, len(matrix.Series))
	for _, series := range matrix.Series {
		if len(series.Values) < 2 {
			continue
		}

		prev := series.Values[len(series.Values)-2]
		last := series.Values[len(series.Values)-1]
		value := math.NaN()
		if !math.IsNaN(prev.Value) && !math.IsNaN(last.Value) {
			value = last.Value - prev.Value
		}
		out = append(out, model.InstantSample{Metric: model.DropMetricName(series.Metric), Timestamp: last.Timestamp, Value: value})
	}
	sort.Slice(out, func(i, j int) bool {
		left := model.LabelsKey(out[i].Metric)
		right := model.LabelsKey(out[j].Metric)
		if left == right {
			return out[i].Timestamp < out[j].Timestamp
		}
		return left < right
	})
	return model.VectorValue{Samples: out}, nil
}
