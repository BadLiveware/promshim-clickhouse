package promshim

import "fmt"

func renderInstantQueryValue(value runtimeValue) (string, any, error) {
	switch typed := value.(type) {
	case scalarValue:
		return "scalar", []any{typed.Timestamp, formatPromValue(typed.Value)}, nil
	case vectorValue:
		return "vector", renderVectorSamples(typed.Samples), nil
	case matrixValue:
		return "matrix", renderMatrixSeries(typed.Series), nil
	default:
		return "", nil, newExecutionErrorf("cannot render instant query response for runtime value %T", value)
	}
}

func renderRangeQueryValue(value runtimeValue) (string, any, error) {
	switch typed := value.(type) {
	case matrixValue:
		return "matrix", renderMatrixSeries(typed.Series), nil
	default:
		return "", nil, newExecutionErrorf("cannot render range query response for runtime value %T", value)
	}
}

func renderVectorSamples(samples []instantSample) []map[string]any {
	rows := make([]map[string]any, 0, len(samples))
	for _, sample := range samples {
		rows = append(rows, map[string]any{
			"metric": sample.Metric,
			"value":  []any{sample.Timestamp, formatPromValue(sample.Value)},
		})
	}
	return rows
}

func renderMatrixSeries(series []rangeSeries) []map[string]any {
	rows := make([]map[string]any, 0, len(series))
	for _, item := range series {
		values := make([][]any, 0, len(item.Values))
		for _, point := range item.Values {
			values = append(values, []any{point.Timestamp, formatPromValue(point.Value)})
		}
		rows = append(rows, map[string]any{
			"metric": item.Metric,
			"values": values,
		})
	}
	return rows
}

func describeRuntimeValue(value runtimeValue) string {
	return fmt.Sprintf("%T", value)
}
