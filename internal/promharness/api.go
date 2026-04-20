package promharness

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type apiEnvelope struct {
	Status    string `json:"status"`
	ErrorType string `json:"errorType"`
	Error     string `json:"error"`
	Data      struct {
		ResultType string          `json:"resultType"`
		Result     json.RawMessage `json:"result"`
	} `json:"data"`
}

type queryResult struct {
	Status    string
	ErrorType string
	Error     string
	Data      normalizedResult
}

type normalizedResult struct {
	ResultType string
	Scalar     *normalizedScalar
	Vector     []normalizedVectorSample
	Matrix     []normalizedMatrixSeries
}

type normalizedScalar struct {
	Timestamp float64
	Value     float64
}

type normalizedVectorSample struct {
	Metric    map[string]string
	Timestamp float64
	Value     float64
}

type normalizedMatrixSeries struct {
	Metric map[string]string
	Values []normalizedPoint
}

type normalizedPoint struct {
	Timestamp float64
	Value     float64
}

func QueryAndNormalize(client *http.Client, baseURL string, manifest Manifest, spec QuerySpec) (normalizedResult, error) {
	result, err := QueryAndFetch(client, baseURL, manifest, spec)
	if err != nil {
		return normalizedResult{}, err
	}
	if result.Status != "success" {
		return normalizedResult{}, fmt.Errorf("query %q failed: %s: %s", spec.Name, result.ErrorType, result.Error)
	}
	return result.Data, nil
}

func QueryAndFetch(client *http.Client, baseURL string, manifest Manifest, spec QuerySpec) (queryResult, error) {
	endpoint, err := buildQueryURL(baseURL, manifest, spec)
	if err != nil {
		return queryResult{}, err
	}
	response, err := client.Get(endpoint)
	if err != nil {
		return queryResult{}, err
	}
	defer response.Body.Close()
	var envelope apiEnvelope
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		return queryResult{}, err
	}
	if envelope.Status != "success" {
		return queryResult{Status: "error", ErrorType: envelope.ErrorType, Error: envelope.Error}, nil
	}
	normalized, err := normalizeAPIResult(envelope.Data.ResultType, envelope.Data.Result)
	if err != nil {
		return queryResult{}, err
	}
	return queryResult{Status: "success", Data: normalized}, nil
}

func buildQueryURL(baseURL string, manifest Manifest, spec QuerySpec) (string, error) {
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil {
		return "", err
	}
	base := time.Unix(manifest.BaseUnixSeconds, 0).UTC()
	query := parsed.Query()
	query.Set("query", spec.Query)
	switch spec.Endpoint {
	case "query":
		parsed.Path = "/api/v1/query"
		query.Set("time", formatTime(base.Add(time.Duration(spec.TimeOffsetSeconds)*time.Second)))
	case "query_range":
		parsed.Path = "/api/v1/query_range"
		query.Set("start", formatTime(base.Add(time.Duration(spec.StartOffsetSeconds)*time.Second)))
		query.Set("end", formatTime(base.Add(time.Duration(spec.EndOffsetSeconds)*time.Second)))
		query.Set("step", fmt.Sprintf("%ds", spec.StepSeconds))
	default:
		return "", fmt.Errorf("unsupported endpoint %q", spec.Endpoint)
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func normalizeAPIResult(resultType string, raw json.RawMessage) (normalizedResult, error) {
	result := normalizedResult{ResultType: resultType}
	switch resultType {
	case "scalar":
		var payload []any
		if err := json.Unmarshal(raw, &payload); err != nil {
			return normalizedResult{}, err
		}
		if len(payload) != 2 {
			return normalizedResult{}, fmt.Errorf("unexpected scalar payload shape: %#v", payload)
		}
		timestamp, err := toFloat64(payload[0])
		if err != nil {
			return normalizedResult{}, err
		}
		value, err := toFloat64(payload[1])
		if err != nil {
			return normalizedResult{}, err
		}
		result.Scalar = &normalizedScalar{Timestamp: timestamp, Value: value}
		return result, nil
	case "vector":
		var rows []struct {
			Metric map[string]string `json:"metric"`
			Value  []any             `json:"value"`
		}
		if err := json.Unmarshal(raw, &rows); err != nil {
			return normalizedResult{}, err
		}
		result.Vector = make([]normalizedVectorSample, 0, len(rows))
		for _, row := range rows {
			timestamp, err := toFloat64(row.Value[0])
			if err != nil {
				return normalizedResult{}, err
			}
			value, err := toFloat64(row.Value[1])
			if err != nil {
				return normalizedResult{}, err
			}
			result.Vector = append(result.Vector, normalizedVectorSample{Metric: row.Metric, Timestamp: timestamp, Value: value})
		}
		sort.Slice(result.Vector, func(i, j int) bool {
			return labelKey(result.Vector[i].Metric) < labelKey(result.Vector[j].Metric)
		})
		return result, nil
	case "matrix":
		var rows []struct {
			Metric map[string]string `json:"metric"`
			Values [][]any           `json:"values"`
		}
		if err := json.Unmarshal(raw, &rows); err != nil {
			return normalizedResult{}, err
		}
		result.Matrix = make([]normalizedMatrixSeries, 0, len(rows))
		for _, row := range rows {
			series := normalizedMatrixSeries{Metric: row.Metric, Values: make([]normalizedPoint, 0, len(row.Values))}
			for _, point := range row.Values {
				timestamp, err := toFloat64(point[0])
				if err != nil {
					return normalizedResult{}, err
				}
				value, err := toFloat64(point[1])
				if err != nil {
					return normalizedResult{}, err
				}
				series.Values = append(series.Values, normalizedPoint{Timestamp: timestamp, Value: value})
			}
			result.Matrix = append(result.Matrix, series)
		}
		sort.Slice(result.Matrix, func(i, j int) bool {
			return labelKey(result.Matrix[i].Metric) < labelKey(result.Matrix[j].Metric)
		})
		return result, nil
	default:
		return normalizedResult{}, fmt.Errorf("unsupported resultType %q", resultType)
	}
}

func formatTime(value time.Time) string {
	return value.Format(time.RFC3339)
}

func toFloat64(value any) (float64, error) {
	switch typed := value.(type) {
	case float64:
		return typed, nil
	case string:
		if strings.EqualFold(typed, "nan") {
			return math.NaN(), nil
		}
		return strconv.ParseFloat(typed, 64)
	default:
		return 0, fmt.Errorf("cannot parse float from %T (%v)", value, value)
	}
}

func labelKey(metric map[string]string) string {
	if len(metric) == 0 {
		return ""
	}
	keys := make([]string, 0, len(metric))
	for key := range metric {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var builder strings.Builder
	for _, key := range keys {
		builder.WriteString(key)
		builder.WriteByte('\xff')
		builder.WriteString(metric[key])
		builder.WriteByte('\xfe')
	}
	return builder.String()
}
