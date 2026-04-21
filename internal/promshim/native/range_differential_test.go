package native

import (
	"math"
	"sort"
	"testing"

	"ch-observability/internal/promshim/exec"
	"ch-observability/internal/promshim/model"
)

func TestAggregateOverTimeNativeSemanticsMatchLocalOracle(t *testing.T) {
	matrix := model.MatrixValue{Series: []model.RangeSeries{
		{Metric: map[string]string{"__name__": "up", "job": "api"}, Values: []model.RangePoint{{Timestamp: 10, Value: 1}, {Timestamp: 20, Value: 2}, {Timestamp: 30, Value: 5}}},
		{Metric: map[string]string{"__name__": "up", "job": "worker"}, Values: []model.RangePoint{{Timestamp: 10, Value: 3}, {Timestamp: 20, Value: math.NaN()}, {Timestamp: 30, Value: 9}}},
	}}

	for _, name := range []string{"last_over_time", "sum_over_time", "avg_over_time", "min_over_time", "max_over_time", "count_over_time"} {
		oracle, ok := exec.LocalRangeOracleForTest(name)
		if !ok {
			t.Fatalf("expected local oracle for %q", name)
		}
		localValue, err := oracle(matrix)
		if err != nil {
			t.Fatalf("local oracle for %q returned error: %v", name, err)
		}
		nativeValue, err := applyNativeAggregateOverTimeForTest(name, matrix)
		if err != nil {
			t.Fatalf("native semantic helper for %q returned error: %v", name, err)
		}
		assertVectorEqual(t, name, localValue, nativeValue)
	}
}

func applyNativeAggregateOverTimeForTest(name string, matrix model.MatrixValue) (model.VectorValue, error) {
	out := make([]model.InstantSample, 0, len(matrix.Series))
	for _, series := range matrix.Series {
		if len(series.Values) == 0 {
			continue
		}
		values := make([]float64, 0, len(series.Values))
		hasNaN := false
		for _, point := range series.Values {
			values = append(values, point.Value)
			if math.IsNaN(point.Value) {
				hasNaN = true
			}
		}
		last := series.Values[len(series.Values)-1]
		value := math.NaN()
		switch name {
		case "last_over_time":
			value = last.Value
		case "sum_over_time":
			if hasNaN {
				value = math.NaN()
			} else {
				value = 0
				for _, v := range values {
					value += v
				}
			}
		case "avg_over_time":
			if hasNaN || len(values) == 0 {
				value = math.NaN()
			} else {
				sum := 0.0
				for _, v := range values {
					sum += v
				}
				value = sum / float64(len(values))
			}
		case "min_over_time":
			if hasNaN || len(values) == 0 {
				value = math.NaN()
			} else {
				value = values[0]
				for _, v := range values[1:] {
					if v < value {
						value = v
					}
				}
			}
		case "max_over_time":
			if hasNaN || len(values) == 0 {
				value = math.NaN()
			} else {
				value = values[0]
				for _, v := range values[1:] {
					if v > value {
						value = v
					}
				}
			}
		case "count_over_time":
			value = float64(len(values))
		default:
			return model.VectorValue{}, nil
		}
		out = append(out, model.InstantSample{Metric: model.DropMetricName(series.Metric), Timestamp: last.Timestamp, Value: value})
	}
	return model.VectorValue{Samples: out}, nil
}

func TestRateFamilyNativeSemanticsMatchLocalOracle(t *testing.T) {
	matrix := model.MatrixValue{Series: []model.RangeSeries{
		{Metric: map[string]string{"__name__": "requests_total", "job": "api"}, Values: []model.RangePoint{{Timestamp: 10, Value: 10}, {Timestamp: 20, Value: 16}, {Timestamp: 30, Value: 4}, {Timestamp: 40, Value: 9}}},
		{Metric: map[string]string{"__name__": "requests_total", "job": "worker"}, Values: []model.RangePoint{{Timestamp: 10, Value: 3}, {Timestamp: 20, Value: math.NaN()}, {Timestamp: 30, Value: 9}}},
	}}

	for _, name := range []string{"rate", "irate"} {
		oracle, ok := exec.LocalRangeOracleForTest(name)
		if !ok {
			t.Fatalf("expected local oracle for %q", name)
		}
		localValue, err := oracle(matrix)
		if err != nil {
			t.Fatalf("local oracle for %q returned error: %v", name, err)
		}
		nativeValue, err := applyNativeRateFamilyForTest(name, matrix)
		if err != nil {
			t.Fatalf("native semantic helper for %q returned error: %v", name, err)
		}
		assertVectorEqual(t, name, localValue, nativeValue)
	}
}

