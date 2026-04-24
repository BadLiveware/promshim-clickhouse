package storage

import (
	"context"
	"fmt"
)

const (
	QueryPurposeMetadataLabels      QueryPurpose = "metadata_labels"
	QueryPurposeMetadataLabelValues QueryPurpose = "metadata_label_values"
	QueryPurposeMetadataSeries      QueryPurpose = "metadata_series"
)

func (c *Client) QueryStringRows(ctx context.Context, req QueryRequest) ([]string, error) {
	nativeTransport, ok := c.transport.(*NativeDriverTransport)
	if !ok {
		return nil, fmt.Errorf("typed string row decoding requires %s transport, got %s", TransportNative, c.transportKind)
	}
	rows, err := nativeTransport.QueryNativeRows(ctx, req)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	values := make([]string, 0, 16)
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, fmt.Errorf("scan string metadata row: %w", err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read string metadata rows: %w", err)
	}
	return values, nil
}

func (c *Client) QuerySeriesRows(ctx context.Context, req QueryRequest) ([]map[string]string, error) {
	nativeTransport, ok := c.transport.(*NativeDriverTransport)
	if !ok {
		return nil, fmt.Errorf("typed series row decoding requires %s transport, got %s", TransportNative, c.transportKind)
	}
	rows, err := nativeTransport.QueryNativeRows(ctx, req)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	series := make([]map[string]string, 0, 16)
	for rows.Next() {
		var tags [][]string
		if err := rows.Scan(&tags); err != nil {
			return nil, fmt.Errorf("scan series metadata row: %w", err)
		}
		series = append(series, tagsToObject(tags))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read series metadata rows: %w", err)
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
