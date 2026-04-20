package promshim

import (
	"encoding/json"
	"io"
	"math"
	"strconv"
	"strings"

	"ch-observability/internal/promshim/model"
)

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
			return nil, newExecutionErrorf("failed to parse instant row value %s: %v", string(row.Value), err)
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
				return nil, newExecutionErrorf("failed to parse range sample value %s: %v", string(sample[1]), err)
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
	if len(raw) == 0 || string(raw) == "null" {
		return math.NaN(), nil
	}
	if raw[0] == '"' {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return 0, err
		}
		if strings.EqualFold(text, "nan") {
			return math.NaN(), nil
		}
		return strconv.ParseFloat(text, 64)
	}
	var value float64
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, err
	}
	return value, nil
}
