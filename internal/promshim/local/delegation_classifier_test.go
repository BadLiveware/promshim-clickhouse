package local

import (
	"strings"
	"testing"

	logicalpkg "github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
)

func TestClassifyEntireQueryDelegationAcceptsAllowedRoots(t *testing.T) {
	tests := []struct {
		name  string
		query string
	}{
		{name: "bare selector", query: `up{job="api"}`},
		{name: "offset selector", query: `up offset 5m`},
		{name: "paren selector", query: `(up{job="api"})`},
		{name: "matrix selector", query: `up[5m]`},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			expr, err := logicalpkg.ParseExpression(tc.query)
			if err != nil {
				t.Fatal(err)
			}
			result := ClassifyEntireQueryDelegation(expr, "26.3")
			if !result.Eligible {
				t.Fatalf("expected query %q to be delegation-eligible, got %#v", tc.query, result)
			}
		})
	}
}

func TestClassifyEntireQueryDelegationRejectsUnverifiedConstructs(t *testing.T) {
	tests := []struct {
		name           string
		query          string
		reasonContains string
	}{
		{name: "scalar root", query: `1 + 2`, reasonContains: "scalar-only roots"},
		{name: "aggregation", query: `sum by (job) (up)`, reasonContains: "aggregations"},
		{name: "binary vector scalar", query: `up * 100`, reasonContains: "binary operators"},
		{name: "binary vector vector", query: `up + up`, reasonContains: "binary operators"},
		{name: "range function", query: `sum_over_time(up[5m])`, reasonContains: "sum_over_time()"},
		{name: "rate function", query: `rate(up[5m])`, reasonContains: "rate()"},
		{name: "subquery", query: `sum_over_time(up[5m:1m])`, reasonContains: "sum_over_time()"},
		{name: "label join", query: `label_join(up, "joined", "/", "job", "namespace")`, reasonContains: "label_join()"},
		{name: "vector helper", query: `vector(1)`, reasonContains: "vector()"},
		{name: "histogram quantile", query: `histogram_quantile(0.9, up)`, reasonContains: "histogram_quantile()"},
		{name: "absent", query: `absent(up)`, reasonContains: "absent()"},
		{name: "absent over time", query: `absent_over_time(up[5m])`, reasonContains: "absent_over_time()"},
		{name: "unary minus", query: `-up`, reasonContains: "unary operators"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			expr, err := logicalpkg.ParseExpression(tc.query)
			if err != nil {
				t.Fatal(err)
			}
			result := ClassifyEntireQueryDelegation(expr, "26.3")
			if result.Eligible {
				t.Fatalf("expected query %q to be rejected, got %#v", tc.query, result)
			}
			if !strings.Contains(result.Reason, tc.reasonContains) {
				t.Fatalf("expected reason for %q to contain %q, got %#v", tc.query, tc.reasonContains, result)
			}
		})
	}
}
