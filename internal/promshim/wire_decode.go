package promshim

import (
	"encoding/json"
	"io"
	"net/http"
)

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
