package exec

import (
	"math"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/model"
)

func ApplyIncreaseInstant(input model.RuntimeValue) (model.VectorValue, error) {
	matrix, ok := input.(model.MatrixValue)
	if !ok {
		return model.VectorValue{}, unsupportedf("increase requires matrix input, got %T", input)
	}
	return applyIncreaseMatrix(matrix)
}

func ApplyIncreaseInstantWithBounds(input model.RuntimeValue, rangeStart, rangeEnd float64) (model.VectorValue, error) {
	matrix, ok := input.(model.MatrixValue)
	if !ok {
		return model.VectorValue{}, unsupportedf("increase requires matrix input, got %T", input)
	}
	return applyExtrapolatedMatrix(matrix, rangeStart, rangeEnd, true, false)
}

func applyIncreaseMatrix(matrix model.MatrixValue) (model.VectorValue, error) {
	out := make([]model.InstantSample, 0, len(matrix.Series))
	for _, series := range matrix.Series {
		if len(series.Values) < 2 {
			continue
		}

		increase := 0.0
		hasNaN := false
		prev := series.Values[0].Value
		for _, point := range series.Values[1:] {
			current := point.Value
			if math.IsNaN(prev) || math.IsNaN(current) {
				hasNaN = true
				prev = current
				continue
			}
			if current < prev {
				increase += current
			} else {
				increase += current - prev
			}
			prev = current
		}

		if hasNaN {
			increase = math.NaN()
		}
		last := series.Values[len(series.Values)-1]
		out = append(out, model.InstantSample{Metric: model.DropMetricName(series.Metric), Timestamp: last.Timestamp, Value: increase})
	}

	sortInstantSamples(out)
	return model.VectorValue{Samples: out}, nil
}
