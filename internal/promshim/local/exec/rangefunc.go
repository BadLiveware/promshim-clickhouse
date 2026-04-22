package exec

import (
	"math"
	"sort"

	"github.com/BadLiveware/promshim-ch/internal/promshim/model"
)

type matrixRangeFunction func(model.MatrixValue) (model.VectorValue, error)

var localMatrixRangeFunctions = map[string]matrixRangeFunction{
	"last_over_time":        applyLastOverTimeMatrix,
	"first_over_time":       applyFirstOverTimeMatrix,
	"sum_over_time":         applySumOverTimeMatrix,
	"avg_over_time":         applyAvgOverTimeMatrix,
	"max_over_time":         applyMaxOverTimeMatrix,
	"min_over_time":         applyMinOverTimeMatrix,
	"count_over_time":       applyCountOverTimeMatrix,
	"stddev_over_time":      applyStddevOverTimeMatrix,
	"stdvar_over_time":      applyStdvarOverTimeMatrix,
	"present_over_time":     applyPresentOverTimeMatrix,
	"mad_over_time":         applyMadOverTimeMatrix,
	"resets":                applyResetsMatrix,
	"ts_of_first_over_time": applyTsOfFirstOverTimeMatrix,
	"ts_of_last_over_time":  applyTsOfLastOverTimeMatrix,
	"ts_of_max_over_time":   applyTsOfMaxOverTimeMatrix,
	"ts_of_min_over_time":   applyTsOfMinOverTimeMatrix,
}

func ApplyRangeFunctionInstant(name string, input model.RuntimeValue) (model.VectorValue, error) {
	fn, ok := localMatrixRangeFunctions[name]
	if !ok {
		return model.VectorValue{}, unsupportedf("range function %q is not implemented yet", name)
	}
	matrix, ok := input.(model.MatrixValue)
	if !ok {
		return model.VectorValue{}, unsupportedf("%s currently requires matrix input, got %T", name, input)
	}
	return fn(matrix)
}

func ApplyLastOverTimeInstant(input model.RuntimeValue) (model.VectorValue, error) {
	return ApplyRangeFunctionInstant("last_over_time", input)
}

func ApplyFirstOverTimeInstant(input model.RuntimeValue) (model.VectorValue, error) {
	return ApplyRangeFunctionInstant("first_over_time", input)
}

func ApplyQuantileOverTime(quantile float64, input model.RuntimeValue) (model.VectorValue, error) {
	matrix, ok := input.(model.MatrixValue)
	if !ok {
		return model.VectorValue{}, unsupportedf("quantile_over_time requires matrix input, got %T", input)
	}
	return model.VectorValue{Samples: applyQuantileOverTimeMatrix(quantile, matrix)}, nil
}

