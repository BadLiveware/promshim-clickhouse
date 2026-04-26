package promshim

import (
	"time"

	httpapi "github.com/BadLiveware/promshim-clickhouse/internal/promshim/httpapi"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/storage"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

const denseRateRollupCandidateID = "optional_rollup_cpu_rate_5m_1m_by_job"

func (h *queryService) attachDenseRateRollupCandidate(info *httpapi.RoutingInfo, query string, step time.Duration) {
	if info == nil {
		return
	}
	candidate := httpapi.ExecutionCandidate{
		ID:                 denseRateRollupCandidateID,
		Tier:               "operator_rollup",
		Strategy:           "native_sql",
		Family:             info.Class.Family,
		Strict:             false,
		Selected:           false,
		Served:             false,
		EstimatesAvailable: true,
	}
	if !h.denseRateRollup.Available {
		candidate.RejectReasons = append(candidate.RejectReasons, "rollup_not_detected")
	} else if _, ok := denseRateRollupMetricName(query, step); !ok {
		candidate.RejectReasons = append(candidate.RejectReasons, "shape_mismatch")
	} else {
		candidate.Supported = true
		candidate.KnownCorrect = true
		if h.opts.DenseRateRollups == "prefer" {
			candidate.RejectReasons = append(candidate.RejectReasons, "coverage_unverified")
		} else {
			candidate.RejectReasons = append(candidate.RejectReasons, "gate_disabled")
		}
	}
	info.Candidates = append(info.Candidates, candidate)
}

func matchesDenseRateRollupQuery(query string, step time.Duration) bool {
	_, ok := denseRateRollupMetricName(query, step)
	return ok
}

func denseRateRollupMetricName(query string, step time.Duration) (string, bool) {
	if step != time.Minute {
		return "", false
	}
	expr, err := logical.ParseExpression(query)
	if err != nil {
		return "", false
	}
	agg, ok := expr.(*parser.AggregateExpr)
	if !ok || agg.Op != parser.SUM || agg.Without || len(agg.Grouping) != 1 || agg.Grouping[0] != "job" {
		return "", false
	}
	call, ok := agg.Expr.(*parser.Call)
	if !ok || call.Func == nil || call.Func.Name != "rate" || len(call.Args) != 1 {
		return "", false
	}
	matrix, ok := call.Args[0].(*parser.MatrixSelector)
	if !ok || matrix.Range != 5*time.Minute {
		return "", false
	}
	vector, ok := matrix.VectorSelector.(*parser.VectorSelector)
	if !ok || vector.Name == "" {
		return "", false
	}
	for _, matcher := range vector.LabelMatchers {
		if matcher == nil {
			continue
		}
		if matcher.Name != labels.MetricName {
			return "", false
		}
	}
	return vector.Name, true
}

func denseRateRollupDiscoveryForTest(available bool) storage.DenseRateRollupDiscovery {
	if available {
		return storage.DenseRateRollupDiscovery{Table: storage.DenseRateRollupTableName, Available: true, ColumnsPresent: []string{"job", "metric_name", "timestamp", "value"}}
	}
	return storage.DenseRateRollupDiscovery{Table: storage.DenseRateRollupTableName, Available: false, MissingColumns: []string{"job", "metric_name", "timestamp", "value"}}
}
