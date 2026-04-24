package renderer

import (
	"testing"

	logicalpkg "github.com/BadLiveware/promshim-ch/internal/promshim/logical"
	"github.com/BadLiveware/promshim-ch/internal/promshim/native"
)

// TestHistogramProjectionLogicalMatchesFragment is the Phase-A1 byte-equality
// differential guard that locks the new logical-path rendering to the
// Fragment path. It exercises all five histogram projection functions on a
// direct selector child plus two grouping-aggregation child shapes (sum by
// (le), sum by (le, job)) that drive the RenderParams-threaded tag-narrowing
// path in renderHistogramProjectionLogical.
//
// The test renders each query through BOTH entry points — native.BuildFragment
// + RenderFragment and the new lowerHistogramProjection (which now calls
// renderHistogramProjectionLogical) — in both instant and range modes, and
// asserts byte-identical SQL + QueryParams.
func TestHistogramProjectionLogicalMatchesFragment(t *testing.T) {
	queries := []string{
		`histogram_count(http_request_duration_seconds_bucket{job="api"})`,
		`histogram_sum(http_request_duration_seconds_bucket{job="api"})`,
		`histogram_avg(http_request_duration_seconds_bucket{job="api"})`,
		`histogram_stddev(http_request_duration_seconds_bucket{job="api"})`,
		`histogram_stdvar(http_request_duration_seconds_bucket{job="api"})`,
		`histogram_count(sum by (le) (http_request_duration_seconds_bucket{job="api"}))`,
		`histogram_sum(sum by (le, job) (http_request_duration_seconds_bucket{job="api"}))`,
		// Non-narrowing aggregation shapes: without clause and empty
		// grouping both drop into decideHistogramChildNarrowing's
		// require-full-tags branch, so the child Fragment is rendered
		// with the full selector shape. These cases guard against
		// regressions in the non-narrowed branch of
		// renderClassicHistogramGroupsQueryLogical.
		`histogram_count(sum without (instance) (http_request_duration_seconds_bucket{job="api"}))`,
		`histogram_sum(sum (http_request_duration_seconds_bucket{job="api"}))`,
	}

	for _, query := range queries {
		for _, mode := range []struct {
			name   string
			params RenderParams
		}{
			{name: "instant", params: testRenderParamsInstant()},
			{name: "range", params: testRenderParamsRange()},
		} {
			t.Run(query+"/"+mode.name, func(t *testing.T) {
				root, analysis, nativeAnalysis := buildLowerInputs(t, query)

				proj, ok := root.(*logicalpkg.HistogramProjectionPlan)
				if !ok {
					t.Fatalf("expected HistogramProjectionPlan, got %T", root)
				}

				// Fragment path.
				fragment, err := native.BuildFragment(root, nativeAnalysis)
				if err != nil {
					t.Fatalf("BuildFragment: %v", err)
				}
				wantRQ, err := RenderFragment(testRenderConfig(), fragment, mode.params)
				if err != nil {
					t.Fatalf("RenderFragment: %v", err)
				}

				// Logical path via the flipped lowerHistogramProjection.
				gotRQ, err := lowerHistogramProjection(LoweringCtx{
					Config:         testRenderConfig(),
					Analysis:       analysis,
					NativeAnalysis: nativeAnalysis,
					Params:         mode.params,
				}, proj)
				if err != nil {
					t.Fatalf("lowerHistogramProjection: %v", err)
				}

				if gotRQ.SQL != wantRQ.SQL {
					t.Fatalf("SQL mismatch for %q (%s)\nwant:\n%s\n\ngot:\n%s", query, mode.name, wantRQ.SQL, gotRQ.SQL)
				}
				if !queryParamsEqualForLogicalHistogram(gotRQ.QueryParams, wantRQ.QueryParams) {
					t.Fatalf("QueryParams mismatch for %q (%s)\nwant: %v\ngot:  %v", query, mode.name, wantRQ.QueryParams, gotRQ.QueryParams)
				}
			})
		}
	}
}

