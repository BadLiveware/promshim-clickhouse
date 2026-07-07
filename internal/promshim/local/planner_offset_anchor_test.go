package local

// Regression tests for issue #36 in the tier-4 local executor:
// rate/increase with offset (or @) fetched the correct sample window but
// extrapolated against [eval-range, eval] instead of
// [eval-offset-range, eval-offset]. The adjacent delta gap is covered in
// planner_delta_extrapolation_test.go.
//
// The child plan is replaced with a static matrix so the tests exercise
// only the plan-level anchor math against analytically exact expected
// values (constant-rate counter / linear gauge, where Prometheus's
// extrapolatedRate is closed-form).

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/model"
)

type staticMatrixPlan struct {
	matrix model.MatrixValue
}

func (p *staticMatrixPlan) execute(_ context.Context, _ *Evaluator, _ EvalParams) (model.RuntimeValue, error) {
	return p.matrix, nil
}

func (p *staticMatrixPlan) explain() ExplainNode {
	return ExplainNode{Kind: "static_matrix", Strategy: "test"}
}

const (
	anchorTestEvalSec = 1_700_000_000.0
	anchorTestRateSec = 91.3
)

func localOnlyPlanContext() PlanContext {
	ctx := DefaultPlanContext(EvalModeInstant)
	ctx.NativeLoweringMode = NativeLoweringModeOff
	return ctx
}

// counterWindowMatrix returns a monotonic counter (91.3/s, 15s scrape)
// whose samples exactly cover (windowEnd-300s, windowEnd].
func counterWindowMatrix(windowEnd float64) model.MatrixValue {
	points := make([]model.RangePoint, 0, 21)
	for ts := windowEnd - 300.0; ts <= windowEnd+1e-9; ts += 15.0 {
		points = append(points, model.RangePoint{Timestamp: ts, Value: anchorTestRateSec * ts})
	}
	return model.MatrixValue{Series: []model.RangeSeries{{
		Metric: map[string]string{"__name__": "test_counter", "job": "api"},
		Values: points,
	}}}
}

func executeInstantPlanForAnchorTest(t *testing.T, query string, child Plan) model.VectorValue {
	t.Helper()
	expr, err := logical.ParseExpression(query)
	if err != nil {
		t.Fatalf("parse %q: %v", query, err)
	}
	plan, err := buildPlanWithContext(expr, localOnlyPlanContext())
	if err != nil {
		t.Fatalf("buildPlanWithContext(%q): %v", query, err)
	}
	switch p := plan.(type) {
	case *localRatePlan:
		p.Child = child
	case *localIncreasePlan:
		p.Child = child
	case *localDeltaPlan:
		p.Child = child
	default:
		t.Fatalf("unexpected plan kind %T for %q", plan, query)
	}
	result, err := plan.execute(context.Background(), nil, EvalParams{
		Mode:           EvalModeInstant,
		EvaluationTime: time.Unix(int64(anchorTestEvalSec), 0),
	})
	if err != nil {
		t.Fatalf("execute %q: %v", query, err)
	}
	vector, ok := result.(model.VectorValue)
	if !ok {
		t.Fatalf("expected vector result for %q, got %T", query, result)
	}
	if len(vector.Samples) != 1 {
		t.Fatalf("expected one sample for %q, got %#v", query, vector.Samples)
	}
	return vector
}

func TestLocalRatePlanOffsetShiftsExtrapolationAnchor(t *testing.T) {
	// rate(test_counter[5m] offset 30m): samples cover (eval-35m, eval-30m].
	// With the anchor shifted by the offset the factor is 1 and the rate is
	// exactly 91.3/s; anchored at the raw eval time the factor flips to
	// -4.975 and the rate comes out ≈ -454.2 (the observed lab defect).
	child := &staticMatrixPlan{matrix: counterWindowMatrix(anchorTestEvalSec - 1800.0)}
	vector := executeInstantPlanForAnchorTest(t, `rate(test_counter[5m] offset 30m)`, child)
	if got := vector.Samples[0].Value; math.Abs(got-anchorTestRateSec) > 1e-9 {
		t.Errorf("rate with offset 30m: expected %.3f, got %.3f", anchorTestRateSec, got)
	}
}

