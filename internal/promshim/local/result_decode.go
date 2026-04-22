package promshim

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"

	"ch-observability/internal/promshim/model"
)

func newScanner(body io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(body)
	buffer := make([]byte, 0, 64*1024)
	scanner.Buffer(buffer, 8*1024*1024)
	return scanner
}

type instantRow struct {
	Tags      [][]string      `json:"tags"`
	Timestamp string          `json:"timestamp"`
	Value     json.RawMessage `json:"value"`
	Scalar    json.RawMessage `json:"scalar"`
	String    json.RawMessage `json:"string"`
}

type matrixRow struct {
	Tags       [][]string          `json:"tags"`
	TimeSeries [][]json.RawMessage `json:"time_series"`
}

type labelRow struct {
	Label string `json:"label"`
}

type valueRow struct {
	Value string `json:"value"`
}

type tagsRow struct {
	Tags [][]string `json:"tags"`
}

func tagsToObject(tags [][]string) map[string]string {
	metric := make(map[string]string, len(tags))
	for _, tag := range tags {
		if len(tag) != 2 {
			continue
		}
		metric[tag[0]] = tag[1]
	}
	return metric
}

func decodeStringRows[T any](body io.Reader, project func(T) string) ([]string, *apiError) {
	rows := make([]string, 0, 16)
	scanner := newScanner(body)
	for scanner.Scan() {
		var row T
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			return nil, &apiError{StatusCode: http.StatusBadGateway, ErrorType: "execution", Error: err.Error()}
		}
		rows = append(rows, project(row))
	}
	if err := scanner.Err(); err != nil {
		return nil, &apiError{StatusCode: http.StatusBadGateway, ErrorType: "execution", Error: err.Error()}
	}
	return rows, nil
}

func decodeSeriesRows(body io.Reader) ([]map[string]string, *apiError) {
	rows := make([]map[string]string, 0, 16)
	scanner := newScanner(body)
	for scanner.Scan() {
		var row tagsRow
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			return nil, &apiError{StatusCode: http.StatusBadGateway, ErrorType: "execution", Error: err.Error()}
		}
		rows = append(rows, tagsToObject(row.Tags))
	}
	if err := scanner.Err(); err != nil {
		return nil, &apiError{StatusCode: http.StatusBadGateway, ErrorType: "execution", Error: err.Error()}
	}
	return rows, nil
}

func decodeInstantSamples(body io.Reader) ([]model.InstantSample, error) {
	samples := make([]model.InstantSample, 0, 16)
	scanner := newScanner(body)
	for scanner.Scan() {
		var row instantRow
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			return nil, newExecutionErrorf("failed to decode instant row JSON: %v", err)
		}
		timestamp, err := model.ParseClickHouseTimestamp(row.Timestamp)
		if err != nil {
			return nil, newExecutionErrorf("failed to parse instant row timestamp %q: %v", row.Timestamp, err)
		}
		value, err := rawPromValueToFloat64(row.Value)
		if err != nil {
			return nil, withInternalContext(err, "failed to parse instant row value %q", string(row.Value))
		}
		samples = append(samples, model.InstantSample{Metric: tagsToObject(row.Tags), Timestamp: timestamp, Value: value})
	}
	if err := scanner.Err(); err != nil {
		return nil, newExecutionErrorf("failed while scanning instant result rows: %v", err)
	}
	return samples, nil
}

func decodeRangeSeries(body io.Reader) ([]model.RangeSeries, error) {
	series := make([]model.RangeSeries, 0, 16)
	scanner := newScanner(body)
	for scanner.Scan() {
		var row matrixRow
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			return nil, newExecutionErrorf("failed to decode range row JSON: %v", err)
		}
		values := make([]model.RangePoint, 0, len(row.TimeSeries))
		for _, sample := range row.TimeSeries {
			if len(sample) != 2 {
				return nil, newExecutionErrorf("unexpected time_series row shape with %d elements", len(sample))
			}
			var timestampRaw string
			if err := json.Unmarshal(sample[0], &timestampRaw); err != nil {
				return nil, newExecutionErrorf("failed to decode range sample timestamp: %v", err)
			}
			timestamp, err := model.ParseClickHouseTimestamp(timestampRaw)
			if err != nil {
				return nil, newExecutionErrorf("failed to parse range sample timestamp %q: %v", timestampRaw, err)
			}
			value, err := rawPromValueToFloat64(sample[1])
			if err != nil {
				return nil, withInternalContext(err, "failed to parse range sample value %q", string(sample[1]))
			}
			values = append(values, model.RangePoint{Timestamp: timestamp, Value: value})
		}
		series = append(series, model.RangeSeries{Metric: tagsToObject(row.Tags), Values: values})
	}
	if err := scanner.Err(); err != nil {
		return nil, newExecutionErrorf("failed while scanning range result rows: %v", err)
	}
	return series, nil
}

func rawPromValueToFloat64(raw json.RawMessage) (float64, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return math.NaN(), nil
	}
	if raw[0] == '"' {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return 0, err
		}
		normalized := strings.ToLower(text)
		if normalized == "nan" || normalized == "-nan" || normalized == "+nan" {
			return math.NaN(), nil
		}
		return strconv.ParseFloat(text, 64)
	}
	if raw[0] == '{' || raw[0] == '[' {
		return 0, newUnsupportedErrorf("native histogram values are not supported yet")
	}
	var value float64
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, err
	}
	return value, nil
}
