package exec

import (
	"math"
	"sort"

	"ch-observability/internal/promshim/model"
)

func ApplyChanges(input model.RuntimeValue) (model.VectorValue, error) {
	matrix, ok := input.(model.MatrixValue)
	if !ok {
		return model.VectorValue{}, unsupportedf("changes requires matrix input, got %T", input)
	}
	return applyChangesMatrix(matrix)
}

func applyChangesMatrix(matrix model.MatrixValue) (model.VectorValue, error) {
	out := make([]model.InstantSample, 0, len(matrix.Series))
	for _, series := range matrix.Series {
		if len(series.Values) == 0 {
			continue
		}

		changes := 0.0
		hasNaN := false
		prev := series.Values[0].Value
		for _, point := range series.Values[1:] {
			current := point.Value
			if math.IsNaN(prev) || math.IsNaN(current) {
				hasNaN = true
				prev = current
				continue
			}
			if current != prev {
				changes++
			}
			prev = current
		}
		if hasNaN {
			changes = math.NaN()
		}
		last := series.Values[len(series.Values)-1]
		out = append(out, model.InstantSample{Metric: model.DropMetricName(series.Metric), Timestamp: last.Timestamp, Value: changes})
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
