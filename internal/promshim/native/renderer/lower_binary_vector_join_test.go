package renderer

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/native/physical"
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
	root, analysis, nativeAnalysis := buildLowerInputs(t, `rate(demo_cpu_usage_seconds_total[1h]) + rate(demo_cpu_usage_seconds_total[1h])`)
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
	decision, ok := findPhysicalDecisionByKind(rq.PhysicalDecisions, "row_source_reuse")
	if !ok || decision.Strategy != "instant_self_join" {
		t.Fatalf("expected row_source_reuse=instant_self_join decision, got %#v", rq.PhysicalDecisions)
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
	decision, ok := findPhysicalDecisionByKind(rq.PhysicalDecisions, "row_source_reuse")
	if !ok || decision.Strategy != "range_self_join" {
		t.Fatalf("expected row_source_reuse=range_self_join decision, got %#v", rq.PhysicalDecisions)
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
	decision, ok := findPhysicalDecisionByKind(rq.PhysicalDecisions, "row_source_reuse")
	if !ok || decision.Strategy != "not_reused" {
		t.Fatalf("expected row_source_reuse=not_reused decision, got %#v", rq.PhysicalDecisions)
	}
}

func TestLowerBinaryVectorJoinMarksInstantNotReusedForOnMatching(t *testing.T) {
	root, analysis, nativeAnalysis := buildLowerInputs(t, `rate(demo_cpu_usage_seconds_total[1h]) + on(job) rate(demo_cpu_usage_seconds_total[1h])`)
	rq, err := Lower(LoweringCtx{Config: testRenderConfig(), Analysis: analysis, NativeAnalysis: nativeAnalysis, Params: testRenderParamsInstant()}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	decision, ok := findPhysicalDecisionByKind(rq.PhysicalDecisions, "row_source_reuse")
	if !ok || decision.Strategy != "not_reused" || !strings.Contains(decision.Reason, "default one-to-one matching labels") {
		t.Fatalf("expected row_source_reuse=not_reused with matching reason, got %#v", rq.PhysicalDecisions)
	}
}

func TestLowerBinaryVectorJoinMarksInstantNotReusedForDifferentRepeatedOperands(t *testing.T) {
	root, analysis, nativeAnalysis := buildLowerInputs(t, `rate(demo_cpu_usage_seconds_total[1h]) + rate(demo_cpu_usage_seconds_total[6h])`)
	rq, err := Lower(LoweringCtx{Config: testRenderConfig(), Analysis: analysis, NativeAnalysis: nativeAnalysis, Params: testRenderParamsInstant()}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	decision, ok := findPhysicalDecisionByKind(rq.PhysicalDecisions, "row_source_reuse")
	if !ok || decision.Strategy != "not_reused" || !strings.Contains(decision.Reason, "different repeated subtree candidates") {
		t.Fatalf("expected row_source_reuse=not_reused for different repeated operands, got %#v", rq.PhysicalDecisions)
	}
}

func TestLowerBinaryVectorJoinMarksRangeNotReusedForDifferentRepeatedOperands(t *testing.T) {
	root, analysis, nativeAnalysis := buildLowerInputs(t, `rate(demo_cpu_usage_seconds_total[1h]) + rate(demo_cpu_usage_seconds_total[6h])`)
	rq, err := Lower(LoweringCtx{Config: testRenderConfig(), Analysis: analysis, NativeAnalysis: nativeAnalysis, Params: testRenderParamsRange()}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	decision, ok := findPhysicalDecisionByKind(rq.PhysicalDecisions, "row_source_reuse")
	if !ok || decision.Strategy != "not_reused" || !strings.Contains(decision.Reason, "different repeated subtree candidates") {
		t.Fatalf("expected row_source_reuse=not_reused for range different repeated operands, got %#v", rq.PhysicalDecisions)
	}
}

func findPhysicalDecisionByKind(decisions []physical.Decision, kind string) (physical.Decision, bool) {
	for _, decision := range decisions {
		if decision.Kind == kind {
			return decision, true
		}
	}
	return physical.Decision{}, false
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

func TestLowerBinaryVectorJoinRecognizesResourceStatusJoinShape(t *testing.T) {
	query := `kube_pod_container_resource_requests{resource="memory", job="kube-state-metrics"} * on (namespace, pod, cluster) group_left() max by (namespace, pod, cluster) (kube_pod_status_phase{phase=~"Pending|Running"} == 1)`
	root, analysis, nativeAnalysis := buildLowerInputs(t, query)
	rq, err := Lower(LoweringCtx{Config: testRenderConfig(), Analysis: analysis, NativeAnalysis: nativeAnalysis, Params: testRenderParamsInstant()}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	decision, ok := findPhysicalDecisionByKind(rq.PhysicalDecisions, "resource_status_join")
	if !ok || decision.Strategy != "recognized" {
		t.Fatalf("expected resource_status_join=recognized decision, got %#v", rq.PhysicalDecisions)
	}
}

func TestLowerBinaryVectorJoinRejectsResourceStatusJoinForNonMaxRHS(t *testing.T) {
	query := `kube_pod_container_resource_requests{resource="memory"} * on (namespace, pod, cluster) group_left() sum by (namespace, pod, cluster) (kube_pod_status_phase) != 1`
	root, analysis, nativeAnalysis := buildLowerInputs(t, query)
	rq, err := Lower(LoweringCtx{Config: testRenderConfig(), Analysis: analysis, NativeAnalysis: nativeAnalysis, Params: testRenderParamsInstant()}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	if _, ok := findPhysicalDecisionByKind(rq.PhysicalDecisions, "resource_status_join"); ok {
		t.Fatalf("did not expect resource_status_join for non-max RHS")
	}
}

func TestLowerBinaryVectorJoinReusesSharedActiveStatusRHSAcrossSiblingJoins(t *testing.T) {
	query := `(kube_pod_container_resource_requests{resource="cpu", job="kube-state-metrics"} * on (namespace, pod, cluster) group_left() max by (namespace, pod, cluster) (kube_pod_status_phase{phase=~"Pending|Running"} == 1)) + (kube_pod_container_resource_limits{resource="cpu", job="kube-state-metrics"} * on (namespace, pod, cluster) group_left() max by (namespace, pod, cluster) (kube_pod_status_phase{phase=~"Pending|Running"} == 1))`
	root, analysis, nativeAnalysis := buildLowerInputs(t, query)
	rq, err := Lower(LoweringCtx{Config: testRenderConfig(), Analysis: analysis, NativeAnalysis: nativeAnalysis, Params: testRenderParamsInstant()}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	if !strings.Contains(rq.SQL, "cse_subtree_") {
		t.Fatalf("expected subtree CSE for shared active-status RHS, got SQL:\n%s", rq.SQL)
	}
	if got := strings.Count(rq.SQL, "FROM cse_subtree_"); got < 2 {
		t.Fatalf("expected shared active-status RHS to be referenced by both sibling joins, got count=%d SQL:\n%s", got, rq.SQL)
	}
}

func TestLowerBinaryVectorJoinRecognizesVectorZeroDefaulting(t *testing.T) {
	query := `up or vector(0)`
	root, analysis, nativeAnalysis := buildLowerInputs(t, query)
	rq, err := Lower(LoweringCtx{Config: testRenderConfig(), Analysis: analysis, NativeAnalysis: nativeAnalysis, Params: testRenderParamsInstant()}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	decision, ok := findPhysicalDecisionByKind(rq.PhysicalDecisions, "vector_zero_defaulting")
	if !ok || decision.Strategy != "recognized" {
		t.Fatalf("expected vector_zero_defaulting=recognized decision, got %#v", rq.PhysicalDecisions)
	}
}

func TestLowerBinaryVectorJoinRecognizesMetadataLookupJoinShape(t *testing.T) {
	query := `rate(container_network_receive_packets_total[5m]) * on(cluster, namespace, pod) group_left() topk by (cluster, namespace, pod) (1, max by (cluster, namespace, pod) (kube_pod_info{host_network="false"}))`
	root, analysis, nativeAnalysis := buildLowerInputs(t, query)
	rq, err := Lower(LoweringCtx{Config: testRenderConfig(), Analysis: analysis, NativeAnalysis: nativeAnalysis, Params: testRenderParamsInstant()}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	decision, ok := findPhysicalDecisionByKind(rq.PhysicalDecisions, "metadata_lookup_join")
	if !ok || decision.Strategy != "recognized" {
		t.Fatalf("expected metadata_lookup_join=recognized decision, got %#v", rq.PhysicalDecisions)
	}
}

func TestLowerBinaryVectorJoinRejectsRankingTopKMetadataLookupShape(t *testing.T) {
	query := `rate(container_network_receive_packets_total[5m]) * on(cluster, namespace, pod) group_left() topk by (cluster, namespace, pod) (2, max by (cluster, namespace, pod) (kube_pod_info{host_network="false"}))`
	root, analysis, nativeAnalysis := buildLowerInputs(t, query)
	rq, err := Lower(LoweringCtx{Config: testRenderConfig(), Analysis: analysis, NativeAnalysis: nativeAnalysis, Params: testRenderParamsInstant()}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	decision, ok := findPhysicalDecisionByKind(rq.PhysicalDecisions, "metadata_lookup_join")
	if !ok || decision.Strategy != "not_recognized" || !strings.Contains(decision.Reason, "topk by") {
		t.Fatalf("expected metadata_lookup_join not_recognized decision, got %#v", rq.PhysicalDecisions)
	}
}

func TestLowerBinaryVectorJoinRecognizesMetadataLookupJoinWithGroupLeftLabels(t *testing.T) {
	query := `rate(container_cpu_usage_seconds_total[5m]) * on(cluster, namespace, pod) group_left(node) topk by (cluster, namespace, pod) (1, max by (cluster, namespace, pod, node) (kube_pod_info{node!=""}))`
	root, analysis, nativeAnalysis := buildLowerInputs(t, query)
	rq, err := Lower(LoweringCtx{Config: testRenderConfig(), Analysis: analysis, NativeAnalysis: nativeAnalysis, Params: testRenderParamsInstant()}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	decision, ok := findPhysicalDecisionByKind(rq.PhysicalDecisions, "metadata_lookup_join")
	if !ok || decision.Strategy != "recognized" {
		t.Fatalf("expected metadata_lookup_join=recognized decision, got %#v", rq.PhysicalDecisions)
	}
}

func TestLowerBinaryVectorJoinRejectsMetadataLookupJoinWhenGroupingMisaligned(t *testing.T) {
	query := `rate(container_cpu_usage_seconds_total[5m]) * on(cluster, namespace, pod) group_left(node) topk by (cluster, namespace) (1, max by (cluster, namespace, pod, node) (kube_pod_info{node!=""}))`
	root, analysis, nativeAnalysis := buildLowerInputs(t, query)
	rq, err := Lower(LoweringCtx{Config: testRenderConfig(), Analysis: analysis, NativeAnalysis: nativeAnalysis, Params: testRenderParamsInstant()}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	decision, ok := findPhysicalDecisionByKind(rq.PhysicalDecisions, "metadata_lookup_join")
	if !ok || decision.Strategy != "not_recognized" || !strings.Contains(decision.Reason, "topk grouping labels") {
		t.Fatalf("expected metadata_lookup_join not_recognized misalignment decision, got %#v", rq.PhysicalDecisions)
	}
}

func TestLowerBinaryVectorJoinReusesSharedMetadataLookupRHSAcrossSiblingJoins(t *testing.T) {
	query := `(rate(container_network_receive_packets_total[5m]) * on(cluster, namespace, pod) group_left() topk by (cluster, namespace, pod) (1, max by (cluster, namespace, pod) (kube_pod_info{host_network="false"}))) + (rate(container_network_transmit_packets_total[5m]) * on(cluster, namespace, pod) group_left() topk by (cluster, namespace, pod) (1, max by (cluster, namespace, pod) (kube_pod_info{host_network="false"})))`
	root, analysis, nativeAnalysis := buildLowerInputs(t, query)
	rq, err := Lower(LoweringCtx{Config: testRenderConfig(), Analysis: analysis, NativeAnalysis: nativeAnalysis, Params: testRenderParamsInstant()}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	if !strings.Contains(rq.SQL, "cse_subtree_") {
		t.Fatalf("expected subtree CSE for shared metadata RHS lookup, got SQL:\n%s", rq.SQL)
	}
	if got := strings.Count(rq.SQL, "FROM cse_subtree_"); got < 2 {
		t.Fatalf("expected shared metadata RHS lookup to be referenced by both sibling joins, got count=%d SQL:\n%s", got, rq.SQL)
	}
}

func TestLowerBinaryVectorJoinDoesNotReuseSharedMetadataLookupRHSForTopKRanking(t *testing.T) {
	query := `(rate(container_network_receive_packets_total[5m]) * on(cluster, namespace, pod) group_left() topk by (cluster, namespace, pod) (2, max by (cluster, namespace, pod) (kube_pod_info{host_network="false"}))) + (rate(container_network_transmit_packets_total[5m]) * on(cluster, namespace, pod) group_left() topk by (cluster, namespace, pod) (2, max by (cluster, namespace, pod) (kube_pod_info{host_network="false"})))`
	root, analysis, nativeAnalysis := buildLowerInputs(t, query)
	rq, err := Lower(LoweringCtx{Config: testRenderConfig(), Analysis: analysis, NativeAnalysis: nativeAnalysis, Params: testRenderParamsInstant()}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	if strings.Contains(rq.SQL, "cse_subtree_") {
		t.Fatalf("expected no metadata RHS subtree CSE for topk ranking shape, got SQL:\n%s", rq.SQL)
	}
}

func TestLowerBinaryVectorJoinDoesNotReuseSharedMetadataLookupRHSForNonLeafMetadata(t *testing.T) {
	query := `(rate(container_network_receive_packets_total[5m]) * on(cluster, namespace, pod) group_left() topk by (cluster, namespace, pod) (1, max by (cluster, namespace, pod) (label_replace(kube_pod_info{host_network="false"}, "cluster", "x", "", ".*")))) + (rate(container_network_transmit_packets_total[5m]) * on(cluster, namespace, pod) group_left() topk by (cluster, namespace, pod) (1, max by (cluster, namespace, pod) (label_replace(kube_pod_info{host_network="false"}, "cluster", "x", "", ".*"))))`
	root, analysis, nativeAnalysis := buildLowerInputs(t, query)
	rq, err := Lower(LoweringCtx{Config: testRenderConfig(), Analysis: analysis, NativeAnalysis: nativeAnalysis, Params: testRenderParamsInstant()}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	if strings.Contains(rq.SQL, "cse_subtree_") {
		t.Fatalf("expected no metadata RHS subtree CSE when metadata side is not a leaf selector, got SQL:\n%s", rq.SQL)
	}
}

func TestLowerBinaryVectorJoinReportsMetadataLookupFilterPushdownAlreadyScoped(t *testing.T) {
	query := `rate(container_network_receive_packets_total{cluster="dev",namespace="ns",pod="p1"}[5m]) * on(cluster, namespace, pod) group_left() topk by (cluster, namespace, pod) (1, max by (cluster, namespace, pod) (kube_pod_info{cluster="dev",namespace="ns",pod="p1"}))`
	root, analysis, nativeAnalysis := buildLowerInputs(t, query)
	rq, err := Lower(LoweringCtx{Config: testRenderConfig(), Analysis: analysis, NativeAnalysis: nativeAnalysis, Params: testRenderParamsInstant()}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	decision, ok := findPhysicalDecisionByKind(rq.PhysicalDecisions, "metadata_lookup_filter_pushdown")
	if !ok || decision.Strategy != "already_scoped" {
		t.Fatalf("expected metadata_lookup_filter_pushdown=already_scoped decision, got %#v", rq.PhysicalDecisions)
	}
}

func TestLowerBinaryVectorJoinReportsMetadataLookupFilterPushdownNotAppliedOnMismatch(t *testing.T) {
	query := `rate(container_network_receive_packets_total{cluster="dev",namespace="ns",pod="p1"}[5m]) * on(cluster, namespace, pod) group_left() topk by (cluster, namespace, pod) (1, max by (cluster, namespace, pod) (kube_pod_info{cluster="prod",namespace="ns",pod="p1"}))`
	root, analysis, nativeAnalysis := buildLowerInputs(t, query)
	rq, err := Lower(LoweringCtx{Config: testRenderConfig(), Analysis: analysis, NativeAnalysis: nativeAnalysis, Params: testRenderParamsInstant()}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	decision, ok := findPhysicalDecisionByKind(rq.PhysicalDecisions, "metadata_lookup_filter_pushdown")
	if !ok || decision.Strategy != "not_applied" {
		t.Fatalf("expected metadata_lookup_filter_pushdown=not_applied decision, got %#v", rq.PhysicalDecisions)
	}
}

func TestLowerBinaryVectorJoinReportsMetadataLookupFilterPushdownEligibleButNotRewritten(t *testing.T) {
	query := `rate(container_network_receive_packets_total{cluster="dev",namespace="ns",pod="p1"}[5m]) * on(cluster, namespace, pod) group_left() topk by (cluster, namespace, pod) (1, max by (cluster, namespace, pod) (kube_pod_info{namespace="ns",pod="p1"}))`
	root, analysis, nativeAnalysis := buildLowerInputs(t, query)
	rq, err := Lower(LoweringCtx{Config: testRenderConfig(), Analysis: analysis, NativeAnalysis: nativeAnalysis, Params: testRenderParamsInstant()}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	decision, ok := findPhysicalDecisionByKind(rq.PhysicalDecisions, "metadata_lookup_filter_pushdown")
	if !ok || decision.Strategy != "applied" {
		t.Fatalf("expected metadata_lookup_filter_pushdown=applied decision, got %#v", rq.PhysicalDecisions)
	}
	if !strings.Contains(rq.SQL, "AS metadata_rhs WHERE") || !strings.Contains(rq.SQL, "tag.1 = 'cluster'") {
		t.Fatalf("expected rhs metadata filter injection in SQL, got:\n%s", rq.SQL)
	}
}

func TestLowerBinaryVectorJoinReportsMetadataLookupFilterPushdownNotAppliedWhenLHSKeyMissing(t *testing.T) {
	query := `rate(container_network_receive_packets_total{namespace="ns",pod="p1"}[5m]) * on(cluster, namespace, pod) group_left() topk by (cluster, namespace, pod) (1, max by (cluster, namespace, pod) (kube_pod_info{namespace="ns",pod="p1"}))`
	root, analysis, nativeAnalysis := buildLowerInputs(t, query)
	rq, err := Lower(LoweringCtx{Config: testRenderConfig(), Analysis: analysis, NativeAnalysis: nativeAnalysis, Params: testRenderParamsInstant()}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	decision, ok := findPhysicalDecisionByKind(rq.PhysicalDecisions, "metadata_lookup_filter_pushdown")
	if !ok || decision.Strategy != "not_applied" || !strings.Contains(decision.Reason, "left side is missing") {
		t.Fatalf("expected metadata_lookup_filter_pushdown not_applied for lhs key missing, got %#v", rq.PhysicalDecisions)
	}
}

func TestLowerBinaryVectorJoinReportsMetadataLookupFilterPushdownAlreadyScopedForWrappedLHS(t *testing.T) {
	query := `label_replace(rate(container_network_receive_packets_total{cluster="dev",namespace="ns",pod="p1"}[5m]), "cluster", "dev", "", ".*") * on(cluster, namespace, pod) group_left() topk by (cluster, namespace, pod) (1, max by (cluster, namespace, pod) (kube_pod_info{cluster="dev",namespace="ns",pod="p1"}))`
	root, analysis, nativeAnalysis := buildLowerInputs(t, query)
	rq, err := Lower(LoweringCtx{Config: testRenderConfig(), Analysis: analysis, NativeAnalysis: nativeAnalysis, Params: testRenderParamsInstant()}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	decision, ok := findPhysicalDecisionByKind(rq.PhysicalDecisions, "metadata_lookup_filter_pushdown")
	if !ok || decision.Strategy != "already_scoped" {
		t.Fatalf("expected metadata_lookup_filter_pushdown=already_scoped for wrapped lhs selector extraction, got %#v", rq.PhysicalDecisions)
	}
}

func TestLowerBinaryVectorJoinAppliesMetadataLookupFilterPushdownInRangeMode(t *testing.T) {
	query := `rate(container_network_receive_packets_total{cluster="dev",namespace="ns",pod="p1"}[5m]) * on(cluster, namespace, pod) group_left() topk by (cluster, namespace, pod) (1, max by (cluster, namespace, pod) (kube_pod_info{namespace="ns",pod="p1"}))`
	root, analysis, nativeAnalysis := buildLowerInputs(t, query)
	rq, err := Lower(LoweringCtx{Config: testRenderConfig(), Analysis: analysis, NativeAnalysis: nativeAnalysis, Params: testRenderParamsRange()}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	decision, ok := findPhysicalDecisionByKind(rq.PhysicalDecisions, "metadata_lookup_filter_pushdown")
	if !ok || decision.Strategy != "applied" {
		t.Fatalf("expected metadata_lookup_filter_pushdown=applied in range mode, got %#v", rq.PhysicalDecisions)
	}
	if strings.Contains(rq.SQL, "SELECT *") {
		t.Fatalf("expected no SELECT * in rewritten range SQL, got:\n%s", rq.SQL)
	}
	if !strings.Contains(rq.SQL, "AS metadata_rhs WHERE") {
		t.Fatalf("expected rhs metadata filter injection in range SQL, got:\n%s", rq.SQL)
	}
}

func TestLowerBinaryVectorJoinDoesNotInjectMetadataFilterWhenAlreadyScoped(t *testing.T) {
	query := `rate(container_network_receive_packets_total{cluster="dev",namespace="ns",pod="p1"}[5m]) * on(cluster, namespace, pod) group_left() topk by (cluster, namespace, pod) (1, max by (cluster, namespace, pod) (kube_pod_info{cluster="dev",namespace="ns",pod="p1"}))`
	root, analysis, nativeAnalysis := buildLowerInputs(t, query)
	rq, err := Lower(LoweringCtx{Config: testRenderConfig(), Analysis: analysis, NativeAnalysis: nativeAnalysis, Params: testRenderParamsInstant()}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	decision, ok := findPhysicalDecisionByKind(rq.PhysicalDecisions, "metadata_lookup_filter_pushdown")
	if !ok || decision.Strategy != "already_scoped" {
		t.Fatalf("expected metadata_lookup_filter_pushdown=already_scoped decision, got %#v", rq.PhysicalDecisions)
	}
	if strings.Contains(rq.SQL, "AS metadata_rhs WHERE") {
		t.Fatalf("did not expect injected metadata filter wrapper for already scoped case, got:\n%s", rq.SQL)
	}
}

func TestLowerBinaryVectorJoinDoesNotInjectMetadataFilterWhenNotApplied(t *testing.T) {
	query := `rate(container_network_receive_packets_total{cluster="dev",namespace="ns",pod="p1"}[5m]) * on(cluster, namespace, pod) group_left() topk by (cluster, namespace, pod) (1, max by (cluster, namespace, pod) (kube_pod_info{cluster="prod",namespace="ns",pod="p1"}))`
	root, analysis, nativeAnalysis := buildLowerInputs(t, query)
	rq, err := Lower(LoweringCtx{Config: testRenderConfig(), Analysis: analysis, NativeAnalysis: nativeAnalysis, Params: testRenderParamsInstant()}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	decision, ok := findPhysicalDecisionByKind(rq.PhysicalDecisions, "metadata_lookup_filter_pushdown")
	if !ok || decision.Strategy != "not_applied" {
		t.Fatalf("expected metadata_lookup_filter_pushdown=not_applied decision, got %#v", rq.PhysicalDecisions)
	}
	if strings.Contains(rq.SQL, "AS metadata_rhs WHERE") {
		t.Fatalf("did not expect injected metadata filter wrapper for not_applied case, got:\n%s", rq.SQL)
	}
}
