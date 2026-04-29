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
	if strategy := chunked.explain().Strategy; strategy != "chunked_native" {
		t.Fatalf("chunked native strategy = %q, want chunked_native", strategy)
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
