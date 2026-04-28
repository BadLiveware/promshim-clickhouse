package renderer

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// binaryVectorJoinCases covers the full matching-shape matrix for Surface 13:
//
//   - Arithmetic ops (*, +, -, /, %, ^): one-to-one, no modifier
//   - Arithmetic with on(...): label-restricted join
//   - Arithmetic with ignoring(...): label-excluded join
//   - Arithmetic group_left: many-to-one cardinality
//   - Arithmetic group_right: one-to-many cardinality
//   - Comparison with bool (==, !=): drops metric name
//   - Set ops: and (instant only), or (instant + range), unless (instant)
//   - Range-function children: rate(...) / ignoring(...) group_left() rate(...)
//
// TestLowerBinaryVectorJoinGolden locks a representative subset into .sql files.
var binaryVectorJoinCases = []struct {
	name  string
	query string
}{
	// — arithmetic, one-to-one, no modifier —
	{name: "mul_up_up", query: `up * up`},
	// — arithmetic, on(instance) label match —
	{name: "add_on_instance", query: `up{job="api"} + on(instance) up{job="db"}`},
	// — arithmetic, ignoring(env) label exclusion —
	{name: "add_ignoring_env", query: `up + ignoring(env) up`},
	// — arithmetic, group_left (many-to-one) —
	{name: "mul_group_left_job", query: `up * ignoring(instance) group_left(job) up`},
	// — arithmetic, group_right (one-to-many) —
	{name: "mul_group_right_job", query: `up * ignoring(instance) group_right(job) up`},
	// — comparison with bool (vector-vector, drops metric name) —
	{name: "eq_bool", query: `up == bool up`},
	// — comparison with bool, != —
	{name: "neq_bool", query: `up != bool up`},
	// — set op: and —
	{name: "and_up", query: `up and up`},
	// — set op: or —
	{name: "or_up", query: `up or up{job="new"}`},
	// — set op: unless —
	{name: "unless_up", query: `up unless up{status="down"}`},
	// — range-function children: rate / ignoring group_left —
	{name: "rate_div_group_left", query: `rate(http_requests_total[5m]) / ignoring(code) group_left() rate(http_requests_total[5m])`},
	// — arithmetic sub —
	{name: "sub_up_up", query: `up - up`},
}

// goldenBinaryVectorJoinCases selects the subset of binaryVectorJoinCases
// that receive golden files: the first six canonical shapes plus the rate
// group_left case.
var goldenBinaryVectorJoinCases = []int{0, 1, 2, 3, 5, 7, 8, 10}

// TestLowerBinaryVectorJoinGolden locks in the exact SQL for the golden
// subset in both instant and range modes. Run with -update to regenerate.
func TestLowerBinaryVectorJoinGolden(t *testing.T) {
	for _, idx := range goldenBinaryVectorJoinCases {
		tc := binaryVectorJoinCases[idx]
		for _, mode := range []struct {
			name   string
			params RenderParams
		}{
			{name: "instant", params: testRenderParamsInstant()},
			{name: "range", params: testRenderParamsRange()},
		} {
			t.Run(tc.name+"_"+mode.name, func(t *testing.T) {
				root, analysis, nativeAnalysis := buildLowerInputs(t, tc.query)
				rq, err := Lower(LoweringCtx{
					Config:         testRenderConfig(),
					Analysis:       analysis,
					NativeAnalysis: nativeAnalysis,
					Params:         mode.params,
				}, root)
				if err != nil {
					t.Fatalf("Lower: %v", err)
				}
				goldenPath := filepath.Join("testdata", "lower_binary_vector_join", tc.name+"_"+mode.name+".sql")
				if *updateLowerGolden {
					if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
						t.Fatalf("mkdir testdata: %v", err)
					}
					if err := os.WriteFile(goldenPath, []byte(rq.SQL), 0o644); err != nil {
						t.Fatalf("write golden: %v", err)
					}
					return
				}
				want, err := os.ReadFile(goldenPath)
				if err != nil {
					t.Fatalf("read golden (run with -update to create): %v", err)
				}
				if string(want) != rq.SQL {
					t.Errorf("SQL differs from golden %s\nwant:\n%s\ngot:\n%s", goldenPath, want, rq.SQL)
				}
			})
		}
	}
}

