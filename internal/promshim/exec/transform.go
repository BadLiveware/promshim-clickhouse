package exec

import (
	"math"
	"sort"
	"time"

	"github.com/BadLiveware/promshim-ch/internal/promshim/model"
)

func ApplyVector(input model.RuntimeValue) (model.VectorValue, error) {
	scalar, ok := input.(model.ScalarValue)
	if !ok {
		return model.VectorValue{}, unsupportedf("vector() currently requires scalar input, got %T", input)
	}
	return model.VectorValue{Samples: []model.InstantSample{{Metric: map[string]string{}, Timestamp: scalar.Timestamp, Value: scalar.Value}}}, nil
}

func ApplyScalar(input model.RuntimeValue, params EvalParams) (model.ScalarValue, error) {
	vector, ok := input.(model.VectorValue)
	if !ok {
		return model.ScalarValue{}, unsupportedf("scalar() requires vector input, got %T", input)
	}
	timestamp := scalarEvalTimestamp(params)
	if len(vector.Samples) != 1 {
		return model.ScalarValue{Timestamp: timestamp, Value: math.NaN()}, nil
	}
	return model.ScalarValue{Timestamp: timestamp, Value: vector.Samples[0].Value}, nil
}

func ApplyScalarBuiltinFunction(name string, params EvalParams) (model.ScalarValue, error) {
	switch name {
	case "pi":
		return model.ScalarValue{Timestamp: scalarEvalTimestamp(params), Value: math.Pi}, nil
	case "time":
		return model.ScalarValue{Timestamp: scalarEvalTimestamp(params), Value: scalarEvalTimestamp(params)}, nil
	default:
		return model.ScalarValue{}, unsupportedf("scalar builtin %q is not implemented yet", name)
	}
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
	return sortVectorSamples(out), nil
}

func ApplyPointwiseFunction(name string, input model.RuntimeValue, params EvalParams, paramNumbers []*float64) (model.VectorValue, error) {
	if input == nil {
		switch name {
		case "minute", "hour", "day_of_week", "day_of_month", "day_of_year", "days_in_month", "month", "year":
			value := applyDateFunction(name, time.Unix(params.EvaluationTime.Unix(), 0).UTC())
			return model.VectorValue{Samples: []model.InstantSample{{Metric: map[string]string{}, Timestamp: scalarEvalTimestamp(params), Value: value}}}, nil
		default:
			return model.VectorValue{}, unsupportedf("%s requires vector input, got <nil>", name)
		}
	}
	vector, ok := input.(model.VectorValue)
	if !ok {
		return model.VectorValue{}, unsupportedf("%s currently requires vector input, got %T", name, input)
	}
	out := make([]model.InstantSample, 0, len(vector.Samples))
	for _, sample := range vector.Samples {
		value, keep, err := applyPointwiseSample(name, sample, paramNumbers)
		if err != nil {
			return model.VectorValue{}, err
		}
		if !keep {
			continue
		}
		out = append(out, model.InstantSample{Metric: model.DropMetricName(sample.Metric), Timestamp: sample.Timestamp, Value: value})
	}
	return sortVectorSamples(out), nil
}

func applyPointwiseSample(name string, sample model.InstantSample, paramNumbers []*float64) (float64, bool, error) {
	switch name {
	case "abs":
		return math.Abs(sample.Value), true, nil
	case "ceil":
		return math.Ceil(sample.Value), true, nil
	case "floor":
		return math.Floor(sample.Value), true, nil
	case "sgn":
		switch {
		case sample.Value < 0:
			return -1, true, nil
		case sample.Value > 0:
			return 1, true, nil
		default:
			return sample.Value, true, nil
		}
	case "exp":
		return math.Exp(sample.Value), true, nil
	case "ln":
		return math.Log(sample.Value), true, nil
	case "log2":
		return math.Log2(sample.Value), true, nil
	case "log10":
		return math.Log10(sample.Value), true, nil
	case "sqrt":
		return math.Sqrt(sample.Value), true, nil
	case "sin":
		return math.Sin(sample.Value), true, nil
	case "cos":
		return math.Cos(sample.Value), true, nil
	case "tan":
		return math.Tan(sample.Value), true, nil
	case "asin":
		return math.Asin(sample.Value), true, nil
	case "acos":
		return math.Acos(sample.Value), true, nil
	case "atan":
		return math.Atan(sample.Value), true, nil
	case "sinh":
		return math.Sinh(sample.Value), true, nil
	case "cosh":
		return math.Cosh(sample.Value), true, nil
	case "tanh":
		return math.Tanh(sample.Value), true, nil
	case "asinh":
		return math.Asinh(sample.Value), true, nil
	case "acosh":
		return math.Acosh(sample.Value), true, nil
	case "atanh":
		return math.Atanh(sample.Value), true, nil
	case "deg":
		return sample.Value * 180 / math.Pi, true, nil
	case "rad":
		return sample.Value * math.Pi / 180, true, nil
	case "timestamp":
		return sample.Timestamp, true, nil
	case "minute", "hour", "day_of_week", "day_of_month", "day_of_year", "days_in_month", "month", "year":
		return applyDateFunction(name, time.Unix(int64(sample.Value), 0).UTC()), true, nil
	case "clamp":
		if len(paramNumbers) != 2 || paramNumbers[0] == nil || paramNumbers[1] == nil {
			return 0, false, badDataf("clamp requires two scalar bounds")
		}
		minVal, maxVal := *paramNumbers[0], *paramNumbers[1]
		if maxVal < minVal {
			return 0, false, nil
		}
		return math.Max(minVal, math.Min(maxVal, sample.Value)), true, nil
	case "clamp_min":
		if len(paramNumbers) != 1 || paramNumbers[0] == nil {
			return 0, false, badDataf("clamp_min requires one scalar bound")
		}
		return math.Max(*paramNumbers[0], sample.Value), true, nil
	case "clamp_max":
		if len(paramNumbers) != 1 || paramNumbers[0] == nil {
			return 0, false, badDataf("clamp_max requires one scalar bound")
		}
		return math.Min(*paramNumbers[0], sample.Value), true, nil
	default:
		return 0, false, unsupportedf("pointwise function %q is not implemented yet", name)
	}
}

func applyDateFunction(name string, t time.Time) float64 {
	switch name {
	case "minute":
		return float64(t.Minute())
	case "hour":
		return float64(t.Hour())
	case "day_of_week":
		return float64(t.Weekday())
	case "day_of_month":
		return float64(t.Day())
	case "day_of_year":
		return float64(t.YearDay())
	case "days_in_month":
		return float64(32 - time.Date(t.Year(), t.Month(), 32, 0, 0, 0, 0, time.UTC).Day())
	case "month":
		return float64(t.Month())
	case "year":
		return float64(t.Year())
	default:
		panic("unexpected date function")
	}
}

func sortVectorSamples(samples []model.InstantSample) model.VectorValue {
	sort.Slice(samples, func(i, j int) bool {
		left := model.LabelsKey(samples[i].Metric)
		right := model.LabelsKey(samples[j].Metric)
		if left == right {
			return samples[i].Timestamp < samples[j].Timestamp
		}
		return left < right
	})
	return model.VectorValue{Samples: samples}
}
