package promshim

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/routingmetrics"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/storage"
)

func (h *queryService) probeSelectorStats(ctx context.Context, sig selectorSignature, budget time.Duration) (selectorStats, error) {
	if h == nil || h.client == nil {
		return selectorStats{}, fmt.Errorf("selector stats probe requires a ClickHouse client")
	}
	if budget <= 0 {
		budget = 250 * time.Millisecond
	}
	probeCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	request, err := selectorStatsHTTPRequest(probeCtx, sig)
	if err != nil {
		return selectorStats{}, err
	}
	sql, params, err := storage.BuildSelectorStatsQuery(h.queryConfig(), request)
	if err != nil {
		routingmetrics.ObserveStatsProbe("selector", "build_error")
		return selectorStats{}, err
	}
	stats, err := h.client.QuerySelectorStats(probeCtx, storage.QueryRequest{SQL: sql, Params: params, Purpose: storage.QueryPurposeSelectorStats})
	if err != nil {
		routingmetrics.ObserveStatsProbe("selector", "query_error")
		return selectorStats{}, err
	}
	routingmetrics.ObserveStatsProbe("selector", "success")
	return selectorStats{MatchedSeries: stats.MatchedSeries, SamplesPerSeries: estimateSamplesPerSeries(sig), ObservedAt: time.Now().UTC()}, nil
}

func selectorStatsHTTPRequest(ctx context.Context, sig selectorSignature) (*http.Request, error) {
	values := url.Values{}
	for _, matcher := range sig.Matchers {
		values.Add("match[]", "{"+matcher+"}")
	}
	values.Set("start", prometheusUnixSeconds(sig.StartMS))
	values.Set("end", prometheusUnixSeconds(sig.EndMS))
	return http.NewRequestWithContext(ctx, http.MethodGet, "/?"+values.Encode(), nil)
}

func prometheusUnixSeconds(ms int64) string {
	seconds := float64(ms) / 1000.0
	return strconv.FormatFloat(seconds, 'f', -1, 64)
}
