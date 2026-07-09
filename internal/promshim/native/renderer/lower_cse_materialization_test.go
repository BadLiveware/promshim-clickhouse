package renderer

import (
	"strings"
	"testing"
)

// These tests pin the trigger boundary for issue #39: a selector (or repeated
// subtree) shared by both sides of a set operator, with a value-filtering
// comparison on at least one side, must NOT be promoted to a MATERIALIZED
// CTE — ClickHouse rejects filtered references to MATERIALIZED CTEs at
// execution. The shared CTE stays, but renders as a plain CTE and the
// enable_materialized_cte setting is dropped. Non-trigger shapes keep the
// MATERIALIZED promotion (goldens in testdata/lower_binary_vector_join pin
// their exact SQL).

func lowerForCSETest(t *testing.T, query string, params RenderParams) RenderedQuery {
	t.Helper()
	root, analysis, nativeAnalysis := buildLowerInputs(t, query)
	rq, err := Lower(LoweringCtx{Config: testRenderConfig(), Analysis: analysis, NativeAnalysis: nativeAnalysis, Params: params}, root)
	if err != nil {
		t.Fatalf("Lower(%q): %v", query, err)
	}
	return rq
}

func TestSetOpFilteredSharedSelectorRendersPlainCTE(t *testing.T) {
	cases := []struct {
		name  string
		query string
	}{
		{name: "unless_eq", query: `up unless (up == 0)`},
		{name: "and_eq", query: `up and (up == 0)`},
		{name: "or_eq", query: `up or (up == 0)`},
		{name: "unless_neq", query: `up unless (up != 1)`},
		{name: "unless_on_eq", query: `up unless on(instance) (up == 0)`},
		{name: "unless_lhs_filtered", query: `(up == 0) unless up`},
	}
	modes := []struct {
		name   string
		params RenderParams
	}{
		{name: "instant", params: testRenderParamsInstant()},
		{name: "range", params: testRenderParamsRange()},
	}
	for _, tc := range cases {
		for _, mode := range modes {
			t.Run(tc.name+"_"+mode.name, func(t *testing.T) {
				rq := lowerForCSETest(t, tc.query, mode.params)
				if !strings.Contains(rq.SQL, "WITH cse_selector_") {
					t.Fatalf("expected shared selector CTE in SQL:\n%s", rq.SQL)
				}
				if strings.Contains(rq.SQL, "MATERIALIZED") {
					t.Fatalf("filtered set-op reference must not use a MATERIALIZED CTE, got SQL:\n%s", rq.SQL)
				}
				if strings.Contains(rq.SQL, "enable_materialized_cte") {
					t.Fatalf("plain-CTE-only query must not set enable_materialized_cte, got SQL:\n%s", rq.SQL)
				}
				decision, ok := findPhysicalDecisionByKind(rq.PhysicalDecisions, "cse_cte_materialization")
				if !ok || decision.Strategy != "plain_cte" {
					t.Fatalf("expected cse_cte_materialization=plain_cte decision, got %#v", rq.PhysicalDecisions)
				}
			})
		}
	}
}

func TestSetOpUnfilteredSharedSelectorKeepsMaterializedCTE(t *testing.T) {
	rq := lowerForCSETest(t, `up unless up`, testRenderParamsInstant())
	if !strings.Contains(rq.SQL, "AS MATERIALIZED (") {
		t.Fatalf("unfiltered shared set-op selector must keep MATERIALIZED CTE, got SQL:\n%s", rq.SQL)
	}
	if !strings.Contains(rq.SQL, "enable_materialized_cte = 1") {
		t.Fatalf("expected enable_materialized_cte setting, got SQL:\n%s", rq.SQL)
	}
	if _, ok := findPhysicalDecisionByKind(rq.PhysicalDecisions, "cse_cte_materialization"); ok {
		t.Fatalf("unexpected cse_cte_materialization decision for unfiltered shape: %#v", rq.PhysicalDecisions)
	}
}

func TestSetOpBoolComparisonKeepsMaterializedCTE(t *testing.T) {
	// `== bool` rewrites values without dropping rows: the CTE reference is
	// wrapped in a value transform, not a filter, so materialization stays.
	rq := lowerForCSETest(t, `up unless (up == bool 0)`, testRenderParamsInstant())
	if !strings.Contains(rq.SQL, "AS MATERIALIZED (") {
		t.Fatalf("bool comparison must keep MATERIALIZED CTE, got SQL:\n%s", rq.SQL)
	}
	if !strings.Contains(rq.SQL, "enable_materialized_cte = 1") {
		t.Fatalf("expected enable_materialized_cte setting, got SQL:\n%s", rq.SQL)
	}
}

func TestSetOpDifferentSelectorsKeepNoSharedCTE(t *testing.T) {
	// Different selectors never share a CTE; the filter on one side must not
	// introduce one either.
	rq := lowerForCSETest(t, `up unless (up{status="down"} == 0)`, testRenderParamsInstant())
	if strings.Contains(rq.SQL, "cse_selector_") {
		t.Fatalf("different selectors must not share a CTE, got SQL:\n%s", rq.SQL)
	}
}

func TestSetOpFilteredSharedSubtreeRendersPlainCTE(t *testing.T) {
	// The same ClickHouse rejection applies to repeated-subtree CTEs whose
	// reference sits behind a filter inside a set-operator join.
	rq := lowerForCSETest(t, `rate(demo_cpu_usage_seconds_total[5m]) unless (rate(demo_cpu_usage_seconds_total[5m]) > 0)`, testRenderParamsInstant())
	if !strings.Contains(rq.SQL, "WITH cse_subtree_") {
		t.Fatalf("expected shared subtree CTE in SQL:\n%s", rq.SQL)
	}
	if strings.Contains(rq.SQL, "MATERIALIZED") {
		t.Fatalf("filtered set-op subtree reference must not use a MATERIALIZED CTE, got SQL:\n%s", rq.SQL)
	}
	if strings.Contains(rq.SQL, "enable_materialized_cte") {
		t.Fatalf("plain-CTE-only query must not set enable_materialized_cte, got SQL:\n%s", rq.SQL)
	}
}

func TestFilteredSharedSelectorOutsideSetOpKeepsMaterializedCTE(t *testing.T) {
	// The demotion is scoped to set-operator joins — the verified trigger
	// boundary. A filtered shared selector under an arithmetic join keeps
	// the current MATERIALIZED promotion.
	rq := lowerForCSETest(t, `(up == 0) + up`, testRenderParamsInstant())
	if !strings.Contains(rq.SQL, "WITH cse_selector_") {
		t.Fatalf("expected shared selector CTE in SQL:\n%s", rq.SQL)
	}
	if !strings.Contains(rq.SQL, "AS MATERIALIZED (") {
		t.Fatalf("arithmetic join must keep MATERIALIZED CTE, got SQL:\n%s", rq.SQL)
	}
}
