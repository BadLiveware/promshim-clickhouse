package exec

import (
	"math"
	"sort"
	"time"

	"ch-observability/internal/promshim/model"
	"github.com/prometheus/prometheus/promql/parser"
)

type EvalMode string

const (
	EvalModeInstant EvalMode = "instant"
	EvalModeRange   EvalMode = "range"
)

type EvalParams struct {
	Mode           EvalMode
	EvaluationTime time.Time
	Start          time.Time
	End            time.Time
	Step           time.Duration
}

func ApplyUnaryRuntimeValue(op parser.ItemType, value model.RuntimeValue, params EvalParams) (model.RuntimeValue, error) {
	switch typed := value.(type) {
	case model.ScalarValue:
		if params.Mode == EvalModeRange {
			matrix, err := scalarValueToRangeMatrix(applyUnaryScalar(op, typed.Value), params)
			if err != nil {
				return nil, err
			}
			return matrix, nil
		}
		return model.ScalarValue{Timestamp: typed.Timestamp, Value: applyUnaryScalar(op, typed.Value)}, nil
	case model.VectorValue:
		samples := applyUnaryToSamples(op, typed.Samples)
		if err := ensureUniqueInstantLabelsets(samples); err != nil {
			return nil, err
		}
		return model.VectorValue{Samples: samples}, nil
	case model.MatrixValue:
		series, err := ensureUniqueRangeLabelsets(applyUnaryToSeries(op, typed.Series))
		if err != nil {
			return nil, err
		}
		return model.MatrixValue{Series: series}, nil
	default:
		return nil, executionf("unary operator %q requires scalar, vector, or matrix input, got %T", op.String(), value)
	}
}

func ApplyBinaryRuntimeValue(op parser.ItemType, lhs, rhs model.RuntimeValue, vectorMatching *parser.VectorMatching, returnBool bool, params EvalParams) (model.RuntimeValue, error) {
	switch left := lhs.(type) {
	case model.ScalarValue:
		switch right := rhs.(type) {
		case model.ScalarValue:
			if params.Mode == EvalModeRange {
				matrix, err := scalarValueToRangeMatrix(applyScalarBinary(op, left.Value, right.Value), params)
				if err != nil {
					return nil, err
				}
				return matrix, nil
			}
			return model.ScalarValue{Timestamp: scalarEvalTimestamp(params), Value: applyScalarBinary(op, left.Value, right.Value)}, nil
		case model.VectorValue:
			samples := applyVectorScalarBinaryInstant(op, right.Samples, left.Value, true, returnBool)
			if err := ensureUniqueInstantLabelsets(samples); err != nil {
				return nil, err
			}
			return model.VectorValue{Samples: samples}, nil
		case model.MatrixValue:
			series, err := ensureUniqueRangeLabelsets(applyVectorScalarBinaryRange(op, right.Series, left.Value, true, returnBool))
			if err != nil {
				return nil, err
			}
			return model.MatrixValue{Series: series}, nil
		default:
			return nil, executionf("binary operator %q does not support operand types %T and %T", op.String(), lhs, rhs)
		}
	case model.VectorValue:
		switch right := rhs.(type) {
		case model.ScalarValue:
			samples := applyVectorScalarBinaryInstant(op, left.Samples, right.Value, false, returnBool)
			if err := ensureUniqueInstantLabelsets(samples); err != nil {
				return nil, err
			}
			return model.VectorValue{Samples: samples}, nil
		case model.VectorValue:
			samples, err := applyVectorVectorBinaryInstant(op, left.Samples, right.Samples, vectorMatching, returnBool)
			if err != nil {
				return nil, err
			}
			return model.VectorValue{Samples: samples}, nil
		default:
			return nil, executionf("binary operator %q does not support operand types %T and %T", op.String(), lhs, rhs)
		}
	case model.MatrixValue:
		switch right := rhs.(type) {
		case model.ScalarValue:
			series, err := ensureUniqueRangeLabelsets(applyVectorScalarBinaryRange(op, left.Series, right.Value, false, returnBool))
			if err != nil {
				return nil, err
			}
			return model.MatrixValue{Series: series}, nil
		case model.MatrixValue:
			leftIsScalar := isRangeScalarMatrix(left)
			rightIsScalar := isRangeScalarMatrix(right)
			if vectorMatching == nil && leftIsScalar != rightIsScalar {
				var scalar, vector []model.RangeSeries
				swap := false
				if leftIsScalar {
					scalar, vector = left.Series, right.Series
					swap = true
				} else {
					scalar, vector = right.Series, left.Series
				}
				series, err := ensureUniqueRangeLabelsets(applyScalarMatrixBinaryRange(op, vector, scalar[0], swap, returnBool))
				if err != nil {
					return nil, err
				}
				return model.MatrixValue{Series: series}, nil
			}
			series, err := applyVectorVectorBinaryRange(op, left.Series, right.Series, vectorMatching, returnBool)
			if err != nil {
				return nil, err
			}
			return model.MatrixValue{Series: series}, nil
		default:
			return nil, executionf("binary operator %q does not support operand types %T and %T", op.String(), lhs, rhs)
		}
	default:
		return nil, executionf("binary operator %q does not support operand types %T and %T", op.String(), lhs, rhs)
	}
}

