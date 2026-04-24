package native

import (
	"testing"
	"time"

	logicalpkg "github.com/BadLiveware/promshim-ch/internal/promshim/logical"

	"github.com/prometheus/prometheus/promql/parser"
)

// TestAnalyzeShapeLiftsLeafInstantVectorSelector verifies that a bare
// instant-vector selector's analysis Shape mirrors what the renderer
// currently reads off fragment.Selector.Kind / Lookback / Offset.
func TestAnalyzeShapeLiftsLeafInstantVectorSelector(t *testing.T) {
	expr := mustParseExpr(t, `up offset 90s`)
	leaf := &logicalpkg.LeafExprPlan{Expr: expr}

	info := Analyze(leaf).InfoFor(leaf)
	if info == nil {
		t.Fatal("expected lowering info")
	}
	if !info.Shape.HasSelector {
		t.Fatalf("expected HasSelector=true for vector-selector leaf, got %#v", info.Shape)
	}
	if info.Shape.SelectorKind != SelectorKindInstantVector {
		t.Fatalf("expected SelectorKindInstantVector, got %q", info.Shape.SelectorKind)
	}
	if info.Shape.SelectorLookback != DefaultInstantSelectorLookback {
		t.Fatalf("expected SelectorLookback=%s, got %s", DefaultInstantSelectorLookback, info.Shape.SelectorLookback)
	}
	if info.Shape.SelectorOffset != 90*time.Second {
		t.Fatalf("expected SelectorOffset=90s, got %s", info.Shape.SelectorOffset)
	}
	// Parity check against the LeafSelector side-map field (the
	// post-retirement equivalent of fragment.Selector).
	if info.LeafSelector == nil {
		t.Fatalf("expected leaf selector to be populated for leaf")
	}
	if info.LeafSelector.Kind != info.Shape.SelectorKind {
		t.Fatalf("leaf selector vs shape kind mismatch: %q vs %q", info.LeafSelector.Kind, info.Shape.SelectorKind)
	}
	if info.LeafSelector.Lookback != info.Shape.SelectorLookback {
		t.Fatalf("leaf selector vs shape lookback mismatch: %s vs %s", info.LeafSelector.Lookback, info.Shape.SelectorLookback)
	}
	if info.LeafSelector.Offset != info.Shape.SelectorOffset {
		t.Fatalf("leaf selector vs shape offset mismatch: %s vs %s", info.LeafSelector.Offset, info.Shape.SelectorOffset)
	}
}

// TestAnalyzeShapeLiftsLeafRangeVectorSelector verifies that a matrix
// selector leaf's Shape carries RangeVector kind and the matrix Range
// as SelectorLookback, matching fragment.Selector.
func TestAnalyzeShapeLiftsLeafRangeVectorSelector(t *testing.T) {
	expr := mustParseExpr(t, `rate(http_requests_total[2m] offset 30s)`)
	call := expr.(*parser.Call)
	matrix := call.Args[0].(*parser.MatrixSelector)
	leaf := &logicalpkg.LeafExprPlan{Expr: matrix}

	info := Analyze(leaf).InfoFor(leaf)
	if info == nil {
		t.Fatal("expected lowering info")
	}
	if !info.Shape.HasSelector {
		t.Fatalf("expected HasSelector=true for matrix-selector leaf, got %#v", info.Shape)
	}
	if info.Shape.SelectorKind != SelectorKindRangeVector {
		t.Fatalf("expected SelectorKindRangeVector, got %q", info.Shape.SelectorKind)
	}
	if info.Shape.SelectorLookback != 2*time.Minute {
		t.Fatalf("expected SelectorLookback=2m, got %s", info.Shape.SelectorLookback)
	}
	if info.Shape.SelectorOffset != 30*time.Second {
		t.Fatalf("expected SelectorOffset=30s, got %s", info.Shape.SelectorOffset)
	}
}

