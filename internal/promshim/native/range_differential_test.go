package native

import (
	"math"
	"sort"
	"testing"
	"time"

	"ch-observability/internal/promshim/exec"
	"ch-observability/internal/promshim/model"
)

func TestAggregateOverTimeNativeSemanticsMatchLocalOracle(t *testing.T) {
	matrix := model.MatrixValue{Series: []model.RangeSeries{
		{Metric: map[string]string{"__name__": "up", "job": "api"}, Values: []model.RangePoint{{Timestamp: 10, Value: 1}, {Timestamp: 20, Value: 2}, {Timestamp: 30, Value: 5}}},
		{Metric: map[string]string{"__name__": "up", "job": "worker"}, Values: []model.RangePoint{{Timestamp: 10, Value: 3}, {Timestamp: 20, Value: math.NaN()}, {Timestamp: 30, Value: 9}}},
	}}

	for _, name := range []string{"last_over_time", "sum_over_time", "avg_over_time", "min_over_time", "max_over_time", "count_over_time", "stddev_over_time", "stdvar_over_time", "present_over_time", "mad_over_time"} {
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
		case "stddev_over_time", "stdvar_over_time":
			if hasNaN || len(values) == 0 {
				value = math.NaN()
			} else {
				count := 0.0
				mean := 0.0
				m2 := 0.0
				for _, v := range values {
					count++
					delta := v - mean
					mean += delta / count
					m2 += delta * (v - mean)
				}
				value = m2 / count
				if name == "stddev_over_time" {
					value = math.Sqrt(value)
				}
			}
		case "present_over_time":
			value = 1
		case "mad_over_time":
			if len(values) == 0 {
				continue
			}
			median := nativeMedian(values)
			deviations := make([]float64, 0, len(values))
			for _, v := range values {
				deviations = append(deviations, math.Abs(v-median))
			}
			value = nativeMedian(deviations)
		default:
			return model.VectorValue{}, nil
		}
		out = append(out, model.InstantSample{Metric: model.DropMetricName(series.Metric), Timestamp: last.Timestamp, Value: value})
	}
	return model.VectorValue{Samples: out}, nil
}

func TestPredictLinearNativeSemanticsMatchLocalOracle(t *testing.T) {
	matrix := model.MatrixValue{Series: []model.RangeSeries{
		{Metric: map[string]string{"__name__": "up", "job": "api"}, Values: []model.RangePoint{{Timestamp: 1000, Value: 1}, {Timestamp: 2000, Value: 2}, {Timestamp: 3000, Value: 3}}},
		{Metric: map[string]string{"__name__": "up", "job": "worker"}, Values: []model.RangePoint{{Timestamp: 1000, Value: 9}, {Timestamp: 2000, Value: 9}, {Timestamp: 3000, Value: 9}}},
		{Metric: map[string]string{"__name__": "up", "job": "inf"}, Values: []model.RangePoint{{Timestamp: 1000, Value: math.Inf(1)}, {Timestamp: 2000, Value: math.Inf(1)}}},
	}}
	params := exec.EvalParams{Mode: exec.EvalModeInstant, EvaluationTime: time.UnixMilli(4000).UTC()}
	localValue, err := exec.ApplyPredictLinear(60, matrix, params)
	if err != nil {
		t.Fatalf("local oracle for predict_linear returned error: %v", err)
	}
	nativeValue, err := applyNativePredictLinearForTest(60, matrix, 4000)
	if err != nil {
		t.Fatalf("native semantic helper for predict_linear returned error: %v", err)
	}
	assertVectorEqual(t, "predict_linear", localValue, nativeValue)
}

