package promshim

import (
	"testing"
	"time"

	httpapi "github.com/BadLiveware/promshim-clickhouse/internal/promshim/httpapi"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
)

func TestClassifyQueryCostFamilies(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		endpoint string
		want     string
		check    func(*testing.T, httpapi.QueryCostClass)
	}{
		{
			name:     "selector",
			query:    `up{job="api"}`,
			endpoint: "query",
			want:     "selector",
			check: func(t *testing.T, got httpapi.QueryCostClass) {
				if got.SelectorCount != 1 {
					t.Fatalf("selector count = %d, want 1", got.SelectorCount)
				}
			},
		},
		{
			name:     "range selector",
			query:    `up{job="api"}`,
			endpoint: "query_range",
			want:     "range_selector",
			check: func(t *testing.T, got httpapi.QueryCostClass) {
				if got.RangePointsPerSeries != 6 {
					t.Fatalf("range points = %d, want 6", got.RangePointsPerSeries)
				}
			},
		},
		{
			name:     "aggregation",
			query:    `sum by (job) (up)`,
			endpoint: "query",
			want:     "aggregation",
			check: func(t *testing.T, got httpapi.QueryCostClass) {
				if !got.HasAggregation || got.DropsAllLabels {
					t.Fatalf("aggregation metadata = %+v", got)
				}
			},
		},
		{
			name:     "selection aggregation",
			query:    `topk(3, up)`,
			endpoint: "query",
			want:     "selection_aggregation",
			check: func(t *testing.T, got httpapi.QueryCostClass) {
				if !got.HasAggregation || !got.HasSelectionAgg {
					t.Fatalf("selection aggregation metadata = %+v", got)
				}
			},
		},
		{
			name:     "range rate",
			query:    `rate(up[5m])`,
			endpoint: "query_range",
			want:     "range_rate",
			check: func(t *testing.T, got httpapi.QueryCostClass) {
				if !got.HasRangeFunction || got.LookbackMS != int64((5*time.Minute).Milliseconds()) {
					t.Fatalf("range metadata = hasRangeFunction:%v lookback:%d", got.HasRangeFunction, got.LookbackMS)
				}
				if got.RangePointsPerSeries != 6 {
					t.Fatalf("range points = %d, want 6", got.RangePointsPerSeries)
				}
			},
		},
		{
			name:     "histogram",
			query:    `histogram_quantile(0.9, sum by (le) (rate(bucket[5m])))`,
			endpoint: "query",
			want:     "histogram_quantile",
			check: func(t *testing.T, got httpapi.QueryCostClass) {
				if !got.HasHistogram || !got.HasAggregation {
					t.Fatalf("histogram/aggregation metadata missing: %+v", got)
				}
			},
		},
		{
			name:     "range function",
			query:    `avg_over_time(up[5m])`,
			endpoint: "query",
			want:     "range_function",
			check: func(t *testing.T, got httpapi.QueryCostClass) {
				if !got.HasRangeFunction || got.LookbackMS != int64((5*time.Minute).Milliseconds()) {
					t.Fatalf("range function metadata = %+v", got)
				}
			},
		},
		{
			name:     "increase",
			query:    `increase(up[5m])`,
			endpoint: "query",
			want:     "increase",
			check: func(t *testing.T, got httpapi.QueryCostClass) {
				if !got.HasRangeFunction {
					t.Fatalf("increase metadata = %+v", got)
				}
			},
		},
		{
			name:     "binary scalar vector",
			query:    `up * 1.5`,
			endpoint: "query",
			want:     "binary",
			check: func(t *testing.T, got httpapi.QueryCostClass) {
				if got.HasVectorJoin {
					t.Fatalf("binary scalar/vector should not be vector join: %+v", got)
				}
			},
		},
		{
			name:     "binary default vector matching",
			query:    `rate(up[5m]) + rate(up[5m])`,
			endpoint: "query",
			want:     "binary",
			check: func(t *testing.T, got httpapi.QueryCostClass) {
				if got.HasVectorJoin {
					t.Fatalf("default one-to-one binary matching should not be high-cardinality vector join: %+v", got)
				}
				if got.SelectorCount != 2 {
					t.Fatalf("selector count = %d", got.SelectorCount)
				}
				if !got.HasRepeatedRangeFunc {
					t.Fatalf("expected repeated range function metadata: %+v", got)
				}
			},
		},
		{
			name:     "binary non-repeated rate operands",
			query:    `rate(up[5m]) + rate(other_metric[5m])`,
			endpoint: "query",
			want:     "binary",
			check: func(t *testing.T, got httpapi.QueryCostClass) {
				if got.HasRepeatedRangeFunc {
					t.Fatalf("different rate operands should not be marked repeated: %+v", got)
				}
			},
		},
		{
			name:     "binary explicit on matching",
			query:    `up + on(job) other_metric`,
			endpoint: "query",
			want:     "vector_match",
			check: func(t *testing.T, got httpapi.QueryCostClass) {
				if !got.HasVectorJoin {
					t.Fatalf("explicit vector matching should be classified as vector join: %+v", got)
				}
			},
		},
		{
			name:     "label mutation",
			query:    `label_replace(up, "job_copy", "$1", "job", "(.+)")`,
			endpoint: "query",
			want:     "label_mutation",
			check: func(t *testing.T, got httpapi.QueryCostClass) {
				if !got.HasLabelMutation {
					t.Fatalf("label mutation metadata = %+v", got)
				}
			},
		},
		{
			name:     "vector match",
			query:    `up * on(job) group_left(instance) other_metric`,
			endpoint: "query",
			want:     "vector_match",
			check: func(t *testing.T, got httpapi.QueryCostClass) {
				if !got.HasVectorJoin {
					t.Fatalf("expected vector join metadata: %+v", got)
				}
			},
		},
		{
			name:     "subquery",
			query:    `rate(up[5m])[30m:1m]`,
			endpoint: "query",
			want:     "subquery",
			check: func(t *testing.T, got httpapi.QueryCostClass) {
				if !got.HasSubquery {
					t.Fatalf("expected subquery metadata: %+v", got)
				}
				if got.SubqueryRangeMS != int64((30 * time.Minute).Milliseconds()) {
					t.Fatalf("subquery range ms = %d, want %d", got.SubqueryRangeMS, int64((30*time.Minute).Milliseconds()))
				}
				if got.SubqueryStepMS != int64(time.Minute.Milliseconds()) {
					t.Fatalf("subquery step ms = %d, want %d", got.SubqueryStepMS, int64(time.Minute.Milliseconds()))
				}
				if got.SubqueryPointsPerEval != 31 {
					t.Fatalf("subquery points per eval = %d, want 31", got.SubqueryPointsPerEval)
				}
				if got.SubqueryOverlapSlots != 30 {
					t.Fatalf("subquery overlap slots = %v, want 30", got.SubqueryOverlapSlots)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expr, err := logical.ParseExpression(tt.query)
			if err != nil {
				t.Fatal(err)
			}
			got := classifyQueryCost(expr, queryCostTiming{Endpoint: tt.endpoint, Start: time.Unix(0, 0), End: time.Unix(300, 0), Step: time.Minute}, "native_sql")
			if got.Family != tt.want {
				t.Fatalf("family = %q, want %q (class=%+v)", got.Family, tt.want, got)
			}
			tt.check(t, got)
		})
	}
}
