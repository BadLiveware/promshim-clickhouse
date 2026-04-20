package promshim

import (
	"math"
	"time"

	"github.com/prometheus/prometheus/promql/parser"
)

func applyUnaryRuntimeValue(op parser.ItemType, value runtimeValue, params evalParams) (runtimeValue, error) {
	switch typed := value.(type) {
	case scalarValue:
		if params.Mode == evalModeRange {
			matrix, err := scalarValueToRangeMatrix(applyUnaryScalar(op, typed.Value), params)
			if err != nil {
				return nil, err
			}
			return matrix, nil
		}
		return scalarValue{Timestamp: typed.Timestamp, Value: applyUnaryScalar(op, typed.Value)}, nil
	case vectorValue:
		return vectorValue{Samples: applyUnaryToSamples(op, typed.Samples)}, nil
	case matrixValue:
		return matrixValue{Series: applyUnaryToSeries(op, typed.Series)}, nil
	default:
		return nil, newExecutionErrorf("unary operator %q requires scalar, vector, or matrix input, got %T", op.String(), value)
	}
}

func applyUnaryScalar(op parser.ItemType, value float64) float64 {
	if op == parser.SUB {
		return -value
	}
	return value
}

func applyUnaryToSamples(op parser.ItemType, samples []instantSample) []instantSample {
	if op == parser.ADD {
		return cloneSamples(samples)
	}
	result := make([]instantSample, 0, len(samples))
	for _, sample := range samples {
		result = append(result, instantSample{
			Metric:    dropMetricName(sample.Metric),
			Timestamp: sample.Timestamp,
			Value:     -sample.Value,
		})
	}
	return result
}

func applyUnaryToSeries(op parser.ItemType, series []rangeSeries) []rangeSeries {
	if op == parser.ADD {
		return cloneSeries(series)
	}
	result := make([]rangeSeries, 0, len(series))
	for _, item := range series {
		values := make([]rangePoint, 0, len(item.Values))
		for _, point := range item.Values {
			values = append(values, rangePoint{Timestamp: point.Timestamp, Value: -point.Value})
		}
		result = append(result, rangeSeries{Metric: dropMetricName(item.Metric), Values: values})
	}
	return result
}

func applyBinaryRuntimeValue(op parser.ItemType, lhs, rhs runtimeValue, returnBool bool, params evalParams) (runtimeValue, error) {
	switch left := lhs.(type) {
	case scalarValue:
		switch right := rhs.(type) {
		case scalarValue:
			if params.Mode == evalModeRange {
				matrix, err := scalarValueToRangeMatrix(applyScalarBinary(op, left.Value, right.Value), params)
				if err != nil {
					return nil, err
				}
				return matrix, nil
			}
			return scalarValue{Timestamp: scalarEvalTimestamp(params), Value: applyScalarBinary(op, left.Value, right.Value)}, nil
		case vectorValue:
			return vectorValue{Samples: applyVectorScalarBinaryInstant(op, right.Samples, left.Value, true, returnBool)}, nil
		case matrixValue:
			return matrixValue{Series: applyVectorScalarBinaryRange(op, right.Series, left.Value, true, returnBool)}, nil
		default:
			return nil, newExecutionErrorf("binary operator %q does not support operand types %T and %T", op.String(), lhs, rhs)
		}
	case vectorValue:
		switch right := rhs.(type) {
		case scalarValue:
			return vectorValue{Samples: applyVectorScalarBinaryInstant(op, left.Samples, right.Value, false, returnBool)}, nil
		default:
			return nil, newExecutionErrorf("binary operator %q does not support operand types %T and %T", op.String(), lhs, rhs)
		}
	case matrixValue:
		switch right := rhs.(type) {
		case scalarValue:
			return matrixValue{Series: applyVectorScalarBinaryRange(op, left.Series, right.Value, false, returnBool)}, nil
		default:
			return nil, newExecutionErrorf("binary operator %q does not support operand types %T and %T", op.String(), lhs, rhs)
		}
	default:
		return nil, newExecutionErrorf("binary operator %q does not support operand types %T and %T", op.String(), lhs, rhs)
	}
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

func applyVectorScalarBinaryInstant(op parser.ItemType, samples []instantSample, scalar float64, swap, returnBool bool) []instantSample {
	result := make([]instantSample, 0, len(samples))
	for _, sample := range samples {
		vectorValue := sample.Value
		lhs, rhs := vectorValue, scalar
		if swap {
			lhs, rhs = rhs, lhs
		}
		binaryValue := applyScalarBinary(op, lhs, rhs)
		keep := true
		outputValue := binaryValue
		metric := cloneMetric(sample.Metric)
		if isComparisonBinaryOperator(op) {
			comparisonKept := binaryValue != 0
			if swap && !returnBool {
				outputValue = vectorValue
			} else if !swap && !returnBool {
				outputValue = vectorValue
			}
			if returnBool {
				outputValue = boolToFloat(comparisonKept)
				metric = dropMetricName(metric)
			} else {
				keep = comparisonKept
			}
		} else {
			metric = dropMetricName(metric)
		}
		if !keep {
			continue
		}
		result = append(result, instantSample{Metric: metric, Timestamp: sample.Timestamp, Value: outputValue})
	}
	return result
}

func applyVectorScalarBinaryRange(op parser.ItemType, series []rangeSeries, scalar float64, swap, returnBool bool) []rangeSeries {
	result := make([]rangeSeries, 0, len(series))
	for _, item := range series {
		values := make([]rangePoint, 0, len(item.Values))
		metric := cloneMetric(item.Metric)
		if !isComparisonBinaryOperator(op) || returnBool {
			metric = dropMetricName(metric)
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
			values = append(values, rangePoint{Timestamp: point.Timestamp, Value: outputValue})
		}
		if len(values) == 0 {
			continue
		}
		result = append(result, rangeSeries{Metric: metric, Values: values})
	}
	return result
}

func scalarValueToRangeMatrix(value float64, params evalParams) (matrixValue, error) {
	values, err := buildConstantRangePoints(value, params)
	if err != nil {
		return matrixValue{}, err
	}
	return matrixValue{Series: []rangeSeries{{Metric: map[string]string{}, Values: values}}}, nil
}

func scalarEvalTimestamp(params evalParams) float64 {
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