// TestAnalyzeShapeForScalarLiteralHasNoSelector covers the
// LiteralSeries-style shape: scalar literals carry no base selector.
func TestAnalyzeShapeForScalarLiteralHasNoSelector(t *testing.T) {
	scalar := &logicalpkg.ScalarLiteralPlan{Value: 42}
	info := Analyze(scalar).InfoFor(scalar)
	if info == nil {
		t.Fatal("expected lowering info")
	}
	if info.Shape.HasSelector {
		t.Fatalf("expected HasSelector=false for scalar literal, got %#v", info.Shape)
	}
	if info.Shape.HasFixedTemporalAnchor {
		t.Fatalf("expected HasFixedTemporalAnchor=false for scalar literal, got %#v", info.Shape)
	}
}

// TestAnalyzeShapeForSyntheticSeriesFunctionHasNoSelector covers the
// synthetic-series scalar builtin shape (time, pi, etc.) — no base
// selector, no fixed anchor.
func TestAnalyzeShapeForSyntheticSeriesFunctionHasNoSelector(t *testing.T) {
	expr := mustParseExpr(t, `time()`)
	call := expr.(*parser.Call)
	plan := &logicalpkg.ScalarBuiltinPlan{Expr: call, Func: "time"}
	info := Analyze(plan).InfoFor(plan)
	if info == nil {
		t.Fatal("expected lowering info")
	}
	if info.Shape.HasSelector {
		t.Fatalf("expected HasSelector=false for time() builtin, got %#v", info.Shape)
	}
}

// TestAnalyzeShapeLiftsSelectorWithFixedAnchor verifies that an @
// timestamp anchor on a selector propagates as
// HasFixedTemporalAnchor=true through Shape.
func TestAnalyzeShapeLiftsSelectorWithFixedAnchor(t *testing.T) {
	expr := mustParseExpr(t, `up @ 1000`)
	leaf := &logicalpkg.LeafExprPlan{Expr: expr}
	info := Analyze(leaf).InfoFor(leaf)
	if info == nil {
		t.Fatal("expected lowering info")
	}
	if !info.Shape.HasFixedTemporalAnchor {
		t.Fatalf("expected HasFixedTemporalAnchor=true for @-anchored selector, got %#v", info.Shape)
	}
	if info.Shape.SelectorTimestamp == nil || *info.Shape.SelectorTimestamp == 0 {
		t.Fatalf("expected non-nil SelectorTimestamp carrying the anchor, got %#v", info.Shape.SelectorTimestamp)
	}
}

// TestAnalyzeShapeLiftsStartEndAnchor verifies start()/end() anchors
// propagate as HasFixedTemporalAnchor=true and set SelectorStartOrEnd.
func TestAnalyzeShapeLiftsStartEndAnchor(t *testing.T) {
	expr := mustParseExpr(t, `up @ start()`)
	leaf := &logicalpkg.LeafExprPlan{Expr: expr}
	info := Analyze(leaf).InfoFor(leaf)
	if info == nil {
		t.Fatal("expected lowering info")
	}
	if info.Shape.SelectorStartOrEnd != parser.START {
		t.Fatalf("expected SelectorStartOrEnd=START, got %v", info.Shape.SelectorStartOrEnd)
	}
	if !info.Shape.HasFixedTemporalAnchor {
		t.Fatalf("expected HasFixedTemporalAnchor=true for start() anchor, got %#v", info.Shape)
	}
}

// TestAnalyzeShapePropagatesFixedAnchorThroughAggregation verifies that
// a fixed temporal anchor on a leaf selector is visible on an
// aggregation ancestor's Shape.
func TestAnalyzeShapePropagatesFixedAnchorThroughAggregation(t *testing.T) {
	expr := mustParseExpr(t, `sum(up @ 1000)`)
	agg := expr.(*parser.AggregateExpr)
	logical := &logicalpkg.AggregationPlan{
		Expr:     agg,
		Op:       agg.Op,
		Grouping: append([]string(nil), agg.Grouping...),
		Child:    &logicalpkg.LeafExprPlan{Expr: agg.Expr},
	}
	info := Analyze(logical).InfoFor(logical)
	if info == nil {
		t.Fatal("expected lowering info")
	}
	if !info.Shape.HasFixedTemporalAnchor {
		t.Fatalf("expected HasFixedTemporalAnchor=true for sum(up @ 1000), got %#v", info.Shape)
	}
	// HasFixedTemporalAnchorNode helper exposes the same value via the
	// analysis side-map lookup.
	if !HasFixedTemporalAnchorNode(Analyze(logical), logical) {
		t.Fatalf("expected HasFixedTemporalAnchorNode=true for sum(up @ 1000)")
	}
}

