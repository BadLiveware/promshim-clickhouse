package promshim

import (
	"context"
	"net/http"
	"time"

	httpapi "github.com/BadLiveware/promshim-clickhouse/internal/promshim/httpapi"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/local"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/model"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/storage"
)

func (h *queryService) shouldServeDenseRateRollup(ctx context.Context, query string, start, end time.Time, step time.Duration) (string, bool) {
	metricName, ok := denseRateRollupMetricName(query, step)
	if h.opts.DenseRateRollups != "prefer" || !h.denseRateRollup.Available || !ok {
		return "", false
	}
	coverage, err := storage.DiscoverDenseRateRollupCoverage(ctx, h.client, h.queryConfig(), metricName)
	if err != nil {
		return "", false
	}
	return metricName, coverage.Covers(start.UnixMilli(), end.UnixMilli())
}

func (h *queryService) executeDenseRateRollupRange(ctx context.Context, metricName string, start, end time.Time) (model.MatrixValue, error) {
	sql, params := storage.BuildDenseRateRollupRangeQuerySQL(h.queryConfig(), metricName, start.UnixMilli(), end.UnixMilli())
	var series []model.RangeSeries
	var err error
	if h.client.TransportKind() == storage.TransportNative {
		series, err = h.client.QueryRangeSeries(ctx, storage.QueryRequest{SQL: sql, Params: params, Purpose: storage.QueryPurposeRange})
	} else {
		var response *http.Response
		response, err = h.client.Execute(ctx, sql, params)
		if err == nil {
			defer response.Body.Close()
			series, err = local.DecodeRangeSeries(response.Body)
		}
	}
	if err != nil {
		return model.MatrixValue{}, local.WithInternalContext(local.NormalizeInternalError(err), "executing optional dense rate rollup range")
	}
	return model.MatrixValue{Series: series}, nil
}

func denseRateRollupExplainNode(metricName string) local.ExplainNode {
	return local.ExplainNode{
		Kind:             "operator_rollup",
		Strategy:         "native_sql",
		NativeScope:      "optional_rollup",
		Expr:             "sum by (job) (rate(" + metricName + "[5m]))",
		Reason:           "explicit_dense_rate_rollup_gate",
		RenderedSQL:      "SELECT [tuple('job', job)] AS tags, arraySort(item -> item.1, groupArray((timestamp, value))) AS time_series FROM rollup_cpu_rate_5m_1m_by_job WHERE metric_name = 'demo_cpu_usage_seconds_total' AND timestamp BETWEEN start AND end GROUP BY job ORDER BY tags",
		RequiredColumns:  []string{"metric_name", "job", "timestamp", "value"},
		RulesApplied:     []string{"optional_dense_rate_rollup"},
		SemanticBarriers: []string{"exact_metric", "exact_grouping", "exact_rate_window", "exact_step", "coverage_probe", "explicit_gate", "raw_timeseries_fallback"},
	}
}

func markDenseRateRollupServed(routing *httpapi.RoutingInfo) {
	if routing == nil {
		return
	}
	strictCandidate := ""
	if routing.CandidateDecision != nil {
		strictCandidate = routing.CandidateDecision.StrictCandidate
	}
	routing.Decision = "rollup_override"
	routing.Reason = "optional_dense_rate_rollup_gate"
	routing.SelectedStrategy = "native_sql"
	routing.WouldSelect = "native_sql"
	routing.CandidateDecision = &httpapi.CandidateDecision{StrictCandidate: strictCandidate, SelectedCandidate: denseRateRollupCandidateID, ServedCandidate: denseRateRollupCandidateID}
	for i := range routing.Candidates {
		if routing.Candidates[i].ID == denseRateRollupCandidateID {
			routing.Candidates[i].Eligible = true
			routing.Candidates[i].Selected = true
			routing.Candidates[i].Served = true
			routing.Candidates[i].RejectReasons = nil
		}
	}
}
