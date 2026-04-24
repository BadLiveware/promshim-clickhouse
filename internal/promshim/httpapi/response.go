package httpapi

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/model"
)

type ResponseStats struct{ Series, Points int64 }

func RuntimeValueResponseStats(value model.RuntimeValue) (ResponseStats, error) {
	switch typed := value.(type) {
	case model.ScalarValue:
		return ResponseStats{Series: 1, Points: 1}, nil
	case model.VectorValue:
		count := int64(len(typed.Samples))
		return ResponseStats{Series: count, Points: count}, nil
	case model.MatrixValue:
		stats := ResponseStats{Series: int64(len(typed.Series))}
		for _, series := range typed.Series {
			stats.Points += int64(len(series.Values))
		}
		return stats, nil
	default:
		return ResponseStats{}, fmt.Errorf("cannot compute response stats for runtime value %T", value)
	}
}

func RenderInstantQueryValue(value model.RuntimeValue) (string, any, error) {
	switch typed := value.(type) {
	case model.ScalarValue:
		return "scalar", []any{typed.Timestamp, formatPromValue(typed.Value)}, nil
	case model.VectorValue:
		return "vector", renderVectorSamples(typed.Samples), nil
	case model.MatrixValue:
		return "matrix", renderMatrixSeries(typed.Series), nil
	default:
		return "", nil, fmt.Errorf("cannot render instant query response for runtime value %T", value)
	}
}

func RenderRangeQueryValue(value model.RuntimeValue) (string, any, error) {
	switch typed := value.(type) {
	case model.MatrixValue:
		return "matrix", renderMatrixSeries(typed.Series), nil
	default:
		return "", nil, fmt.Errorf("cannot render range query response for runtime value %T", value)
	}
}

func WritePromSuccessInstantValue(w http.ResponseWriter, value model.RuntimeValue) error {
	switch typed := value.(type) {
	case model.ScalarValue:
		return writePromSuccessScalar(w, typed)
	case model.VectorValue:
		return writePromSuccessVector(w, typed.Samples)
	case model.MatrixValue:
		return writePromSuccessMatrix(w, typed.Series)
	default:
		return fmt.Errorf("cannot render instant query response for runtime value %T", value)
	}
}

func WritePromSuccessRangeValue(w http.ResponseWriter, value model.RuntimeValue) error {
	typed, ok := value.(model.MatrixValue)
	if !ok {
		return fmt.Errorf("cannot render range query response for runtime value %T", value)
	}
	return writePromSuccessMatrix(w, typed.Series)
}

func renderVectorSamples(samples []model.InstantSample) []map[string]any {
	rows := make([]map[string]any, 0, len(samples))
	for _, sample := range samples {
		rows = append(rows, map[string]any{"metric": sample.Metric, "value": []any{sample.Timestamp, formatPromValue(sample.Value)}})
	}
	return rows
}

func renderMatrixSeries(series []model.RangeSeries) []map[string]any {
	rows := make([]map[string]any, 0, len(series))
	for _, item := range series {
		values := make([][]any, 0, len(item.Values))
		for _, point := range item.Values {
			values = append(values, []any{point.Timestamp, formatPromValue(point.Value)})
		}
		rows = append(rows, map[string]any{"metric": item.Metric, "values": values})
	}
	return rows
}

func writePromSuccessScalar(w http.ResponseWriter, value model.ScalarValue) error {
	startJSONResponse(w, http.StatusOK)
	writer := bufio.NewWriter(w)
	if _, err := writer.WriteString(`{"status":"success","data":{"resultType":"scalar","result":[`); err != nil {
		return err
	}
	if _, err := writer.WriteString(strconv.FormatFloat(value.Timestamp, 'f', -1, 64)); err != nil {
		return err
	}
	if _, err := writer.WriteString(","); err != nil {
		return err
	}
	if err := writeJSONString(writer, formatPromValue(value.Value)); err != nil {
		return err
	}
	if _, err := writer.WriteString(`]}}` + "\n"); err != nil {
		return err
	}
	return writer.Flush()
}

func writePromSuccessVector(w http.ResponseWriter, samples []model.InstantSample) error {
	startJSONResponse(w, http.StatusOK)
	writer := bufio.NewWriter(w)
	if _, err := writer.WriteString(`{"status":"success","data":{"resultType":"vector","result":[`); err != nil {
		return err
	}
	for index, sample := range samples {
		if index > 0 {
			if _, err := writer.WriteString(","); err != nil {
				return err
			}
		}
		if err := writeJSONValue(writer, map[string]any{"metric": sample.Metric, "value": []any{sample.Timestamp, formatPromValue(sample.Value)}}); err != nil {
			return err
		}
	}
	if _, err := writer.WriteString(`]}}` + "\n"); err != nil {
		return err
	}
	return writer.Flush()
}

func writePromSuccessMatrix(w http.ResponseWriter, series []model.RangeSeries) error {
	startJSONResponse(w, http.StatusOK)
	writer := bufio.NewWriter(w)
	if _, err := writer.WriteString(`{"status":"success","data":{"resultType":"matrix","result":[`); err != nil {
		return err
	}
	for index, item := range series {
		if index > 0 {
			if _, err := writer.WriteString(","); err != nil {
				return err
			}
		}
		values := make([][]any, 0, len(item.Values))
		for _, point := range item.Values {
			values = append(values, []any{point.Timestamp, formatPromValue(point.Value)})
		}
		if err := writeJSONValue(writer, map[string]any{"metric": item.Metric, "values": values}); err != nil {
			return err
		}
	}
	if _, err := writer.WriteString(`]}}` + "\n"); err != nil {
		return err
	}
	return writer.Flush()
}

func startJSONResponse(w http.ResponseWriter, statusCode int) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(statusCode)
}

func writeJSONString(writer *bufio.Writer, value string) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = writer.Write(encoded)
	return err
}

func writeJSONValue(writer *bufio.Writer, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = writer.Write(encoded)
	return err
}

func formatPromValue(value float64) string { return strconv.FormatFloat(value, 'f', -1, 64) }