// TestAnalyzeShapeNoFixedAnchorForUnanchoredTree verifies that a tree
// without any @/start()/end() anchors reports
// HasFixedTemporalAnchor=false.
func TestAnalyzeShapeNoFixedAnchorForUnanchoredTree(t *testing.T) {
	expr := mustParseExpr(t, `sum(rate(http_requests_total[5m]))`)
	agg := expr.(*parser.AggregateExpr)
	rateCall := agg.Expr.(*parser.Call)
	matrix := rateCall.Args[0].(*parser.MatrixSelector)
	rangeLeaf := &logicalpkg.LeafExprPlan{Expr: matrix}
	ratePlan := &logicalpkg.RatePlan{Expr: rateCall, Func: "rate", Child: rangeLeaf}
	logical := &logicalpkg.AggregationPlan{Expr: agg, Op: agg.Op, Child: ratePlan}

	analysis := Analyze(logical)
	info := analysis.InfoFor(logical)
	if info == nil {
		t.Fatal("expected lowering info")
	}
	if info.Shape.HasFixedTemporalAnchor {
		t.Fatalf("expected HasFixedTemporalAnchor=false for sum(rate(...)), got %#v", info.Shape)
	}
	if !info.Shape.HasSelector {
		t.Fatalf("expected HasSelector=true via chain to range-vector leaf, got %#v", info.Shape)
	}
	if info.Shape.SelectorKind != SelectorKindRangeVector {
		t.Fatalf("expected inherited SelectorKindRangeVector, got %q", info.Shape.SelectorKind)
	}
	if info.Shape.SelectorLookback != 5*time.Minute {
		t.Fatalf("expected inherited SelectorLookback=5m, got %s", info.Shape.SelectorLookback)
	}
}

// TestAnalyzeShapeSubqueryAnchorPropagates verifies that a subquery's
// own @ anchor sets HasFixedTemporalAnchor on the subquery node's
// Shape even when the inner selector has no anchor.
func TestAnalyzeShapeSubqueryAnchorPropagates(t *testing.T) {
	expr := mustParseExpr(t, `sum(up)[5m:1m] @ 2000`)
	sub := expr.(*parser.SubqueryExpr)
	plan := &logicalpkg.SubqueryPlan{
		Expr:       sub,
		Range:      sub.Range,
		Step:       sub.Step,
		Offset:     sub.OriginalOffset,
		Timestamp:  sub.Timestamp,
		StartOrEnd: sub.StartOrEnd,
		Child: &logicalpkg.AggregationPlan{
			Expr: sub.Expr.(*parser.AggregateExpr),
			Op:   sub.Expr.(*parser.AggregateExpr).Op,
			Child: &logicalpkg.LeafExprPlan{Expr: sub.Expr.(*parser.AggregateExpr).Expr},
		},
	}
	info := Analyze(plan).InfoFor(plan)
	if info == nil {
		t.Fatal("expected lowering info")
	}
	if !info.Shape.HasFixedTemporalAnchor {
		t.Fatalf("expected subquery-level @ anchor to propagate, got %#v", info.Shape)
	}
}