func applyUnaryScalar(op parser.ItemType, value float64) float64 {
	if op == parser.SUB {
		return -value
	}
	return value
}

func applyUnaryToSamples(op parser.ItemType, samples []model.InstantSample) []model.InstantSample {
	if op == parser.ADD {
		return model.CloneSamples(samples)
	}
	result := make([]model.InstantSample, 0, len(samples))
	for _, sample := range samples {
		result = append(result, model.InstantSample{Metric: model.DropMetricName(sample.Metric), Timestamp: sample.Timestamp, Value: -sample.Value})
	}
	return result
}

func applyUnaryToSeries(op parser.ItemType, series []model.RangeSeries) []model.RangeSeries {
	if op == parser.ADD {
		return model.CloneSeries(series)
	}
	result := make([]model.RangeSeries, 0, len(series))
	for _, item := range series {
		values := make([]model.RangePoint, 0, len(item.Values))
		for _, point := range item.Values {
			values = append(values, model.RangePoint{Timestamp: point.Timestamp, Value: -point.Value})
		}
		result = append(result, model.RangeSeries{Metric: model.DropMetricName(item.Metric), Values: values})
	}
	return result
}

func applyScalarBinary(op parser.ItemType, lhs, rhs float64) float64 {
	switch op {
	case parser.ADD:
		return lhs + rhs
	case parser.SUB:
		return lhs - rhs
	case parser.MUL:
		return lhs * rhs
	case parser.DIV:
		return lhs / rhs
	case parser.POW:
		return math.Pow(lhs, rhs)
	case parser.MOD:
		return math.Mod(lhs, rhs)
	case parser.EQLC:
		return boolToFloat(lhs == rhs)
	case parser.NEQ:
		return boolToFloat(lhs != rhs)
	case parser.GTR:
		return boolToFloat(lhs > rhs)
	case parser.LSS:
		return boolToFloat(lhs < rhs)
	case parser.GTE:
		return boolToFloat(lhs >= rhs)
	case parser.LTE:
		return boolToFloat(lhs <= rhs)
	default:
		panic("unsupported scalar binary operator")
	}
}

func applyVectorScalarBinaryInstant(op parser.ItemType, samples []model.InstantSample, scalar float64, swap, returnBool bool) []model.InstantSample {
	result := make([]model.InstantSample, 0, len(samples))
	for _, sample := range samples {
		vectorValue := sample.Value
		lhs, rhs := vectorValue, scalar
		if swap {
			lhs, rhs = rhs, lhs
		}
		binaryValue := applyScalarBinary(op, lhs, rhs)
		keep := true
		outputValue := binaryValue
		metric := model.CloneMetric(sample.Metric)
		if isComparisonBinaryOperator(op) {
			comparisonKept := binaryValue != 0
			if !returnBool {
				outputValue = vectorValue
				keep = comparisonKept
			} else {
				outputValue = boolToFloat(comparisonKept)
				metric = model.DropMetricName(metric)
			}
		} else {
			metric = model.DropMetricName(metric)
		}
		if !keep {
			continue
		}
		result = append(result, model.InstantSample{Metric: metric, Timestamp: sample.Timestamp, Value: outputValue})
	}
	return result
}

func applyVectorScalarBinaryRange(op parser.ItemType, series []model.RangeSeries, scalar float64, swap, returnBool bool) []model.RangeSeries {
	result := make([]model.RangeSeries, 0, len(series))
	for _, item := range series {
		values := make([]model.RangePoint, 0, len(item.Values))
		metric := model.CloneMetric(item.Metric)
		if !isComparisonBinaryOperator(op) || returnBool {
			metric = model.DropMetricName(metric)
		}
		for _, point := range item.Values {
			vectorValue := point.Value
			lhs, rhs := vectorValue, scalar
			if swap {
				lhs, rhs = rhs, lhs
			}
			binaryValue := applyScalarBinary(op, lhs, rhs)
			keep := true
			outputValue := binaryValue
			if isComparisonBinaryOperator(op) {
				comparisonKept := binaryValue != 0
				if !returnBool {
					outputValue = vectorValue
					keep = comparisonKept
				} else {
					outputValue = boolToFloat(comparisonKept)
				}
			}
			if !keep {
				continue
			}
			values = append(values, model.RangePoint{Timestamp: point.Timestamp, Value: outputValue})
		}
		if len(values) == 0 {
			continue
		}
		result = append(result, model.RangeSeries{Metric: metric, Values: values})
	}
	return result
}

