package storage

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/model"
)

const (
	QueryPurposeInstant          QueryPurpose = "instant"
	QueryPurposeRange            QueryPurpose = "range"
	QueryPurposeDelegatedInstant QueryPurpose = "delegated_instant"
	QueryPurposeDelegatedRange   QueryPurpose = "delegated_range"
)

func (c *Client) QueryInstantSamples(ctx context.Context, req QueryRequest) (samples []model.InstantSample, err error) {
	nativeTransport, ok := c.transport.(*NativeDriverTransport)
	if !ok {
		return nil, fmt.Errorf("typed instant row decoding requires %s transport, got %s", TransportNative, c.transportKind)
	}
	rows, err := nativeTransport.QueryNativeRows(ctx, req)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	start := time.Now()
	decoded := 0
	defer func() { observeDecode(TransportNative, req.Purpose, decoded, time.Since(start), err) }()
	samples = make([]model.InstantSample, 0, 16)
	columnTypes := rows.ColumnTypes()
	if len(columnTypes) < 3 {
		return nil, fmt.Errorf("instant rows returned %d columns, expected at least 3", len(columnTypes))
	}
	for rows.Next() {
		var (
			tags      [][]string
			timestamp time.Time
		)
		valueDest, value := nativeValueDestination(columnTypes[2].DatabaseTypeName())
		if err := rows.Scan(&tags, &timestamp, valueDest); err != nil {
			return nil, fmt.Errorf("scan instant row: %w", err)
		}
		floatValue, err := value()
		if err != nil {
			return nil, fmt.Errorf("unexpected instant row value: %w", err)
		}
		samples = append(samples, model.InstantSample{Metric: tagsToObject(tags), Timestamp: timestampToPromSeconds(timestamp), Value: floatValue})
		decoded++
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("read instant rows: %w", rowsErr)
	}
	return samples, nil
}

func (c *Client) QueryRangeSeries(ctx context.Context, req QueryRequest) (series []model.RangeSeries, err error) {
	nativeTransport, ok := c.transport.(*NativeDriverTransport)
	if !ok {
		return nil, fmt.Errorf("typed range row decoding requires %s transport, got %s", TransportNative, c.transportKind)
	}
	rows, err := nativeTransport.QueryNativeRows(ctx, req)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	start := time.Now()
	decoded := 0
	defer func() { observeDecode(TransportNative, req.Purpose, decoded, time.Since(start), err) }()
	series = make([]model.RangeSeries, 0, 16)
	for rows.Next() {
		var (
			tags      [][]string
			rawPoints [][]any
		)
		if err := rows.Scan(&tags, &rawPoints); err != nil {
			return nil, fmt.Errorf("scan range row: %w", err)
		}
		points, err := decodeNativeRangePoints(rawPoints)
		if err != nil {
			return nil, err
		}
		series = append(series, model.RangeSeries{Metric: tagsToObject(tags), Values: points})
		decoded++
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("read range rows: %w", rowsErr)
	}
	return series, nil
}

func decodeNativeRangePoints(rawPoints [][]any) ([]model.RangePoint, error) {
	points := make([]model.RangePoint, 0, len(rawPoints))
	for _, rawPoint := range rawPoints {
		if len(rawPoint) != 2 {
			return nil, fmt.Errorf("unexpected native range point shape with %d elements", len(rawPoint))
		}
		timestamp, ok := rawPoint[0].(time.Time)
		if !ok {
			return nil, fmt.Errorf("unexpected native range point timestamp type %T", rawPoint[0])
		}
		value, err := nativeFloat64(rawPoint[1])
		if err != nil {
			return nil, fmt.Errorf("unexpected native range point value: %w", err)
		}
		points = append(points, model.RangePoint{Timestamp: timestampToPromSeconds(timestamp), Value: value})
	}
	return points, nil
}

func nativeValueDestination(databaseType string) (any, func() (float64, error)) {
	normalized := strings.ToLower(databaseType)
	if strings.Contains(normalized, "float32") {
		var value float32
		return &value, func() (float64, error) { return float64(value), nil }
	}
	if strings.Contains(normalized, "float64") {
		var value float64
		return &value, func() (float64, error) { return value, nil }
	}
	if strings.Contains(normalized, "uint8") {
		var value uint8
		return &value, func() (float64, error) { return float64(value), nil }
	}
	if strings.Contains(normalized, "uint16") {
		var value uint16
		return &value, func() (float64, error) { return float64(value), nil }
	}
	if strings.Contains(normalized, "uint32") {
		var value uint32
		return &value, func() (float64, error) { return float64(value), nil }
	}
	if strings.Contains(normalized, "uint64") {
		var value uint64
		return &value, func() (float64, error) { return float64(value), nil }
	}
	if strings.Contains(normalized, "int8") {
		var value int8
		return &value, func() (float64, error) { return float64(value), nil }
	}
	if strings.Contains(normalized, "int16") {
		var value int16
		return &value, func() (float64, error) { return float64(value), nil }
	}
	if strings.Contains(normalized, "int32") {
		var value int32
		return &value, func() (float64, error) { return float64(value), nil }
	}
	if strings.Contains(normalized, "int64") {
		var value int64
		return &value, func() (float64, error) { return float64(value), nil }
	}
	var value float64
	return &value, func() (float64, error) {
		return value, fmt.Errorf("unsupported type %q", databaseType)
	}
}

func timestampToPromSeconds(timestamp time.Time) float64 {
	return float64(timestamp.UTC().UnixNano()) / float64(time.Second)
}

func nativeFloat64(value any) (float64, error) {
	switch v := value.(type) {
	case nil:
		return math.NaN(), nil
	case float64:
		return v, nil
	case *float64:
		return derefNumber(v)
	case float32:
		return float64(v), nil
	case *float32:
		return derefNumber(v)
	case int:
		return float64(v), nil
	case *int:
		return derefNumber(v)
	case int8:
		return float64(v), nil
	case *int8:
		return derefNumber(v)
	case int16:
		return float64(v), nil
	case *int16:
		return derefNumber(v)
	case int32:
		return float64(v), nil
	case *int32:
		return derefNumber(v)
	case int64:
		return float64(v), nil
	case *int64:
		return derefNumber(v)
	case uint:
		return float64(v), nil
	case *uint:
		return derefNumber(v)
	case uint8:
		return float64(v), nil
	case *uint8:
		return derefNumber(v)
	case uint16:
		return float64(v), nil
	case *uint16:
		return derefNumber(v)
	case uint32:
		return float64(v), nil
	case *uint32:
		return derefNumber(v)
	case uint64:
		return float64(v), nil
	case *uint64:
		return derefNumber(v)
	default:
		return 0, fmt.Errorf("unsupported type %T", value)
	}
}

func derefNumber[T ~float32 | ~float64 | ~int | ~int8 | ~int16 | ~int32 | ~int64 | ~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64](value *T) (float64, error) {
	if value == nil {
		return math.NaN(), nil
	}
	return float64(*value), nil
}
