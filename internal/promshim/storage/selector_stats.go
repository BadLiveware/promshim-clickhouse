package storage

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

type SelectorStats struct {
	MatchedSeries int64 `json:"matched_series"`
}

func (c *Client) QuerySelectorStats(ctx context.Context, req QueryRequest) (stats SelectorStats, err error) {
	if req.Purpose == "" {
		req.Purpose = QueryPurposeSelectorStats
	}
	if c.transportKind == TransportNative {
		nativeTransport, ok := c.transport.(*NativeDriverTransport)
		if !ok {
			return SelectorStats{}, fmt.Errorf("typed selector stats decoding requires %s transport, got %s", TransportNative, c.transportKind)
		}
		prepared, err := c.prepareQueryRequest(req)
		if err != nil {
			return SelectorStats{}, err
		}
		rows, err := nativeTransport.QueryNativeRows(ctx, prepared)
		if err != nil {
			return SelectorStats{}, err
		}
		defer func() { _ = rows.Close() }()
		if rows.Next() {
			var count uint64
			if err := rows.Scan(&count); err != nil {
				return SelectorStats{}, fmt.Errorf("scan selector stats row: %w", err)
			}
			stats.MatchedSeries = int64(count)
		}
		if rowsErr := rows.Err(); rowsErr != nil {
			return SelectorStats{}, fmt.Errorf("read selector stats rows: %w", rowsErr)
		}
		return stats, nil
	}
	rows, err := c.Query(ctx, req)
	if err != nil {
		return SelectorStats{}, err
	}
	defer func() { _ = rows.Close() }()
	return decodeSelectorStats(rows)
}

func decodeSelectorStats(reader io.Reader) (SelectorStats, error) {
	start := time.Now()
	decoded := 0
	var err error
	defer func() { observeDecode(TransportHTTP, QueryPurposeSelectorStats, decoded, time.Since(start), err) }()
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var row SelectorStats
		if err = json.Unmarshal(line, &row); err != nil {
			return SelectorStats{}, fmt.Errorf("decode selector stats row: %w", err)
		}
		decoded++
		return row, nil
	}
	if scanErr := scanner.Err(); scanErr != nil {
		err = scanErr
		return SelectorStats{}, fmt.Errorf("read selector stats rows: %w", scanErr)
	}
	return SelectorStats{}, nil
}