func applySumOverTimeMatrix(matrix model.MatrixValue) (model.VectorValue, error) {
	out := make([]model.InstantSample, 0, len(matrix.Series))
	for _, series := range matrix.Series {
		if len(series.Values) == 0 {
			continue
		}
		sum := 0.0
		hasNaN := false
		for _, point := range series.Values {
			if math.IsNaN(point.Value) {
				hasNaN = true
				continue
			}
			sum += point.Value
		}
		last := series.Values[len(series.Values)-1]
		if hasNaN {
			sum = math.NaN()
		}
		out = append(out, model.InstantSample{Metric: model.DropMetricName(series.Metric), Timestamp: last.Timestamp, Value: sum})
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

func applyAvgOverTimeMatrix(matrix model.MatrixValue) (model.VectorValue, error) {
	out := make([]model.InstantSample, 0, len(matrix.Series))
	for _, series := range matrix.Series {
		if len(series.Values) == 0 {
			continue
		}
		sum := 0.0
		count := 0
		hasNaN := false
		for _, point := range series.Values {
			if math.IsNaN(point.Value) {
				hasNaN = true
				continue
			}
			sum += point.Value
			count++
		}
		last := series.Values[len(series.Values)-1]
		value := math.NaN()
		if count > 0 {
			value = sum / float64(count)
		}
		if hasNaN {
			value = math.NaN()
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

func applyCountOverTimeMatrix(matrix model.MatrixValue) (model.VectorValue, error) {
	out := make([]model.InstantSample, 0, len(matrix.Series))
	for _, series := range matrix.Series {
		count := float64(len(series.Values))
		if len(series.Values) == 0 {
			continue
		}
		last := series.Values[len(series.Values)-1]
		out = append(out, model.InstantSample{Metric: model.DropMetricName(series.Metric), Timestamp: last.Timestamp, Value: count})
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

func applyStddevOverTimeMatrix(matrix model.MatrixValue) (model.VectorValue, error) {
	return applyVarianceOverTimeMatrix(matrix, true), nil
}

func applyStdvarOverTimeMatrix(matrix model.MatrixValue) (model.VectorValue, error) {
	return applyVarianceOverTimeMatrix(matrix, false), nil
}

func applyPresentOverTimeMatrix(matrix model.MatrixValue) (model.VectorValue, error) {
	out := make([]model.InstantSample, 0, len(matrix.Series))
	for _, series := range matrix.Series {
		if len(series.Values) == 0 {
			continue
		}
		last := series.Values[len(series.Values)-1]
		out = append(out, model.InstantSample{Metric: model.DropMetricName(series.Metric), Timestamp: last.Timestamp, Value: 1})
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

func applyMadOverTimeMatrix(matrix model.MatrixValue) (model.VectorValue, error) {
	out := make([]model.InstantSample, 0, len(matrix.Series))
	for _, series := range matrix.Series {
		if len(series.Values) == 0 {
			continue
		}
		values := make([]float64, 0, len(series.Values))
		for _, point := range series.Values {
			values = append(values, point.Value)
		}
		median := calculateQuantileFromValues(0.5, values)
		deviations := make([]float64, 0, len(values))
		for _, value := range values {
			deviations = append(deviations, math.Abs(value-median))
		}
		mad := calculateQuantileFromValues(0.5, deviations)
		last := series.Values[len(series.Values)-1]
		out = append(out, model.InstantSample{Metric: model.DropMetricName(series.Metric), Timestamp: last.Timestamp, Value: mad})
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

func applyResetsMatrix(matrix model.MatrixValue) (model.VectorValue, error) {
	out := make([]model.InstantSample, 0, len(matrix.Series))
	for _, series := range matrix.Series {
		if len(series.Values) == 0 {
			continue
		}
		resets := 0.0
		prev := math.NaN()
		for _, point := range series.Values {
			if math.IsNaN(point.Value) {
				continue
			}
			if !math.IsNaN(prev) && point.Value < prev {
				resets++
			}
			prev = point.Value
		}
		last := series.Values[len(series.Values)-1]
		out = append(out, model.InstantSample{Metric: model.DropMetricName(series.Metric), Timestamp: last.Timestamp, Value: resets})
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

func applyVarianceOverTimeMatrix(matrix model.MatrixValue, sqrt bool) model.VectorValue {
	out := make([]model.InstantSample, 0, len(matrix.Series))
	for _, series := range matrix.Series {
		if len(series.Values) == 0 {
			continue
		}
		count := 0.0
		mean := 0.0
		m2 := 0.0
		for _, point := range series.Values {
			count++
			delta := point.Value - mean
			mean += delta / count
			m2 += delta * (point.Value - mean)
		}
		value := m2 / count
		if sqrt {
			value = math.Sqrt(value)
		}
		last := series.Values[len(series.Values)-1]
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
	return model.VectorValue{Samples: out}
}

func applyMaxOverTimeMatrix(matrix model.MatrixValue) (model.VectorValue, error) {
	out := make([]model.InstantSample, 0, len(matrix.Series))
	for _, series := range matrix.Series {
		if len(series.Values) == 0 {
			continue
		}
		max := math.Inf(-1)
		hasFinite := false
		hasNaN := false
		for _, point := range series.Values {
			if math.IsNaN(point.Value) {
				hasNaN = true
				continue
			}
			if point.Value > max {
				max = point.Value
			}
			hasFinite = true
		}
		last := series.Values[len(series.Values)-1]
		value := math.NaN()
		if hasFinite {
			value = max
		}
		if hasNaN {
			value = math.NaN()
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

func applyMinOverTimeMatrix(matrix model.MatrixValue) (model.VectorValue, error) {
	out := make([]model.InstantSample, 0, len(matrix.Series))
	for _, series := range matrix.Series {
		if len(series.Values) == 0 {
			continue
		}
		min := math.Inf(1)
		hasFinite := false
		hasNaN := false
		for _, point := range series.Values {
			if math.IsNaN(point.Value) {
				hasNaN = true
				continue
			}
			if point.Value < min {
				min = point.Value
			}
			hasFinite = true
		}
		last := series.Values[len(series.Values)-1]
		value := math.NaN()
		if hasFinite {
			value = min
		}
		if hasNaN {
			value = math.NaN()
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

func applyLastOverTimeMatrix(matrix model.MatrixValue) (model.VectorValue, error) {
	out := make([]model.InstantSample, 0, len(matrix.Series))
	for _, series := range matrix.Series {
		if len(series.Values) == 0 {
			continue
		}
		last := series.Values[len(series.Values)-1]
		out = append(out, model.InstantSample{Metric: model.CloneMetric(series.Metric), Timestamp: last.Timestamp, Value: last.Value})
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

func applyFirstOverTimeMatrix(matrix model.MatrixValue) (model.VectorValue, error) {
	out := make([]model.InstantSample, 0, len(matrix.Series))
	for _, series := range matrix.Series {
		if len(series.Values) == 0 {
			continue
		}
		first := series.Values[0]
		out = append(out, model.InstantSample{Metric: model.CloneMetric(series.Metric), Timestamp: first.Timestamp, Value: first.Value})
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

func applyTsOfFirstOverTimeMatrix(matrix model.MatrixValue) (model.VectorValue, error) {
	out := make([]model.InstantSample, 0, len(matrix.Series))
	for _, series := range matrix.Series {
		if len(series.Values) == 0 {
			continue
		}
		first := series.Values[0]
		out = append(out, model.InstantSample{Metric: model.DropMetricName(series.Metric), Timestamp: first.Timestamp, Value: first.Timestamp})
	}
	return sortVectorSamples(out), nil
}

func applyTsOfLastOverTimeMatrix(matrix model.MatrixValue) (model.VectorValue, error) {
	out := make([]model.InstantSample, 0, len(matrix.Series))
	for _, series := range matrix.Series {
		if len(series.Values) == 0 {
			continue
		}
		last := series.Values[len(series.Values)-1]
		out = append(out, model.InstantSample{Metric: model.DropMetricName(series.Metric), Timestamp: last.Timestamp, Value: last.Timestamp})
	}
	return sortVectorSamples(out), nil
}

func applyTsOfMaxOverTimeMatrix(matrix model.MatrixValue) (model.VectorValue, error) {
	out := make([]model.InstantSample, 0, len(matrix.Series))
	for _, series := range matrix.Series {
		if len(series.Values) == 0 {
			continue
		}
		best := series.Values[0]
		for _, point := range series.Values[1:] {
			if point.Value >= best.Value || math.IsNaN(best.Value) {
				best = point
			}
		}
		out = append(out, model.InstantSample{Metric: model.DropMetricName(series.Metric), Timestamp: best.Timestamp, Value: best.Timestamp})
	}
	return sortVectorSamples(out), nil
}

func applyTsOfMinOverTimeMatrix(matrix model.MatrixValue) (model.VectorValue, error) {
	out := make([]model.InstantSample, 0, len(matrix.Series))
	for _, series := range matrix.Series {
		if len(series.Values) == 0 {
			continue
		}
		best := series.Values[0]
		for _, point := range series.Values[1:] {
			if point.Value <= best.Value || math.IsNaN(best.Value) {
				best = point
			}
		}
		out = append(out, model.InstantSample{Metric: model.DropMetricName(series.Metric), Timestamp: best.Timestamp, Value: best.Timestamp})
	}
	return sortVectorSamples(out), nil
}

func applyQuantileOverTimeMatrix(quantile float64, matrix model.MatrixValue) []model.InstantSample {
	out := make([]model.InstantSample, 0, len(matrix.Series))
	for _, series := range matrix.Series {
		if len(series.Values) == 0 {
			continue
		}
		values := make([]float64, 0, len(series.Values))
		for _, point := range series.Values {
			values = append(values, point.Value)
		}
		quantileValue := calculateQuantileFromValues(quantile, values)
		last := series.Values[len(series.Values)-1]
		out = append(out, model.InstantSample{Metric: model.DropMetricName(series.Metric), Timestamp: last.Timestamp, Value: quantileValue})
	}
	sort.Slice(out, func(i, j int) bool {
		left := model.LabelsKey(out[i].Metric)
		right := model.LabelsKey(out[j].Metric)
		if left == right {
			return out[i].Timestamp < out[j].Timestamp
		}
		return left < right
	})
	return out
}

func calculateQuantileFromValues(quantile float64, values []float64) float64 {
	if len(values) == 0 {
		return math.NaN()
	}
	if math.IsNaN(quantile) {
		return math.NaN()
	}
	if quantile < 0 {
		return math.Inf(-1)
	}
	if quantile > 1 {
		return math.Inf(+1)
	}

	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)

	n := float64(len(sorted))
	rank := quantile * (n - 1)
	lower := math.Floor(rank)
	upper := math.Min(n-1, lower+1)
	weight := rank - lower
	lowerIndex := int(lower)
	upperIndex := int(math.Floor(upper))
	if lowerIndex < 0 {
		lowerIndex = 0
	}
	if lowerIndex >= len(sorted) {
		lowerIndex = len(sorted) - 1
	}
	if upperIndex < 0 {
		upperIndex = 0
	}
	if upperIndex >= len(sorted) {
		upperIndex = len(sorted) - 1
	}
	return sorted[lowerIndex]*(1-weight) + sorted[upperIndex]*weight
}