func applyNativeRateFamilyForTest(name string, matrix model.MatrixValue) (model.VectorValue, error) {
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
		last := series.Values[len(series.Values)-1]
		value := math.NaN()
		switch name {
		case "rate":
			if !hasNaN {
				delta := 0.0
				for i := 1; i < len(series.Values); i++ {
					prev := series.Values[i-1].Value
					cur := series.Values[i].Value
					if cur < prev {
						delta += cur
					} else {
						delta += cur - prev
					}
				}
				duration := last.Timestamp - series.Values[0].Timestamp
				if duration > 0 {
					value = delta / duration
				}
			}
		case "irate":
			if !hasNaN {
				prev := series.Values[len(series.Values)-2]
				cur := series.Values[len(series.Values)-1]
				duration := cur.Timestamp - prev.Timestamp
				if duration > 0 {
					delta := cur.Value - prev.Value
					if cur.Value < prev.Value {
						delta = cur.Value
					}
					value = delta / duration
				}
			}
		default:
			return model.VectorValue{}, nil
		}
		out = append(out, model.InstantSample{Metric: model.DropMetricName(series.Metric), Timestamp: last.Timestamp, Value: value})
	}
	return model.VectorValue{Samples: out}, nil
}

func TestIncreaseDeltaFamilyNativeSemanticsMatchLocalOracle(t *testing.T) {
	matrix := model.MatrixValue{Series: []model.RangeSeries{
		{Metric: map[string]string{"__name__": "requests_total", "job": "api"}, Values: []model.RangePoint{{Timestamp: 10, Value: 10}, {Timestamp: 20, Value: 16}, {Timestamp: 30, Value: 4}, {Timestamp: 40, Value: 9}}},
		{Metric: map[string]string{"__name__": "temperature", "job": "worker"}, Values: []model.RangePoint{{Timestamp: 10, Value: 3}, {Timestamp: 20, Value: math.NaN()}, {Timestamp: 30, Value: 9}}},
	}}

	for _, name := range []string{"increase", "delta", "idelta"} {
		oracle, ok := exec.LocalRangeOracleForTest(name)
		if !ok {
			t.Fatalf("expected local oracle for %q", name)
		}
		localValue, err := oracle(matrix)
		if err != nil {
			t.Fatalf("local oracle for %q returned error: %v", name, err)
		}
		nativeValue, err := applyNativeIncreaseDeltaFamilyForTest(name, matrix)
		if err != nil {
			t.Fatalf("native semantic helper for %q returned error: %v", name, err)
		}
		assertVectorEqual(t, name, localValue, nativeValue)
	}
}

func applyNativeIncreaseDeltaFamilyForTest(name string, matrix model.MatrixValue) (model.VectorValue, error) {
	out := make([]model.InstantSample, 0, len(matrix.Series))
	for _, series := range matrix.Series {
		if len(series.Values) < 2 {
			continue
		}
		last := series.Values[len(series.Values)-1]
		value := math.NaN()
		switch name {
		case "increase":
			hasNaN := false
			value = 0
			for i := 1; i < len(series.Values); i++ {
				prev := series.Values[i-1].Value
				cur := series.Values[i].Value
				if math.IsNaN(prev) || math.IsNaN(cur) {
					hasNaN = true
					continue
				}
				if cur < prev {
					value += cur
				} else {
					value += cur - prev
				}
			}
			if hasNaN {
				value = math.NaN()
			}
		case "delta":
			first := series.Values[0]
			if !math.IsNaN(first.Value) && !math.IsNaN(last.Value) {
				value = last.Value - first.Value
			}
		case "idelta":
			prev := series.Values[len(series.Values)-2]
			if !math.IsNaN(prev.Value) && !math.IsNaN(last.Value) {
				value = last.Value - prev.Value
			}
		default:
			return model.VectorValue{}, nil
		}
		out = append(out, model.InstantSample{Metric: model.DropMetricName(series.Metric), Timestamp: last.Timestamp, Value: value})
	}
	return model.VectorValue{Samples: out}, nil
}