func isRangeScalarMatrix(m model.MatrixValue) bool {
	return len(m.Series) == 1 && len(m.Series[0].Metric) == 0
}

func applyScalarMatrixBinaryRange(op parser.ItemType, series []model.RangeSeries, scalar model.RangeSeries, swap, returnBool bool) []model.RangeSeries {
	scalarByTimestamp := make(map[float64]float64, len(scalar.Values))
	for _, point := range scalar.Values {
		scalarByTimestamp[point.Timestamp] = point.Value
	}
	result := make([]model.RangeSeries, 0, len(series))
	for _, item := range series {
		values := make([]model.RangePoint, 0, len(item.Values))
		metric := model.CloneMetric(item.Metric)
		if !isComparisonBinaryOperator(op) || returnBool {
			metric = model.DropMetricName(metric)
		}
		for _, point := range item.Values {
			scalarValue, ok := scalarByTimestamp[point.Timestamp]
			if !ok {
				continue
			}
			vectorValue := point.Value
			lhs, rhs := vectorValue, scalarValue
			if swap {
				lhs, rhs = rhs, lhs
			}
			binaryValue := applyScalarBinary(op, lhs, rhs)
			keep := true
			outputValue := binaryValue
			if isComparisonBinaryOperator(op) {
				comparisonKept := binaryValue != 0
				if !returnBool {
					outputValue = vectorValue
					keep = comparisonKept
				} else {
					outputValue = boolToFloat(comparisonKept)
				}
			}
			if !keep {
				continue
			}
			values = append(values, model.RangePoint{Timestamp: point.Timestamp, Value: outputValue})
		}
		if len(values) == 0 {
			continue
		}
		result = append(result, model.RangeSeries{Metric: metric, Values: values})
	}
	return result
}

func ensureUniqueInstantLabelsets(samples []model.InstantSample) error {
	if len(samples) <= 1 {
		return nil
	}
	seen := make(map[string]struct{}, len(samples))
	for _, sample := range samples {
		key := model.LabelsKey(sample.Metric)
		if _, ok := seen[key]; ok {
			return badDataf("%s", model.ErrDuplicateLabelsetTimestamps.Error())
		}
		seen[key] = struct{}{}
	}
	return nil
}

func ensureUniqueRangeLabelsets(series []model.RangeSeries) ([]model.RangeSeries, error) {
	if len(series) <= 1 {
		return series, nil
	}
	grouped := make(map[string]model.RangeSeries, len(series))
	order := make([]string, 0, len(series))
	for _, item := range series {
		key := model.LabelsKey(item.Metric)
		group, ok := grouped[key]
		if !ok {
			grouped[key] = model.RangeSeries{Metric: model.CloneMetric(item.Metric), Values: model.CloneRangePoints(item.Values)}
			order = append(order, key)
			continue
		}
		group.Values = append(group.Values, model.CloneRangePoints(item.Values)...)
		grouped[key] = group
	}
	result := make([]model.RangeSeries, 0, len(order))
	for _, key := range order {
		group := grouped[key]
		sort.Slice(group.Values, func(i, j int) bool {
			return group.Values[i].Timestamp < group.Values[j].Timestamp
		})
		for i := 1; i < len(group.Values); i++ {
			if group.Values[i].Timestamp == group.Values[i-1].Timestamp {
				return nil, badDataf("%s", model.ErrDuplicateLabelsetTimestamps.Error())
			}
		}
		result = append(result, group)
	}
	return result, nil
}

func scalarValueToRangeMatrix(value float64, params EvalParams) (model.MatrixValue, error) {
	values, err := model.BuildConstantRangePoints(params.Start, params.End, params.Step, value)
	if err != nil {
		return model.MatrixValue{}, badDataf("%s", err.Error())
	}
	return model.MatrixValue{Series: []model.RangeSeries{{Metric: map[string]string{}, Values: values}}}, nil
}

func scalarEvalTimestamp(params EvalParams) float64 {
	return float64(params.EvaluationTime.UnixNano()) / float64(time.Second)
}
func boolToFloat(value bool) float64 {
	if value {
		return 1
	}
	return 0
}
func isComparisonBinaryOperator(op parser.ItemType) bool {
	switch op {
	case parser.EQLC, parser.NEQ, parser.GTR, parser.LSS, parser.GTE, parser.LTE:
		return true
	default:
		return false
	}
}
