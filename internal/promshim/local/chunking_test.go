package local

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/model"
)

type syntheticRangePlan struct{}

func (syntheticRangePlan) execute(_ context.Context, _ *Evaluator, params EvalParams) (model.RuntimeValue, error) {
	values := make([]model.RangePoint, 0)
	for current := params.Start; !current.After(params.End); current = current.Add(params.Step) {
		values = append(values, model.RangePoint{Timestamp: float64(current.Unix()), Value: float64(current.Unix())})
	}
	return model.MatrixValue{Series: []model.RangeSeries{{Metric: map[string]string{"job": "synthetic"}, Values: values}}}, nil
}

func (syntheticRangePlan) explain() ExplainNode {
	return ExplainNode{Kind: "synthetic", Strategy: "local", Expr: "synthetic"}
}

func TestBuildPlanWithContextRejectsRangeQueryOverGuardrail(t *testing.T) {
	expr, err := logical.ParseExpression("up")
	if err != nil {
		t.Fatal(err)
	}

	_, err = buildPlanWithContext(expr, PlanContext{
		Mode:                      EvalModeRange,
		Start:                     time.Unix(0, 0).UTC(),
		End:                       time.Unix(600, 0).UTC(),
		Step:                      30 * time.Second,
		MaxRangePointsPerSeries:   10,
		RangeChunkPointsPerSeries: 5,
	})
	if err == nil {
		t.Fatal("expected range guardrail error")
	}
	if internalErrorKindOf(err) != internalErrorKindBadData {
		t.Fatalf("expected bad_data error kind, got %v (%v)", internalErrorKindOf(err), err)
	}
	if !strings.Contains(err.Error(), "exceeding configured limit") {
		t.Fatalf("expected guardrail error message, got %q", err.Error())
	}
}

func TestBuildPlanWithContextWrapsLargeLocalRangePlanInChunkedRangePlan(t *testing.T) {
	expr, err := logical.ParseExpression(`sum by (job) (up)`)
	if err != nil {
		t.Fatal(err)
	}

	plan, err := buildPlanWithContext(expr, PlanContext{
		Mode:                      EvalModeRange,
		Start:                     time.Unix(0, 0).UTC(),
		End:                       time.Unix(600, 0).UTC(),
		Step:                      30 * time.Second,
		MaxRangePointsPerSeries:   100,
		RangeChunkPointsPerSeries: 5,
	})
	if err != nil {
		t.Fatalf("expected chunked plan, got error: %v", err)
	}
	chunked, ok := plan.(*chunkedRangePlan)
	if !ok {
		t.Fatalf("expected chunkedRangePlan, got %T", plan)
	}
	if _, ok := chunked.Child.(*localAggregationPlan); !ok {
		t.Fatalf("expected localAggregationPlan child, got %T", chunked.Child)
	}
}

func TestBuildPlanWithContextWrapsLargeNativeRangePlanInChunkedRangePlan(t *testing.T) {
	expr, err := logical.ParseExpression(`sum by (job) (rate(up[5m]))`)
	if err != nil {
		t.Fatal(err)
	}

	plan, err := buildPlanWithContext(expr, PlanContext{
		Mode:                            EvalModeRange,
		Start:                           time.Unix(0, 0).UTC(),
		End:                             time.Unix(600, 0).UTC(),
		Step:                            time.Minute,
		NativeLoweringMode:              NativeLoweringModeForceSupported,
		EnableNativeGridFunctions:       true,
		MaxRangePointsPerSeries:         100,
		RangeChunkPointsPerSeries:       5000,
		NativeRangeChunkPointsPerSeries: 5,
	})
	if err != nil {
		t.Fatalf("expected chunked native plan, got error: %v", err)
	}
	chunked, ok := plan.(*chunkedRangePlan)
	if !ok {
		t.Fatalf("expected chunkedRangePlan, got %T", plan)
	}
	nativeChild, ok := chunked.Child.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan child, got %T", chunked.Child)
	}
	if nativeChild.LogicalRoot == nil || nativeChild.LogicalAnalysis == nil {
		t.Fatalf("expected chunked native child to retain logical lowering metadata")
	}
	explain := chunked.explain()
	if explain.Strategy != "chunked_native" {
		t.Fatalf("chunked native strategy = %q, want chunked_native", explain.Strategy)
	}
	if explain.ChunkPointsPerSeries != 5 {
		t.Fatalf("chunked native explain chunk points = %d, want 5", explain.ChunkPointsPerSeries)
	}
}

func TestBuildPlanWithContextChunksCumulativeAvgWithTwoChunkDefault(t *testing.T) {
	expr, err := logical.ParseExpression(`sum by (job, type) (avg_over_time(demo_memory_usage_bytes[1h]))`)
	if err != nil {
		t.Fatal(err)
	}

	plan, err := buildPlanWithContext(expr, PlanContext{
		Mode:                            EvalModeRange,
		Start:                           time.Unix(0, 0).UTC(),
		End:                             time.Unix(1440*60, 0).UTC(),
		Step:                            time.Minute,
		NativeLoweringMode:              NativeLoweringModeForceSupported,
		EnableCumulativeAvgOverTime:     true,
		MaxRangePointsPerSeries:         5000,
		RangeChunkPointsPerSeries:       5000,
		NativeRangeChunkPointsPerSeries: DefaultNativeRangeChunkPointsPerSeries,
	})
	if err != nil {
		t.Fatalf("expected chunked cumulative native plan, got error: %v", err)
	}
	chunked, ok := plan.(*chunkedRangePlan)
	if !ok {
		t.Fatalf("expected chunkedRangePlan, got %T", plan)
	}
	if chunked.ChunkPointsPerSeries != 721 {
		t.Fatalf("chunk points = %d, want 721", chunked.ChunkPointsPerSeries)
	}
	if chunked.Reason != "chunking cumulative avg_over_time range SQL to cap ClickHouse peak memory" {
		t.Fatalf("reason = %q", chunked.Reason)
	}
}