func TestChangesAndDerivNativeSemanticsMatchLocalOracle(t *testing.T) {
	matrix := model.MatrixValue{Series: []model.RangeSeries{
		{Metric: map[string]string{"__name__": "requests_total", "job": "api"}, Values: []model.RangePoint{{Timestamp: 10, Value: 10}, {Timestamp: 20, Value: 16}, {Timestamp: 30, Value: 4}, {Timestamp: 40, Value: 9}}},
		{Metric: map[string]string{"__name__": "temperature", "job": "worker"}, Values: []model.RangePoint{{Timestamp: 10, Value: 1}, {Timestamp: 20, Value: 3}, {Timestamp: 30, Value: 5}}},
		{Metric: map[string]string{"__name__": "temperature", "job": "nan"}, Values: []model.RangePoint{{Timestamp: 10, Value: 1}, {Timestamp: 20, Value: math.NaN()}, {Timestamp: 30, Value: 5}}},
	}}

	for _, name := range []string{"changes", "deriv"} {
		oracle, ok := exec.LocalRangeOracleForTest(name)
		if !ok {
			t.Fatalf("expected local oracle for %q", name)
		}
		localValue, err := oracle(matrix)
		if err != nil {
			t.Fatalf("local oracle for %q returned error: %v", name, err)
		}
		nativeValue, err := applyNativeChangesDerivFamilyForTest(name, matrix)
		if err != nil {
			t.Fatalf("native semantic helper for %q returned error: %v", name, err)
		}
		assertVectorEqual(t, name, localValue, nativeValue)
	}
}

func applyNativeChangesDerivFamilyForTest(name string, matrix model.MatrixValue) (model.VectorValue, error) {
	out := make([]model.InstantSample, 0, len(matrix.Series))
	for _, series := range matrix.Series {
		if len(series.Values) == 0 {
			continue
		}
		last := series.Values[len(series.Values)-1]
		value := math.NaN()
		switch name {
		case "changes":
			changes := 0.0
			hasNaN := false
			prev := series.Values[0].Value
			for _, point := range series.Values[1:] {
				cur := point.Value
				if math.IsNaN(prev) || math.IsNaN(cur) {
					hasNaN = true
					prev = cur
					continue
				}
				if cur != prev {
					changes++
				}
				prev = cur
			}
			if hasNaN {
				value = math.NaN()
			} else {
				value = changes
			}
		case "deriv":
			if len(series.Values) < 2 {
				continue
			}
			hasNaN := false
			sumX, sumY, sumXY, sumX2 := 0.0, 0.0, 0.0, 0.0
			for _, point := range series.Values {
				if math.IsNaN(point.Value) {
					hasNaN = true
					break
				}
				x, y := point.Timestamp, point.Value
				sumX += x
				sumY += y
				sumXY += x * y
				sumX2 += x * x
			}
			if !hasNaN {
				n := float64(len(series.Values))
				denom := n*sumX2 - sumX*sumX
				if denom != 0 {
					value = (n*sumXY - sumX*sumY) / denom
				}
			}
		default:
			return model.VectorValue{}, nil
		}
		out = append(out, model.InstantSample{Metric: model.DropMetricName(series.Metric), Timestamp: last.Timestamp, Value: value})
	}
	return model.VectorValue{Samples: out}, nil
}

