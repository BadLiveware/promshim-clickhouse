package exec

import (
	"math"
	"sort"

	"github.com/BadLiveware/promshim-ch/internal/promshim/model"
)

func ApplyDeriv(input model.RuntimeValue) (model.VectorValue, error) {
	matrix, ok := input.(model.MatrixValue)
	if !ok {
		return model.VectorValue{}, unsupportedf("deriv requires matrix input, got %T", input)
	}
	return applyDerivMatrix(matrix)
}

func applyDerivMatrix(matrix model.MatrixValue) (model.VectorValue, error) {
	out := make([]model.InstantSample, 0, len(matrix.Series))
	for _, series := range matrix.Series {
		if len(series.Values) < 2 {
			continue
		}

		hasNaN := false
		sumX := 0.0
		sumY := 0.0
		sumXY := 0.0
		sumX2 := 0.0
		for _, point := range series.Values {
			if math.IsNaN(point.Value) {
				hasNaN = true
				break
			}
			x := point.Timestamp
			y := point.Value
			sumX += x
			sumY += y
			sumXY += x * y
			sumX2 += x * x
		}

		last := series.Values[len(series.Values)-1]
		value := math.NaN()
		if !hasNaN {
			n := float64(len(series.Values))
			denom := n*sumX2 - sumX*sumX
			if denom != 0 {
				value = (n*sumXY - sumX*sumY) / denom
			}
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