// TestAnalyzeShapeOutputHasMetricNameForBareSelector verifies that a
// bare selector reports OutputHasMetricName=true. After 13c-14e this
// is structurally true for any HasSelector=true shape; the test keeps
// the signature to guard against the flag ever regressing.
func TestAnalyzeShapeOutputHasMetricNameForBareSelector(t *testing.T) {
	expr := mustParseExpr(t, `up`)
	leaf := &logicalpkg.LeafExprPlan{Expr: expr}
	info := Analyze(leaf).InfoFor(leaf)
	if info == nil {
		t.Fatal("expected lowering info")
	}
	if !info.Shape.OutputHasMetricName {
		t.Fatalf("expected OutputHasMetricName=true for bare selector, got %#v", info.Shape)
	}
	// Parity with the OutputHasMetricNameNode helper lookup.
	if !OutputHasMetricNameNode(Analyze(leaf), leaf) {
		t.Fatalf("expected OutputHasMetricNameNode=true for bare selector")
	}
}

// TestAnalyzeShapeOutputHasMetricNameDropsThroughAggregation verifies
// that aggregations inherit the OutputHasMetricName flag from their
// child selector shape, since aggregations don't re-wrap
// fragment.Selector at their own level.
func TestAnalyzeShapeOutputHasMetricNameDropsThroughAggregation(t *testing.T) {
	expr := mustParseExpr(t, `sum by (job) (up)`)
	agg := expr.(*parser.AggregateExpr)
	logical := &logicalpkg.AggregationPlan{
		Expr:     agg,
		Op:       agg.Op,
		Grouping: append([]string(nil), agg.Grouping...),
		Child:    &logicalpkg.LeafExprPlan{Expr: agg.Expr},
	}
	info := Analyze(logical).InfoFor(logical)
	if info == nil {
		t.Fatal("expected lowering info")
	}
	// After 13c-14e OutputHasMetricName is structurally true for any
	// HasSelector=true shape; the aggregation inherits HasSelector=true
	// from the child leaf and therefore OutputHasMetricName=true.
	if !info.Shape.OutputHasMetricName {
		t.Fatalf("expected aggregation over bare selector to inherit OutputHasMetricName=true, got %#v", info.Shape)
	}
}

// TestRangeFunctionChildIsLeafSelectorPredicate verifies the
// structural predicate for "range function child is a leaf selector".
func TestRangeFunctionChildIsLeafSelectorPredicate(t *testing.T) {
	matrix := mustParseExpr(t, `http_requests_total[5m]`).(*parser.MatrixSelector)
	leaf := &logicalpkg.LeafExprPlan{Expr: matrix}
	if !RangeFunctionChildIsLeafSelector(leaf) {
		t.Fatalf("expected leaf matrix selector to be detected as leaf selector")
	}

	vec := mustParseExpr(t, `http_requests_total`).(*parser.VectorSelector)
	leaf = &logicalpkg.LeafExprPlan{Expr: vec}
	if !RangeFunctionChildIsLeafSelector(leaf) {
		t.Fatalf("expected leaf vector selector to be detected as leaf selector")
	}

	// A composite child should not count as a leaf selector.
	agg := mustParseExpr(t, `sum(up)`).(*parser.AggregateExpr)
	aggPlan := &logicalpkg.AggregationPlan{Expr: agg, Op: agg.Op, Child: &logicalpkg.LeafExprPlan{Expr: agg.Expr}}
	if RangeFunctionChildIsLeafSelector(aggPlan) {
		t.Fatalf("expected aggregation child not to count as leaf selector")
	}
}

// TestSubqueryChildIsInstantVectorLoweringPredicate verifies the
// structural predicate for "subquery child lowers as an instant vector
// with a discoverable selector".
func TestSubqueryChildIsInstantVectorLoweringPredicate(t *testing.T) {
	expr := mustParseExpr(t, `up`)
	leaf := &logicalpkg.LeafExprPlan{Expr: expr}
	analysis := Analyze(leaf)
	if !SubqueryChildIsInstantVectorLowering(analysis, leaf) {
		t.Fatalf("expected bare instant-vector leaf to be a valid subquery child")
	}

	scalar := &logicalpkg.ScalarLiteralPlan{Value: 1}
	analysis = Analyze(scalar)
	if SubqueryChildIsInstantVectorLowering(analysis, scalar) {
		t.Fatalf("expected scalar literal to fail the subquery-child predicate")
	}
}