func TestCounterFamilyNativeDifferentialsCoverResetAndEdgeCases(t *testing.T) {
	cases := []struct {
		name   string
		funcs  []string
		matrix model.MatrixValue
	}{
		{
			name:  "counter reset and sparse windows",
			funcs: []string{"rate", "irate", "increase", "delta", "idelta", "changes", "deriv"},
			matrix: model.MatrixValue{Series: []model.RangeSeries{
				{Metric: map[string]string{"__name__": "requests_total", "job": "reset"}, Values: []model.RangePoint{{Timestamp: 10, Value: 10}, {Timestamp: 20, Value: 15}, {Timestamp: 30, Value: 3}, {Timestamp: 40, Value: 8}}},
				{Metric: map[string]string{"__name__": "requests_total", "job": "single"}, Values: []model.RangePoint{{Timestamp: 10, Value: 4}}},
			}},
		},
		{
			name:  "nan propagation and repeated timestamps",
			funcs: []string{"rate", "irate", "increase", "delta", "idelta", "changes", "deriv"},
			matrix: model.MatrixValue{Series: []model.RangeSeries{
				{Metric: map[string]string{"__name__": "temperature", "job": "nan"}, Values: []model.RangePoint{{Timestamp: 10, Value: 1}, {Timestamp: 20, Value: math.NaN()}, {Timestamp: 30, Value: 5}}},
				{Metric: map[string]string{"__name__": "temperature", "job": "flat-ts"}, Values: []model.RangePoint{{Timestamp: 10, Value: 1}, {Timestamp: 10, Value: 3}, {Timestamp: 10, Value: 5}}},
			}},
		},
	}

	for _, tc := range cases {
		for _, fn := range tc.funcs {
			t.Run(tc.name+"/"+fn, func(t *testing.T) {
				oracle, ok := exec.LocalRangeOracleForTest(fn)
				if !ok {
					t.Fatalf("expected local oracle for %q", fn)
				}
				want, err := oracle(tc.matrix)
				if err != nil {
					t.Fatalf("local oracle for %q returned error: %v", fn, err)
				}
				got, err := applyNativeCounterFamilyForTest(fn, tc.matrix)
				if err != nil {
					t.Fatalf("native helper for %q returned error: %v", fn, err)
				}
				assertVectorEqual(t, fn, want, got)
			})
		}
	}
}

func applyNativeCounterFamilyForTest(name string, matrix model.MatrixValue) (model.VectorValue, error) {
	switch name {
	case "rate", "irate":
		return applyNativeRateFamilyForTest(name, matrix)
	case "increase", "delta", "idelta":
		return applyNativeIncreaseDeltaFamilyForTest(name, matrix)
	case "changes", "deriv":
		return applyNativeChangesDerivFamilyForTest(name, matrix)
	default:
		return model.VectorValue{}, nil
	}
}

func assertVectorEqual(t *testing.T, name string, want, got model.VectorValue) {
	t.Helper()
	wantSamples := append([]model.InstantSample(nil), want.Samples...)
	gotSamples := append([]model.InstantSample(nil), got.Samples...)
	sort.Slice(wantSamples, func(i, j int) bool {
		left := model.LabelsKey(wantSamples[i].Metric)
		right := model.LabelsKey(wantSamples[j].Metric)
		if left == right {
			return wantSamples[i].Timestamp < wantSamples[j].Timestamp
		}
		return left < right
	})
	sort.Slice(gotSamples, func(i, j int) bool {
		left := model.LabelsKey(gotSamples[i].Metric)
		right := model.LabelsKey(gotSamples[j].Metric)
		if left == right {
			return gotSamples[i].Timestamp < gotSamples[j].Timestamp
		}
		return left < right
	})
	if len(wantSamples) != len(gotSamples) {
		t.Fatalf("%s: sample count mismatch want=%#v got=%#v", name, want, got)
	}
	for i := range wantSamples {
		if model.LabelsKey(wantSamples[i].Metric) != model.LabelsKey(gotSamples[i].Metric) {
			t.Fatalf("%s: metric mismatch want=%#v got=%#v", name, wantSamples[i], gotSamples[i])
		}
		if wantSamples[i].Timestamp != gotSamples[i].Timestamp {
			t.Fatalf("%s: timestamp mismatch want=%#v got=%#v", name, wantSamples[i], gotSamples[i])
		}
		if math.IsNaN(wantSamples[i].Value) != math.IsNaN(gotSamples[i].Value) {
			t.Fatalf("%s: NaN mismatch want=%#v got=%#v", name, wantSamples[i], gotSamples[i])
		}
		if !math.IsNaN(wantSamples[i].Value) && wantSamples[i].Value != gotSamples[i].Value {
			t.Fatalf("%s: value mismatch want=%#v got=%#v", name, wantSamples[i], gotSamples[i])
		}
	}
}