func TestLocalRatePlanAtModifierShiftsExtrapolationAnchor(t *testing.T) {
	// rate(test_counter[5m] @ 1699998200): the anchor is the @ time.
	child := &staticMatrixPlan{matrix: counterWindowMatrix(1_699_998_200.0)}
	vector := executeInstantPlanForAnchorTest(t, `rate(test_counter[5m] @ 1699998200)`, child)
	if got := vector.Samples[0].Value; math.Abs(got-anchorTestRateSec) > 1e-9 {
		t.Errorf("rate with @: expected %.3f, got %.3f", anchorTestRateSec, got)
	}
}

func TestLocalIncreasePlanOffsetShiftsExtrapolationAnchor(t *testing.T) {
	child := &staticMatrixPlan{matrix: counterWindowMatrix(anchorTestEvalSec - 1800.0)}
	vector := executeInstantPlanForAnchorTest(t, `increase(test_counter[5m] offset 30m)`, child)
	expected := anchorTestRateSec * 300.0
	if got := vector.Samples[0].Value; math.Abs(got-expected) > 1e-6 {
		t.Errorf("increase with offset 30m: expected %.3f, got %.3f", expected, got)
	}
}

// The tests below cover subquery arguments: rangeFunctionOffset and
// rangeFunctionTimestampMS read the *subquery's* own offset/@ (Prometheus's
// evalSubquery synthesizes a MatrixSelector carrying them), so the tier-4
// anchor math must shift by them exactly as for a matrix selector. The child
// (the evaluated subquery samples) is stubbed with the same static matrix
// used above, so the assertions isolate the plan-level anchor arithmetic.

func TestLocalRatePlanSubqueryOffsetShiftsExtrapolationAnchor(t *testing.T) {
	// rate(test_counter[5m:15s] offset 30m): the subquery offset shifts the
	// extrapolation window end to eval-30m. Samples cover (eval-35m, eval-30m],
	// so the factor is 1 and the rate is exactly 91.3/s. Without the offset
	// subtraction the anchor stays at the raw eval time and the factor flips
	// negative (the issue #36 defect).
	child := &staticMatrixPlan{matrix: counterWindowMatrix(anchorTestEvalSec - 1800.0)}
	vector := executeInstantPlanForAnchorTest(t, `rate(test_counter[5m:15s] offset 30m)`, child)
	if got := vector.Samples[0].Value; math.Abs(got-anchorTestRateSec) > 1e-9 {
		t.Errorf("subquery rate with offset 30m: expected %.3f, got %.3f", anchorTestRateSec, got)
	}
}

func TestLocalRatePlanSubqueryAtModifierShiftsExtrapolationAnchor(t *testing.T) {
	// rate(test_counter[5m:15s] @ 1699998200): the subquery @ pins the anchor
	// to the @ time regardless of the outer evaluation time.
	child := &staticMatrixPlan{matrix: counterWindowMatrix(1_699_998_200.0)}
	vector := executeInstantPlanForAnchorTest(t, `rate(test_counter[5m:15s] @ 1699998200)`, child)
	if got := vector.Samples[0].Value; math.Abs(got-anchorTestRateSec) > 1e-9 {
		t.Errorf("subquery rate with @: expected %.3f, got %.3f", anchorTestRateSec, got)
	}
}

func TestLocalRatePlanSubqueryAtWithOffsetShiftsExtrapolationAnchor(t *testing.T) {
	// rate(test_counter[5m:15s] @ 1700001800 offset 30m): the effective anchor
	// is @ts - offset = 1700001800 - 1800 = 1700000000 (== anchorTestEvalSec),
	// so the @ and the offset combine. Samples cover (anchor-5m, anchor], factor
	// 1, rate exactly 91.3/s.
	child := &staticMatrixPlan{matrix: counterWindowMatrix(anchorTestEvalSec)}
	vector := executeInstantPlanForAnchorTest(t, `rate(test_counter[5m:15s] @ 1700001800 offset 30m)`, child)
	if got := vector.Samples[0].Value; math.Abs(got-anchorTestRateSec) > 1e-9 {
		t.Errorf("subquery rate with @ + offset: expected %.3f, got %.3f", anchorTestRateSec, got)
	}
}

