package storage

import (
	"context"
	"fmt"
	"time"
)

const (
	QueryPurposeMetadataLabels      QueryPurpose = "metadata_labels"
	QueryPurposeMetadataLabelValues QueryPurpose = "metadata_label_values"
	QueryPurposeMetadataSeries      QueryPurpose = "metadata_series"
)

func (c *Client) QueryStringRows(ctx context.Context, req QueryRequest) (values []string, err error) {
	nativeTransport, ok := c.transport.(*NativeDriverTransport)
	if !ok {
		return nil, fmt.Errorf("typed string row decoding requires %s transport, got %s", TransportNative, c.transportKind)
	}
	rows, err := nativeTransport.QueryNativeRows(ctx, req)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	start := time.Now()
	decoded := 0
	defer func() { observeDecode(TransportNative, req.Purpose, decoded, time.Since(start), err) }()
	values = make([]string, 0, 16)
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, fmt.Errorf("scan string metadata row: %w", err)
		}
		values = append(values, value)
		decoded++
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("read string metadata rows: %w", rowsErr)
	}
	return values, nil
}

func (c *Client) QuerySeriesRows(ctx context.Context, req QueryRequest) (series []map[string]string, err error) {
	nativeTransport, ok := c.transport.(*NativeDriverTransport)
	if !ok {
		return nil, fmt.Errorf("typed series row decoding requires %s transport, got %s", TransportNative, c.transportKind)
	}
	rows, err := nativeTransport.QueryNativeRows(ctx, req)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	start := time.Now()
	decoded := 0
	defer func() { observeDecode(TransportNative, req.Purpose, decoded, time.Since(start), err) }()
	series = make([]map[string]string, 0, 16)
	for rows.Next() {
		var tags [][]string
		if err := rows.Scan(&tags); err != nil {
			return nil, fmt.Errorf("scan series metadata row: %w", err)
		}
		series = append(series, tagsToObject(tags))
		decoded++
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("read series metadata rows: %w", rowsErr)
	}
	return series, nil
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