func TestBuildPlanWithContextLeavesDefaultNativeGridSumUnchunkedWithinDurationCap(t *testing.T) {
	expr, err := logical.ParseExpression(`sum by (job) (rate(demo_cpu_usage_seconds_total[5m]))`)
	if err != nil {
		t.Fatal(err)
	}

	plan, err := buildPlanWithContext(expr, PlanContext{
		Mode:                            EvalModeRange,
		Start:                           time.Unix(0, 0).UTC(),
		End:                             time.Unix(1440*60, 0).UTC(),
		Step:                            time.Minute,
		NativeLoweringMode:              NativeLoweringModeForceSupported,
		EnableNativeGridFunctions:       true,
		MaxRangePointsPerSeries:         5000,
		RangeChunkPointsPerSeries:       5000,
		NativeRangeChunkPointsPerSeries: DefaultNativeRangeChunkPointsPerSeries,
		NativeRangeChunkMaxDuration:     DefaultNativeRangeChunkMaxDuration,
		NativeRangeChunkMaxChunks:       DefaultNativeRangeChunkMaxChunks,
	})
	if err != nil {
		t.Fatalf("expected native grid sum plan, got error: %v", err)
	}
	if _, ok := plan.(*chunkedRangePlan); ok {
		t.Fatalf("expected default native grid sum plan within duration cap to remain unchunked, got %T", plan)
	}
	nativeChild, ok := plan.(*nativeSubtreePlan)
	if !ok {
		t.Fatalf("expected nativeSubtreePlan, got %T", plan)
	}
	if nativeChild.OptimizationReport == nil {
		t.Fatal("expected native optimization report")
	}
}

func TestNativeRangeChunkPointsUsesDurationCap(t *testing.T) {
	got := nativeRangeChunkPointsPerSeries(PlanContext{
		Step:                            15 * time.Minute,
		NativeRangeChunkPointsPerSeries: 289,
		NativeRangeChunkMaxDuration:     24 * time.Hour,
		NativeRangeChunkMaxChunks:       12,
	}, &planEstimate{PointsPerSeries: 673})
	if got != 97 {
		t.Fatalf("native chunk points = %d, want 97", got)
	}
}

func TestNativeRangeChunkPointsRespectsMaxChunks(t *testing.T) {
	got := nativeRangeChunkPointsPerSeries(PlanContext{
		Step:                            15 * time.Minute,
		NativeRangeChunkPointsPerSeries: 289,
		NativeRangeChunkMaxDuration:     24 * time.Hour,
		NativeRangeChunkMaxChunks:       12,
	}, &planEstimate{PointsPerSeries: 2881})
	if got != 241 {
		t.Fatalf("native chunk points = %d, want 241", got)
	}
}

func TestNativeRangeChunkPointsCanBeDisabled(t *testing.T) {
	got := nativeRangeChunkPointsPerSeries(PlanContext{
		Step:                            15 * time.Minute,
		NativeRangeChunkPointsPerSeries: 0,
		NativeRangeChunkMaxDuration:     24 * time.Hour,
		NativeRangeChunkMaxChunks:       12,
	}, &planEstimate{PointsPerSeries: 673})
	if got != 0 {
		t.Fatalf("native chunk points = %d, want disabled", got)
	}
}

func TestChunkedRangePlanExecutesAndMergesChunks(t *testing.T) {
	plan := &chunkedRangePlan{
		Child:                syntheticRangePlan{},
		ChunkPointsPerSeries: 2,
		Reason:               "test chunking",
		Estimate:             &planEstimate{PointsPerSeries: 4},
	}

	value, err := plan.execute(context.Background(), nil, EvalParams{
		Mode:  EvalModeRange,
		Start: time.Unix(0, 0).UTC(),
		End:   time.Unix(90, 0).UTC(),
		Step:  30 * time.Second,
	})
	if err != nil {
		t.Fatalf("expected merged chunked result, got error: %v", err)
	}
	matrix, ok := value.(model.MatrixValue)
	if !ok {
		t.Fatalf("expected model.MatrixValue, got %T", value)
	}
	if len(matrix.Series) != 1 {
		t.Fatalf("expected one series, got %#v", matrix.Series)
	}
	if len(matrix.Series[0].Values) != 4 {
		t.Fatalf("expected four merged points, got %#v", matrix.Series[0].Values)
	}
	if matrix.Series[0].Values[0].Timestamp != 0 || matrix.Series[0].Values[3].Timestamp != 90 {
		t.Fatalf("unexpected merged timestamps: %#v", matrix.Series[0].Values)
	}
}
