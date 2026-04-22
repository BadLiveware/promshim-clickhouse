package exec

import (
	"sort"

	"ch-observability/internal/promshim/model"
)

func ApplyDoubleExponentialSmoothing(sf, tf float64, input model.RuntimeValue) (model.VectorValue, error) {
	if sf <= 0 || sf >= 1 {
		return model.VectorValue{}, badDataf("smoothing factor must be between 0 and 1 exclusive")
	}
	if tf <= 0 || tf >= 1 {
		return model.VectorValue{}, badDataf("trend factor must be between 0 and 1 exclusive")
	}
	matrix, ok := input.(model.MatrixValue)
	if !ok {
		return model.VectorValue{}, unsupportedf("double_exponential_smoothing requires matrix input, got %T", input)
	}
	out := make([]model.InstantSample, 0, len(matrix.Series))
	for _, series := range matrix.Series {
		if len(series.Values) < 2 {
			continue
		}
		s1 := series.Values[1].Value
		b := series.Values[1].Value - series.Values[0].Value
		for i := 2; i < len(series.Values); i++ {
			v := series.Values[i].Value
			newS1 := sf*v + (1-sf)*(s1+b)
			b = tf*(newS1-s1) + (1-tf)*b
			s1 = newS1
		}
		last := series.Values[len(series.Values)-1]
		out = append(out, model.InstantSample{Metric: model.DropMetricName(series.Metric), Timestamp: last.Timestamp, Value: s1})
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
