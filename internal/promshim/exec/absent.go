package exec

import "github.com/BadLiveware/promshim-ch/internal/promshim/model"

func ApplyAbsent(input model.RuntimeValue, outputMetric map[string]string, timestamp float64) (model.VectorValue, error) {
	vector, ok := input.(model.VectorValue)
	if !ok {
		return model.VectorValue{}, unsupportedf("absent currently requires vector input, got %T", input)
	}
	if len(vector.Samples) > 0 {
		return model.VectorValue{}, nil
	}
	return model.VectorValue{Samples: []model.InstantSample{{Metric: model.CloneMetric(outputMetric), Timestamp: timestamp, Value: 1}}}, nil
}

func ApplyAbsentOverTime(input model.RuntimeValue, outputMetric map[string]string, timestamp float64) (model.VectorValue, error) {
	matrix, ok := input.(model.MatrixValue)
	if !ok {
		return model.VectorValue{}, unsupportedf("absent_over_time currently requires matrix input, got %T", input)
	}
	for _, series := range matrix.Series {
		if len(series.Values) > 0 {
			return model.VectorValue{}, nil
		}
	}
	return model.VectorValue{Samples: []model.InstantSample{{Metric: model.CloneMetric(outputMetric), Timestamp: timestamp, Value: 1}}}, nil
}