func TestLowerBinaryVectorJoinReusesIdenticalInstantAddSubexpression(t *testing.T) {
	root, analysis, nativeAnalysis := buildLowerInputs(t, `(rate(demo_cpu_usage_seconds_total[1h]) + rate(demo_cpu_usage_seconds_total[1h])) / 2`)
	rq, err := Lower(LoweringCtx{Config: testRenderConfig(), Analysis: analysis, NativeAnalysis: nativeAnalysis, Params: testRenderParamsInstant()}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	if got := strings.Count(rq.SQL, "timeSeriesData("); got != 1 {
		t.Fatalf("timeSeriesData count = %d, want 1 in SQL:\n%s", got, rq.SQL)
	}
	if got := strings.Count(rq.SQL, "deltaSumTimestamp"); got != 1 {
		t.Fatalf("deltaSumTimestamp count = %d, want 1 in SQL:\n%s", got, rq.SQL)
	}
	if !strings.Contains(rq.SQL, "lhs.value + lhs.value") {
		t.Fatalf("expected self-reuse value expression, got SQL:\n%s", rq.SQL)
	}
}

func TestLowerBinaryVectorJoinReuseRollbackGate(t *testing.T) {
	t.Setenv(DisableNativeRepeatedSubexpressionReuseEnv, "true")
	root, analysis, nativeAnalysis := buildLowerInputs(t, `(rate(demo_cpu_usage_seconds_total[1h]) + rate(demo_cpu_usage_seconds_total[1h])) / 2`)
	rq, err := Lower(LoweringCtx{Config: testRenderConfig(), Analysis: analysis, NativeAnalysis: nativeAnalysis, Params: testRenderParamsInstant()}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	if got := strings.Count(rq.SQL, "timeSeriesData("); got != 2 {
		t.Fatalf("timeSeriesData count = %d, want rollback to 2 in SQL:\n%s", got, rq.SQL)
	}
}

func TestLowerBinaryVectorJoinReusesIdenticalRangeMulSubexpression(t *testing.T) {
	root, analysis, nativeAnalysis := buildLowerInputs(t, `rate(demo_cpu_usage_seconds_total[5m]) * rate(demo_cpu_usage_seconds_total[5m])`)
	rq, err := Lower(LoweringCtx{Config: testRenderConfig(), Analysis: analysis, NativeAnalysis: nativeAnalysis, Params: testRenderParamsRange()}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	if got := strings.Count(rq.SQL, "timeSeriesData("); got != 1 {
		t.Fatalf("timeSeriesData count = %d, want 1 in SQL:\n%s", got, rq.SQL)
	}
	if got := strings.Count(rq.SQL, "ARRAY JOIN time_series AS point"); got != 1 {
		t.Fatalf("ARRAY JOIN count = %d, want 1 in SQL:\n%s", got, rq.SQL)
	}
	if !strings.Contains(rq.SQL, "lhs.value * lhs.value") {
		t.Fatalf("expected self-reuse multiply expression, got SQL:\n%s", rq.SQL)
	}
}

func TestLowerBinaryVectorJoinReusesIdenticalRangeComparisonSubexpression(t *testing.T) {
	root, analysis, nativeAnalysis := buildLowerInputs(t, `rate(demo_cpu_usage_seconds_total[5m]) >= rate(demo_cpu_usage_seconds_total[5m])`)
	rq, err := Lower(LoweringCtx{Config: testRenderConfig(), Analysis: analysis, NativeAnalysis: nativeAnalysis, Params: testRenderParamsRange()}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	if got := strings.Count(rq.SQL, "timeSeriesData("); got != 1 {
		t.Fatalf("timeSeriesData count = %d, want 1 in SQL:\n%s", got, rq.SQL)
	}
	if got := strings.Count(rq.SQL, "ARRAY JOIN time_series AS point"); got != 1 {
		t.Fatalf("ARRAY JOIN count = %d, want 1 in SQL:\n%s", got, rq.SQL)
	}
	if !strings.Contains(rq.SQL, "lhs.value >= lhs.value") {
		t.Fatalf("expected self-reuse comparison predicate, got SQL:\n%s", rq.SQL)
	}
}

func TestLowerBinaryVectorJoinReusesRangeComparisonBool(t *testing.T) {
	root, analysis, nativeAnalysis := buildLowerInputs(t, `rate(demo_cpu_usage_seconds_total[5m]) >= bool rate(demo_cpu_usage_seconds_total[5m])`)
	rq, err := Lower(LoweringCtx{Config: testRenderConfig(), Analysis: analysis, NativeAnalysis: nativeAnalysis, Params: testRenderParamsRange()}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	if got := strings.Count(rq.SQL, "timeSeriesData("); got != 1 {
		t.Fatalf("timeSeriesData count = %d, want 1 in SQL:\n%s", got, rq.SQL)
	}
	if got := strings.Count(rq.SQL, "ARRAY JOIN time_series AS point"); got != 1 {
		t.Fatalf("ARRAY JOIN count = %d, want 1 for bool comparison self-reuse in SQL:\n%s", got, rq.SQL)
	}
	if !strings.Contains(rq.SQL, "toFloat64(if((lhs.value >= lhs.value), 1, 0))") {
		t.Fatalf("expected bool comparison self-reuse expression, got SQL:\n%s", rq.SQL)
	}
}

func TestLowerBinaryVectorJoinDoesNotReuseLeafArithmetic(t *testing.T) {
	root, analysis, nativeAnalysis := buildLowerInputs(t, `up * up`)
	rq, err := Lower(LoweringCtx{Config: testRenderConfig(), Analysis: analysis, NativeAnalysis: nativeAnalysis, Params: testRenderParamsRange()}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	if got := strings.Count(rq.SQL, "ARRAY JOIN time_series AS point"); got != 2 {
		t.Fatalf("ARRAY JOIN count = %d, want 2 for non-range-function leaf reuse in SQL:\n%s", got, rq.SQL)
	}
}

// TestLowerBinaryVectorJoinNilErrors exercises the defensive nil guard in
// lowerBinaryVectorJoin. A nil node must return a non-sentinel error.
func TestLowerBinaryVectorJoinNilErrors(t *testing.T) {
	_, err := lowerBinaryVectorJoin(LoweringCtx{}, nil)
	if err == nil {
		t.Fatalf("expected error for nil BinaryPlan")
	}
	if errors.Is(err, errUnsupportedLowerNode) {
		t.Fatalf("expected non-sentinel error for nil node, got sentinel")
	}
}
