package local

import (
	"context"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/model"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/storage"
)

func executeStorageInstantSamples(ctx context.Context, client *storage.Client, sql string, params map[string]string, purpose storage.QueryPurpose) ([]model.InstantSample, error) {
	if client.TransportKind() == storage.TransportNative {
		return client.QueryInstantSamples(ctx, storage.QueryRequest{SQL: sql, Params: params, Purpose: purpose})
	}
	response, err := client.Execute(ctx, sql, params)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	return DecodeInstantSamples(response.Body)
}

func executeStorageRangeSeries(ctx context.Context, client *storage.Client, sql string, params map[string]string, purpose storage.QueryPurpose) ([]model.RangeSeries, error) {
	if client.TransportKind() == storage.TransportNative {
		return client.QueryRangeSeries(ctx, storage.QueryRequest{SQL: sql, Params: params, Purpose: purpose})
	}
	response, err := client.Execute(ctx, sql, params)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	return DecodeRangeSeries(response.Body)
}
