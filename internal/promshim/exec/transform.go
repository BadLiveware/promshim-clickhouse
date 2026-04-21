package exec

import (
	"math"
	"sort"

	"github.com/BadLiveware/promshim-ch/internal/promshim/model"
)

func ApplyVector(input model.RuntimeValue) (model.VectorValue, error) {
	scalar, ok := input.(model.ScalarValue)
	if !ok {
		return model.VectorValue{}, unsupportedf("vector() currently requires scalar input, got %T", input)
	}
	return model.VectorValue{Samples: []model.InstantSample{{Metric: map[string]string{}, Timestamp: scalar.Timestamp, Value: scalar.Value}}}, nil
}

func ApplyRound(input model.RuntimeValue, decimals *float64) (model.VectorValue, error) {
	vector, ok := input.(model.VectorValue)
	if !ok {
		return model.VectorValue{}, unsupportedf("round() currently requires vector input, got %T", input)
	}

	toNearest := 1.0
	if decimals != nil {
		toNearest = *decimals
	}
	if toNearest == 0 {
		return model.VectorValue{}, unsupportedf("round() requires non-zero multiplier")
	}
	if math.IsNaN(toNearest) || math.IsInf(toNearest, 0) {
		return model.VectorValue{}, unsupportedf("round() requires finite non-NaN multiplier")
	}

	out := make([]model.InstantSample, 0, len(vector.Samples))
	for _, sample := range vector.Samples {
		value := sample.Value
		if !math.IsNaN(value) && !math.IsInf(value, 0) {
			value = math.Round(value/toNearest) * toNearest
		}
		out = append(out, model.InstantSample{Metric: model.DropMetricName(sample.Metric), Timestamp: sample.Timestamp, Value: value})
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
