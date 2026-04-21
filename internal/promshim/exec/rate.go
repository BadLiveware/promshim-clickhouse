package exec

import (
	"math"
	"sort"

	"ch-observability/internal/promshim/model"
)

func ApplyRate(input model.RuntimeValue) (model.VectorValue, error) {
	matrix, ok := input.(model.MatrixValue)
	if !ok {
		return model.VectorValue{}, unsupportedf("rate requires matrix input, got %T", input)
	}
	return applyRateMatrix(matrix)
}

func ApplyIRate(input model.RuntimeValue) (model.VectorValue, error) {
	matrix, ok := input.(model.MatrixValue)
	if !ok {
		return model.VectorValue{}, unsupportedf("irate requires matrix input, got %T", input)
	}
	return applyIRateMatrix(matrix)
}

func applyRateMatrix(matrix model.MatrixValue) (model.VectorValue, error) {
	out := make([]model.InstantSample, 0, len(matrix.Series))
	for _, series := range matrix.Series {
		if len(series.Values) < 2 {
			continue
		}

		delta, hasNaN := increaseDelta(series.Values)
		timestamp := series.Values[len(series.Values)-1].Timestamp
		value := math.NaN()
		if !hasNaN {
			duration := series.Values[len(series.Values)-1].Timestamp - series.Values[0].Timestamp
			if duration > 0 {
				value = delta / duration
			}
		}
		out = append(out, model.InstantSample{Metric: model.DropMetricName(series.Metric), Timestamp: timestamp, Value: value})
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

func applyIRateMatrix(matrix model.MatrixValue) (model.VectorValue, error) {
	out := make([]model.InstantSample, 0, len(matrix.Series))
	for _, series := range matrix.Series {
		if len(series.Values) < 2 {
			continue
		}

		hasNaN := false
		for _, point := range series.Values {
			if math.IsNaN(point.Value) {
				hasNaN = true
				break
			}
		}
		if hasNaN {
			out = append(out, model.InstantSample{Metric: model.DropMetricName(series.Metric), Timestamp: series.Values[len(series.Values)-1].Timestamp, Value: math.NaN()})
			continue
		}

		prev, cur := lastTwoValues(series.Values)
		if !prev.ok || !cur.ok {
			continue
		}
		duration := cur.timestamp - prev.timestamp
		if duration <= 0 {
			out = append(out, model.InstantSample{Metric: model.DropMetricName(series.Metric), Timestamp: cur.timestamp, Value: math.NaN()})
			continue
		}
		delta := cur.value - prev.value
		if cur.value < prev.value {
			delta = cur.value
		}
		out = append(out, model.InstantSample{Metric: model.DropMetricName(series.Metric), Timestamp: cur.timestamp, Value: delta / duration})
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

func increaseDelta(values []model.RangePoint) (delta float64, hasNaN bool) {
	if len(values) < 2 {
		return 0, false
	}

	prev := values[0].Value
	hasNaN = false
	for _, point := range values[1:] {
		current := point.Value
		if math.IsNaN(prev) || math.IsNaN(current) {
			hasNaN = true
			prev = current
			continue
		}
		if current < prev {
			delta += current
		} else {
			delta += current - prev
		}
		prev = current
	}
	return delta, hasNaN
}

type rangePointWithMetadata struct {
	timestamp float64
	value     float64
	ok        bool
}

func lastTwoValues(values []model.RangePoint) (prev, cur rangePointWithMetadata) {
	for i := len(values) - 1; i >= 0; i-- {
		point := values[i]
		if math.IsNaN(point.Value) {
			continue
		}
		if !cur.ok {
			cur = rangePointWithMetadata{timestamp: point.Timestamp, value: point.Value, ok: true}
			continue
		}
		prev = rangePointWithMetadata{timestamp: point.Timestamp, value: point.Value, ok: true}
		return prev, cur
	}
	return rangePointWithMetadata{}, rangePointWithMetadata{}
}