func TestDoubleExponentialSmoothingNativeSemanticsMatchLocalOracle(t *testing.T) {
	matrix := model.MatrixValue{Series: []model.RangeSeries{
		{Metric: map[string]string{"__name__": "up", "job": "api"}, Values: []model.RangePoint{{Timestamp: 1, Value: 1}, {Timestamp: 2, Value: 3}, {Timestamp: 3, Value: 2}, {Timestamp: 4, Value: 5}}},
		{Metric: map[string]string{"__name__": "up", "job": "worker"}, Values: []model.RangePoint{{Timestamp: 1, Value: 7}, {Timestamp: 2, Value: 7}}},
	}}
	localValue, err := exec.ApplyDoubleExponentialSmoothing(0.5, 0.3, matrix)
	if err != nil {
		t.Fatalf("local oracle for smoothing returned error: %v", err)
	}
	nativeValue, err := applyNativeDoubleExponentialSmoothingForTest(0.5, 0.3, matrix)
	if err != nil {
		t.Fatalf("native semantic helper for smoothing returned error: %v", err)
	}
	assertVectorEqual(t, "double_exponential_smoothing", localValue, nativeValue)
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

func TestChangesDerivAndResetsNativeSemanticsMatchLocalOracle(t *testing.T) {
	matrix := model.MatrixValue{Series: []model.RangeSeries{
		{Metric: map[string]string{"__name__": "requests_total", "job": "api"}, Values: []model.RangePoint{{Timestamp: 10, Value: 10}, {Timestamp: 20, Value: 16}, {Timestamp: 30, Value: 4}, {Timestamp: 40, Value: 9}}},
		{Metric: map[string]string{"__name__": "temperature", "job": "worker"}, Values: []model.RangePoint{{Timestamp: 10, Value: 1}, {Timestamp: 20, Value: 3}, {Timestamp: 30, Value: 5}}},
		{Metric: map[string]string{"__name__": "temperature", "job": "nan"}, Values: []model.RangePoint{{Timestamp: 10, Value: 1}, {Timestamp: 20, Value: math.NaN()}, {Timestamp: 30, Value: 5}}},
	}}

	for _, name := range []string{"changes", "deriv", "resets"} {
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
		case "resets":
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
			value = resets
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
			funcs: []string{"rate", "irate", "increase", "delta", "idelta", "changes", "deriv", "resets"},
			matrix: model.MatrixValue{Series: []model.RangeSeries{
				{Metric: map[string]string{"__name__": "requests_total", "job": "reset"}, Values: []model.RangePoint{{Timestamp: 10, Value: 10}, {Timestamp: 20, Value: 15}, {Timestamp: 30, Value: 3}, {Timestamp: 40, Value: 8}}},
				{Metric: map[string]string{"__name__": "requests_total", "job": "single"}, Values: []model.RangePoint{{Timestamp: 10, Value: 4}}},
			}},
		},
		{
			name:  "nan propagation and repeated timestamps",
			funcs: []string{"rate", "irate", "increase", "delta", "idelta", "changes", "deriv", "resets"},
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
	case "changes", "deriv", "resets":
		return applyNativeChangesDerivFamilyForTest(name, matrix)
	default:
		return model.VectorValue{}, nil
	}
}

func applyNativePredictLinearForTest(duration float64, matrix model.MatrixValue, evaluationTimeMS float64) (model.VectorValue, error) {
	out := make([]model.InstantSample, 0, len(matrix.Series))
	for _, series := range matrix.Series {
		if len(series.Values) < 2 {
			continue
		}
		slope, intercept := nativeLinearRegression(series.Values, evaluationTimeMS)
		out = append(out, model.InstantSample{Metric: model.DropMetricName(series.Metric), Timestamp: evaluationTimeMS, Value: slope*duration + intercept})
	}
	return model.VectorValue{Samples: out}, nil
}

func nativeLinearRegression(samples []model.RangePoint, interceptTime float64) (slope, intercept float64) {
	var (
		n          float64
		sumX, cX   float64
		sumY, cY   float64
		sumXY, cXY float64
		sumX2, cX2 float64
		initY      float64
		constY     bool
	)
	initY = samples[0].Value
	constY = true
	for i, sample := range samples {
		if constY && i > 0 && sample.Value != initY {
			constY = false
		}
		n += 1.0
		x := (sample.Timestamp - interceptTime) / 1e3
		sumX, cX = kahanInc(x, sumX, cX)
		sumY, cY = kahanInc(sample.Value, sumY, cY)
		sumXY, cXY = kahanInc(x*sample.Value, sumXY, cXY)
		sumX2, cX2 = kahanInc(x*x, sumX2, cX2)
	}
	if constY {
		if math.IsInf(initY, 0) {
			return math.NaN(), math.NaN()
		}
		return 0, initY
	}
	sumX += cX
	sumY += cY
	sumXY += cXY
	sumX2 += cX2
	covXY := sumXY - sumX*sumY/n
	varX := sumX2 - sumX*sumX/n
	return covXY / varX, sumY/n - (covXY/varX)*sumX/n
}

func kahanInc(inc, sum, c float64) (float64, float64) {
	y := inc - c
	t := sum + y
	return t, (t - sum) - y
}

func applyNativeDoubleExponentialSmoothingForTest(sf, tf float64, matrix model.MatrixValue) (model.VectorValue, error) {
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
	return model.VectorValue{Samples: out}, nil
}

func nativeMedian(values []float64) float64 {
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
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