func queryParamsEqualForLogicalHistogram(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// TestHistogramFunctionLogicalMatchesFragment is the Phase-A2 byte-equality
// differential guard that locks the new logical-path rendering to the Fragment
// path. It covers all three histogram-function plan kinds across leaf,
// single-label grouping-aggregation, and multi-label grouping-aggregation
// child shapes that drive the RenderParams-threaded tag-narrowing path in
// renderHistogramFunctionLogical.
//
// The test renders each query through BOTH entry points — native.BuildFragment
// + RenderFragment (Fragment path) and the new lowerHistogramFunction (which
// now calls renderHistogramFunctionLogical instead of BuildFragment at the
// boundary) — in both instant and range modes, and asserts byte-identical
// SQL + QueryParams.
func TestHistogramFunctionLogicalMatchesFragment(t *testing.T) {
	queries := []string{
		`histogram_quantile(0.9, foo_bucket)`,
		// Grouping-aggregation child, single-label "by (le)" drives
		// identity-tag shortcut in renderClassicHistogramGroupsQueryLogical.
		`histogram_quantile(0.9, sum by (le) (rate(foo_bucket[5m])))`,
		`histogram_quantile(0.5, sum by (le) (rate(foo_bucket[5m])))`,
		// Multi-label grouping-aggregation child (narrowing, full-tag path).
		`histogram_quantile(0.99, sum by (le, job) (rate(foo_bucket[1m])))`,
		// Non-aggregation child — no narrowing. The immediate child of the
		// histogram function is a RangeFunctionPlan, so
		// histogramFunctionChildAggregation returns nil and RenderParams
		// pass through unchanged.
		`histogram_quantile(0.9, rate(foo_bucket[5m]))`,
		`histogram_fraction(0.1, 0.2, foo_bucket)`,
		// Fraction variant with grouping-aggregation child (narrowing).
		`histogram_fraction(0.1, 0.9, sum by (le) (rate(foo_bucket[5m])))`,
		`histogram_fraction(0.1, 0.2, sum by (le) (rate(foo_bucket[5m])))`,
		// Quantiles variant with grouping-aggregation child — exercises
		// both the logical-child path for the classic histograms and the
		// transitional BuildFragment inside the histogram_quantiles branch
		// (needed for the scalar-binding Quantiles []*NativeFragment).
		`histogram_quantiles(sum by (le) (rate(foo_bucket[5m])), "quantile", 0.5, 0.9, 0.99)`,
		`histogram_quantiles(foo_bucket, "quantile", 0.5, 0.9)`,
		// Non-narrowing aggregation shape as child: "without (instance)"
		// drops into decideHistogramChildNarrowing's require-full-tags
		// branch, so the child Fragment is rendered with the full
		// selector shape. Guards against regressions in the non-narrowed
		// branch of renderClassicHistogramGroupsQueryLogical.
		`histogram_quantile(0.9, sum without (instance) (rate(foo_bucket[5m])))`,
	}

	for _, query := range queries {
		for _, mode := range []struct {
			name   string
			params RenderParams
		}{
			{name: "instant", params: testRenderParamsInstant()},
			{name: "range", params: testRenderParamsRange()},
		} {
			t.Run(query+"/"+mode.name, func(t *testing.T) {
				root, analysis, nativeAnalysis := buildLowerInputs(t, query)

				// Fragment path.
				fragment, err := native.BuildFragment(root, nativeAnalysis)
				if err != nil {
					t.Fatalf("BuildFragment: %v", err)
				}
				wantRQ, err := RenderFragment(testRenderConfig(), fragment, mode.params)
				if err != nil {
					t.Fatalf("RenderFragment: %v", err)
				}

				// Logical path via the flipped lowerHistogramFunction.
				gotRQ, err := lowerHistogramFunction(LoweringCtx{
					Config:         testRenderConfig(),
					Analysis:       analysis,
					NativeAnalysis: nativeAnalysis,
					Params:         mode.params,
				}, root)
				if err != nil {
					t.Fatalf("lowerHistogramFunction: %v", err)
				}

				if gotRQ.SQL != wantRQ.SQL {
					t.Fatalf("SQL mismatch for %q (%s)\nwant:\n%s\n\ngot:\n%s", query, mode.name, wantRQ.SQL, gotRQ.SQL)
				}
				if !queryParamsEqualForLogicalHistogram(gotRQ.QueryParams, wantRQ.QueryParams) {
					t.Fatalf("QueryParams mismatch for %q (%s)\nwant: %v\ngot:  %v", query, mode.name, wantRQ.QueryParams, gotRQ.QueryParams)
				}
			})
		}
	}
}
