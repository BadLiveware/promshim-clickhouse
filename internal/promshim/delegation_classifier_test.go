package promshim

import (
	"strings"
	"testing"

	planpkg "github.com/BadLiveware/promshim-ch/internal/promshim/plan"
)

func TestClassifyEntireQueryDelegationSupportsSimpleSelector(t *testing.T) {
	expr, err := planpkg.ParseExpression(`up{job="api"}`)
	if err != nil {
		t.Fatal(err)
	}
	result := classifyEntireQueryDelegation(expr, "26.3")
	if !result.Eligible {
		t.Fatalf("expected simple selector to be delegation-eligible, got %#v", result)
	}
}

func TestClassifyEntireQueryDelegationRejectsCurrentUnsupportedRoots(t *testing.T) {
	tests := []struct {
		name           string
		query          string
		reasonContains string
	}{
		{name: "scalar root", query: `1 + 2`, reasonContains: "scalar-only roots"},
		{name: "aggregation", query: `sum by (job) (up)`, reasonContains: "aggregations"},
		{name: "subquery", query: `sum(up)[5m:1m]`, reasonContains: "delegated subquery"},
		{name: "range function", query: `sum_over_time(up[5m])`, reasonContains: "sum_over_time()"},
		{name: "rate function", query: `rate(up[5m])`, reasonContains: "rate()"},
		{name: "label join", query: `label_join(up, "joined", "/", "job", "namespace")`, reasonContains: "label_join()"},
		{name: "label replace", query: `label_replace(up, "dst", "$1", "job", "(.*)")`, reasonContains: "label_replace()"},
		{name: "vector helper", query: `vector(1)`, reasonContains: "vector()"},
		{name: "round helper", query: `round(up)`, reasonContains: "round()"},
		{name: "histogram quantile", query: `histogram_quantile(0.9, up)`, reasonContains: "histogram_quantile()"},
		{name: "histogram count", query: `histogram_count(up)`, reasonContains: "histogram_count()"},
		{name: "histogram sum", query: `histogram_sum(up)`, reasonContains: "histogram_sum()"},
		{name: "histogram avg", query: `histogram_avg(up)`, reasonContains: "histogram_avg()"},
		{name: "histogram fraction", query: `histogram_fraction(0, 1, up)`, reasonContains: "histogram_fraction()"},
		{name: "absent", query: `absent(up)`, reasonContains: "absent()"},
		{name: "absent over time", query: `absent_over_time(up[5m])`, reasonContains: "absent_over_time()"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			expr, err := planpkg.ParseExpression(tc.query)
			if err != nil {
				t.Fatal(err)
			}
			result := classifyEntireQueryDelegation(expr, "26.3")
			if result.Eligible {
				t.Fatalf("expected query %q to be rejected by capability map, got %#v", tc.query, result)
			}
			if !strings.Contains(result.Reason, tc.reasonContains) {
				t.Fatalf("expected reason for %q to contain %q, got %#v", tc.query, tc.reasonContains, result)
			}
		})
	}
}
