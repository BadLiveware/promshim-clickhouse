package native

import (
	"time"

	logicalpkg "ch-observability/internal/promshim/logical"
	"github.com/prometheus/prometheus/promql/parser"
)

type OutputKind string

const (
	OutputKindUnknown       OutputKind = "unknown"
	OutputKindScalar        OutputKind = "scalar"
	OutputKindInstantVector OutputKind = "instant_vector"
	OutputKindRangeMatrix   OutputKind = "range_matrix"
)

type RenderMode string

const (
	RenderModeInstant RenderMode = "instant"
	RenderModeRange   RenderMode = "range"
)

// SubtreeShape is the tier-3 / analysis-time classification of a logical
// node's lowered shape. Populated during Analyze from the logical walk.
type SubtreeShape string

const (
	SubtreeShapeLeafSource             SubtreeShape = "leaf_source"
	SubtreeShapeUnarySourceExpr        SubtreeShape = "unary_source_expression"
	SubtreeShapeBinaryScalarSourceExpr SubtreeShape = "binary_scalar_source_expression"
	SubtreeShapeBinaryVectorJoin       SubtreeShape = "binary_vector_join"
	SubtreeShapeRangeFunction          SubtreeShape = "range_function"
	SubtreeShapeSubquery               SubtreeShape = "subquery"
	SubtreeShapeAggregation            SubtreeShape = "aggregation"
	SubtreeShapeSyntheticSeries        SubtreeShape = "synthetic_series"
	SubtreeShapeScalarConvert          SubtreeShape = "scalar_convert"
	SubtreeShapeInfoJoin               SubtreeShape = "info_join"
	SubtreeShapeAbsent                 SubtreeShape = "absent"
	SubtreeShapeHistogramProjection    SubtreeShape = "histogram_projection"
	SubtreeShapeHistogramFunction      SubtreeShape = "histogram_function"
	SubtreeShapeSortTransform          SubtreeShape = "sort_transform"
	SubtreeShapeLabelTransform         SubtreeShape = "label_transform"
	SubtreeShapeClampTransform         SubtreeShape = "clamp_transform"
	SubtreeShapeValueTransform         SubtreeShape = "value_transform"
)

const (
	JoinShapeOneToOne   = "one_to_one"
	JoinShapeManyToOne  = "many_to_one"
	JoinShapeOneToMany  = "one_to_many"
	JoinShapeManyToMany = "many_to_many"
)

// SubqueryFragment is a sentinel marker populated onto
// LoweringInfo.RangeFunctionSubquery when a range function's child is a
// subquery. It carries no payload today — readers recover the subquery
// parameters from the logical tree via SubqueryPlan.Range/Step/Offset —
// and is retained only as a "subquery child present" signal so the
// emission sites on range-function plans stay uniform.
type SubqueryFragment struct{}

const RuntimeValueTransformPromQLModulo RuntimeValueTransformOp = "promql_modulo"

type RuntimeValueTransformOp string

type RuntimeValueTransform struct {
	Op           RuntimeValueTransformOp
	Scalar       *float64
	ScalarOnLeft bool
}

type AggregationSupport struct {
	Eligible bool
	Reason   string
	// SourceInfo points at the LoweringInfo for whichever child carries
	// the lowered source expression the aggregation-pushdown reader
	// needs.
	//
	// For the direct-child case this is info.Children[0] — the
	// AggregationPlan's single child.
	//
	// For the zero-fill case (sum(... or vector(0))) this is the
	// LoweringInfo of the non-zero arm of the LOR BinaryPlan — i.e.
	// whichever side of binary.LHS/binary.RHS is not vector(0).
	//
	// Nil when the aggregation has no native source (ineligible, or the
	// child did not produce a lowerable source expression).
	SourceInfo *LoweringInfo
	// SourceView captures the four-field slice of the aggregation source
	// that nativeAggregationSourceFromLowering currently reads off
	// Source (SourcePromQL / ValueExpr / TagsExpr / DropsMetric) —
	// mirrored here so the reader can drop the Fragment dereference.
	// Nil when the aggregation has no native source (Eligible=false or
	// the source fragment carries no SourcePromQL — the reader's gate).
	SourceView *AggregationSourceView
	// EmitZeroOnEmpty mirrors AggregationFragment.EmitZeroOnEmpty. Populated
	// during the Analyze walk for the `sum(... or vector(0))` zero-fill
	// shape so renderAggregationLogicalBody can branch on it without
	// dereferencing info.Fragment.Aggregation.
	EmitZeroOnEmpty bool
}

// AggregationSourceView mirrors the four NativeFragment fields that
// the tier-3 aggregation-pushdown reader
// (nativeAggregationSourceFromLowering) currently dereferences off
// info.Aggregation.Source: SourcePromQL, ValueExpr, TagsExpr,
// DropsMetric. Populated during the Analyze walk when the aggregation
// has a native-lowerable source fragment whose SourcePromQL is
// non-nil (the reader's gate). Nil otherwise.
type AggregationSourceView struct {
	SourcePromQL parser.Expr
	ValueExpr    string
	TagsExpr     string
	DropsMetric  bool
}

type TimeRequirements struct {
	Lookback              time.Duration
	Offset                time.Duration
	NeedsSubqueryStepGrid bool
}

type LoweringInfo struct {
	NodeType   string
	Expr       string
	OutputKind OutputKind

	NativeLowerable bool
	NativeReason    string

	Aggregation *AggregationSupport

	// DropsMetric indicates the node's lowered SQL strips __name__ from
	// the tags column. Populated during the Analyze walk alongside
	// SubtreeShape.
	DropsMetric bool

	// AbsentFunc mirrors Fragment.Absent.Func ("absent" or
	// "absent_over_time"). Populated on AbsentPlan / AbsentOverTimePlan
	// nodes so semanticBarriersFromLogical can distinguish the two
	// without reading info.Fragment.
	AbsentFunc string

	// HistogramFunc mirrors Fragment.HistogramFunction.Func
	// ("histogram_quantile", "histogram_fraction", "histogram_quantiles").
	// Populated alongside SubtreeShapeHistogramFunction so tier-3
	// dispatchers can gate on the specific histogram variant without
	// reading info.Fragment.
	HistogramFunc string

	LabelLineage     LabelLineage
	TimeRequirements TimeRequirements
	Children         []*LoweringInfo

	// Shape lifts selector-derived structural information off the
	// Fragment sub-struct onto the logical-side analysis record so
	// tier-2 helpers can eventually read it without dereferencing
	// NativeFragment. Populated during Analyze; the corresponding
	// Fragment fields remain authoritative until Task 13a-d ports
	// consumers over. See SelectorShape for field semantics.
	Shape SelectorShape

	// JoinShape mirrors fragment.BinaryJoin.JoinShape for nodes that
	// lower through the BinaryVectorJoin path. Non-empty only when the
	// node corresponds to a supported vector-vector join
	// (isSupportedNativeVectorJoinOp true + SupportedNativeVectorJoinShape
	// returned (shape, true)). Consumed by explain() to surface the
	// join cardinality without dereferencing NativeFragment.
	JoinShape string

	// JoinLabels mirrors VectorMatching.MatchingLabels on the binary
	// join for which JoinShape is set. Populated only when JoinShape is
	// non-empty.
	JoinLabels []string

	// RuntimeValueTransform mirrors fragment.ValueTransform.RuntimeTransform
	// for nodes whose ValueTransform fragment carries a post-SQL runtime
	// correction (currently: PromQL modulo correction on scalar-involving
	// modulo expressions). Consumed by nativeSubtreePlan.execute() to
	// apply the correction to decoded samples without dereferencing
	// NativeFragment.
	RuntimeValueTransform *RuntimeValueTransform

	// SubtreeShape mirrors Fragment.Kind for this node's analysis-time
	// Fragment, but typed as a distinct SubtreeShape so tier-3 consumers
	// no longer depend on the FragmentKind enum. Populated by a single
	// post-walk pass in Analyze. Reflects the pre-optimize Kind
	// (OptimizeFragment operates on a clone, so info.Fragment.Kind is
	// never mutated). Empty ("") when Fragment is nil.
	SubtreeShape SubtreeShape

	// LeafSelector mirrors fragment.Selector on the base leaf (Vector/
	// Matrix) selector reached from this node. Populated during the
	// Analyze walk for LeafExprPlan nodes so Lower can read the cached
	// selector without dereferencing info.Fragment.
	//
	// After 13c-14e native.SelectorSource carries no narrowing state;
	// tag-narrowing flows purely through RenderParams at render time.
	// The LeafSelector pointer is stable across a query's Analyze pass
	// and not cloned for rendering. Nil when the node is not a leaf
	// selector or when selector-source analysis failed.
	LeafSelector *SelectorSource

	// SourceExpr captures the "source expression" view the renderer needs
	// to emit SQL for nodes that lower through renderSourceFragment —
	// specifically LeafSource, UnarySourceExpr, and BinaryScalarSourceExpr
	// kinds. Populated during the Analyze walk for each lowerable node
	// of these shapes. Nil otherwise. See SourceExprView.
	SourceExpr *SourceExprView

	// ValueTransform mirrors the renderer's ValueTransform wrapping view
	// for BinaryPlan nodes whose scalar-involving shape lowers via
	// renderValueTransformFromSource (comparison filters/bool,
	// scalar-expression arms, synthetic-scalar arms). Populated during
	// the Analyze walk alongside the ValueTransformFragment; nil when
	// the node does not lower through that wrapper. See
	// ValueTransformView.
	ValueTransform *ValueTransformView

	// SyntheticSeries captures the scalar-literal fold for BinaryPlan
	// nodes whose two sides fold to a constant (e.g. `1 + 2`, `pi() +
	// pi()` via syntheticLiteralValue). The lowerer dispatches off this
	// view to renderScalarLiteralFragment without dereferencing
	// info.Fragment. Populated alongside
	// FragmentKindSyntheticSeries/OutputKindScalar with Func=="literal".
	// Nil otherwise. See SyntheticSeriesView.
	SyntheticSeries *SyntheticSeriesView

	// RangeFunctionSubquery mirrors RangeFunctionFragment.Child when the
	// range-function's child is a subquery fragment
	// (FragmentKindSubquery). Populated during the Analyze walk on each
	// of the seven range-function plan kinds (RangeFunctionPlan, RatePlan,
	// IncreasePlan, DeltaPlan, ChangesPlan, DerivPlan, QuantileOverTimePlan)
	// whenever that child is a Subquery fragment. Holds the same
	// *SubqueryFragment pointer stored in info.Fragment.RangeFunction.Child.
	// Subquery so the renderer can drive the subquery-fast-path branches
	// without dereferencing info.Fragment.RangeFunction. Nil otherwise.
	RangeFunctionSubquery *SubqueryFragment
}

// SourceExprView is the analysis-side mirror of the Selector / ValueExpr /
// TagsExpr / SourcePromQL / DropsMetric fields on NativeFragment for the
// source-expression kinds (LeafSource, UnarySourceExpr,
// BinaryScalarSourceExpr). It lets Lower render those shapes without
// dereferencing info.Fragment.
//
// Selector holds the same *SelectorSource pointer stored in
// info.Fragment.Selector; the remaining fields are value-typed and
// captured at Analyze time. After 13c-14e the selector carries no
// narrowing state — RenderParams is the single source of truth.
type SourceExprView struct {
	// Selector mirrors fragment.Selector (same pointer).
	Selector *SelectorSource
	// ValueExpr mirrors fragment.ValueExpr. For LeafSource this is
	// "{value}"; for UnarySourceExpr it is the composed pointwise template
	// (e.g. "abs({value})"); for BinaryScalarSourceExpr it carries the
	// scalar-involving operator template.
	ValueExpr string
	// TagsExpr mirrors fragment.TagsExpr.
	TagsExpr string
	// SourcePromQL mirrors fragment.SourcePromQL — non-nil only for
	// ResolveSourcePromQL-driven fallback paths that do not have a
	// native Selector. Rare in practice.
	SourcePromQL parser.Expr
	// DropsMetric mirrors fragment.DropsMetric.
	DropsMetric bool
}

// ValueTransformView is the analysis-side mirror of the ValueExpr /
// FilterExpr / DropsMetric fields on ValueTransformFragment for BinaryPlan
// nodes whose scalar-involving shape lowers via
// renderValueTransformFromSource. It lets Lower render those shapes
// without dereferencing info.Fragment.
//
// VectorChildOnLeft identifies which side of the enclosing BinaryPlan
// carries the non-scalar child whose SQL the ValueTransform wraps. When
// true the vector child is n.LHS (n.RHS is the scalar side); when false
// the vector child is n.RHS (n.LHS is the scalar side). Lower uses this
// flag to pick which child to recurse through for the wrapper's inner
// SELECT.
type ValueTransformView struct {
	// VectorChildOnLeft reports which BinaryPlan side carries the non-
	// scalar child (see struct doc).
	VectorChildOnLeft bool
	// ValueExpr mirrors ValueTransformFragment.ValueExpr — the inner
	// value template wrapped around the child's "value" column.
	ValueExpr string
	// FilterExpr mirrors ValueTransformFragment.FilterExpr — the filter
	// template for comparison-filter arms (empty otherwise).
	FilterExpr string
	// DropsMetric mirrors ValueTransformFragment.DropsMetric. Governs
	// whether the outer SELECT strips __name__ from the tags column.
	DropsMetric bool
}

// SyntheticSeriesView captures the analysis-side view of a synthetic
// series fragment: the folded scalar literal (Func=="literal") or the
// synthetic builtin name (time/pi/minute/hour/... — the Func string on
// the underlying SyntheticSeriesFragment). The enclosed Value is
// populated alongside Func=="literal" (e.g. 3 for `1+2`); Lower reads
// Func=="literal" + Value to render via renderScalarLiteralFragment
// without dereferencing info.Fragment.
type SyntheticSeriesView struct {
	// Func mirrors SyntheticSeriesFragment.Func — "literal" for folded
	// scalar literals (ScalarLiteralPlan / folded UnaryPlan / folded
	// BinaryPlan), and the builtin name ("time", "pi", "minute", "hour",
	// "day_of_week", "day_of_month", "day_of_year", "days_in_month",
	// "month", "year") for synthetic scalar / date functions.
	Func string
	// Value is the folded scalar result (e.g. 3 for `1+2`). Only
	// meaningful when Func=="literal".
	Value float64
}

// SelectorShape carries per-node selector/shape metadata that tier-2
// renderer helpers currently read from NativeFragment sub-structs
// (fragment.Selector.Kind / Lookback / Offset / Timestamp / StartOrEnd
// and the HasFixedTemporalAnchor tree walk). It is populated during the
// native.Analyze walk and available via Analysis.InfoFor(node).Shape.
//
// HasSelector reports whether this node (directly, for leaf selectors)
// or its descendants carry a base selector whose Kind/Lookback/Offset
// are meaningful. Nodes with HasSelector=false (e.g., scalar literals,
// synthetic series, pure scalar BinaryPlans) leave the remaining
// selector fields zero-valued — callers must gate on HasSelector before
// treating Lookback/Offset as authoritative.
type SelectorShape struct {
	// HasSelector is true when a base selector is discoverable from
	// this node (either directly, for a LeafExprPlan wrapping a
	// Vector/MatrixSelector, or transitively through descent). When
	// false, SelectorKind/Lookback/Offset/Timestamp/StartOrEnd are all
	// zero-valued.
	HasSelector bool

	// SelectorKind mirrors fragment.Selector.Kind for the base
	// selector reached from this node (InstantVector for plain vector
	// selectors, RangeVector for matrix selectors).
	SelectorKind SelectorKind

	// SelectorLookback mirrors fragment.Selector.Lookback on the base
	// selector. For instant-vector selectors this is
	// DefaultInstantSelectorLookback (the staleness window). For
	// range-vector selectors this is the matrix range.
	SelectorLookback time.Duration

	// SelectorOffset mirrors fragment.Selector.Offset on the base
	// selector (VectorSelector.OriginalOffset).
	SelectorOffset time.Duration

	// SelectorTimestamp mirrors fragment.Selector.Timestamp (nil unless
	// the selector uses an @ anchor).
	SelectorTimestamp *int64

	// SelectorStartOrEnd mirrors fragment.Selector.StartOrEnd
	// (parser.START / parser.END for start()/end() anchors, zero
	// otherwise).
	SelectorStartOrEnd parser.ItemType

	// HasFixedTemporalAnchor mirrors native.HasFixedTemporalAnchor on
	// the Fragment subtree rooted at this node: true when any node in
	// the subtree pins evaluation to an @ timestamp or start()/end().
	// Computed transitively during Analyze using child InfoFor lookups
	// so later tier-2 ports can drop the Fragment-side recursion.
	HasFixedTemporalAnchor bool

	// OutputHasMetricName is structurally true whenever HasSelector is
	// true. After 13c-14e native.SelectorSource carries no narrowing
	// state — a base selector always emits __name__ as part of its
	// tags; render-time RenderParams narrowing strips it later. The
	// field is retained so existing callers (OutputHasMetricNameNode
	// and its tests) keep their signature, but it is no longer a
	// shape-dependent signal.
	OutputHasMetricName bool
}

type Analysis struct {
	Root   *LoweringInfo
	byNode map[logicalpkg.Node]*LoweringInfo
}

func Analyze(plan logicalpkg.Node) *Analysis {
	analysis := &Analysis{byNode: map[logicalpkg.Node]*LoweringInfo{}}
	analysis.Root = analysis.walk(plan)
	return analysis
}

func (a *Analysis) InfoFor(node logicalpkg.Node) *LoweringInfo {
	if a == nil || node == nil {
		return nil
	}
	return a.byNode[node]
}

type ExplainLabelLineage struct {
	Known      map[string]string `json:"known,omitempty"`
	Wildcard   string            `json:"wildcard,omitempty"`
	MetricName string            `json:"metricName,omitempty"`
}

type ExplainInfo struct {
	NodeType                    string               `json:"nodeType,omitempty"`
	OutputKind                  string               `json:"outputKind,omitempty"`
	NativeLowerable             bool                 `json:"nativeLowerable"`
	Reason                      string               `json:"reason,omitempty"`
	FragmentKind                string               `json:"fragmentKind,omitempty"`
	AggregationPushdownEligible bool                 `json:"aggregationPushdownEligible,omitempty"`
	AggregationReason           string               `json:"aggregationReason,omitempty"`
	RequiredLookbackSeconds     float64              `json:"requiredLookbackSeconds,omitempty"`
	RequiredOffsetSeconds       float64              `json:"requiredOffsetSeconds,omitempty"`
	NeedsSubqueryStepGrid       bool                 `json:"needsSubqueryStepGrid,omitempty"`
	LabelLineage                *ExplainLabelLineage `json:"labelLineage,omitempty"`
}

func (info *LoweringInfo) ExplainInfo() *ExplainInfo {
	if info == nil {
		return nil
	}
	explain := &ExplainInfo{
		NodeType:                info.NodeType,
		OutputKind:              string(info.OutputKind),
		NativeLowerable:         info.NativeLowerable,
		Reason:                  info.NativeReason,
		RequiredLookbackSeconds: info.TimeRequirements.Lookback.Seconds(),
		RequiredOffsetSeconds:   info.TimeRequirements.Offset.Seconds(),
		NeedsSubqueryStepGrid:   info.TimeRequirements.NeedsSubqueryStepGrid,
	}
	if info.SubtreeShape != "" {
		explain.FragmentKind = string(info.SubtreeShape)
	}
	if info.Aggregation != nil {
		explain.AggregationPushdownEligible = info.Aggregation.Eligible
		explain.AggregationReason = info.Aggregation.Reason
	}
	if lineage := info.LabelLineage.explain(); lineage != nil {
		explain.LabelLineage = lineage
	}
	return explain
}

// HasFixedTemporalAnchorNode mirrors HasFixedTemporalAnchor on the
// logical tree using the analysis side-map. It returns true when any
// node in the subtree rooted at node pins evaluation to an @ timestamp
// or start()/end(). Implementation reads the pre-computed
// Shape.HasFixedTemporalAnchor populated during Analyze. Returns false
// for nil analysis, nil node, or nodes with no recorded info.
//
// This exposes the "does this subtree contain a fixed temporal anchor"
// predicate via the logical-side lookup path so tier-2 helpers (e.g.,
// renderer.renderFragment's anchored-range early-out) can eventually
// drop their direct Fragment recursion.
func HasFixedTemporalAnchorNode(analysis *Analysis, node logicalpkg.Node) bool {
	info := analysis.InfoFor(node)
	if info == nil {
		return false
	}
	return info.Shape.HasFixedTemporalAnchor
}

// OutputHasMetricNameNode reports whether the base selector reachable
// from node emits __name__. After 13c-14e narrowing flows purely
// through RenderParams at render time, so this is structurally true
// whenever a base selector is reachable (HasSelector=true) and also
// true when no selector is discoverable (matches the fragment-side
// nil-selector default).
func OutputHasMetricNameNode(analysis *Analysis, node logicalpkg.Node) bool {
	info := analysis.InfoFor(node)
	if info == nil || !info.Shape.HasSelector {
		return true
	}
	return info.Shape.OutputHasMetricName
}

// RangeFunctionChildIsLeafSelector reports whether the RangeFunctionPlan's
// child is a LeafExprPlan wrapping a selector expression (the "leaf
// selector" structural predicate tier-2 range helpers pattern-match on
// via fragment.Kind == FragmentKindLeafSource). It peers through the
// logical tree via a Go type switch; a later port can drop the
// corresponding fragment-kind checks in favor of this helper.
func RangeFunctionChildIsLeafSelector(child logicalpkg.Node) bool {
	leaf, ok := child.(*logicalpkg.LeafExprPlan)
	if !ok || leaf == nil {
		return false
	}
	switch leaf.Expr.(type) {
	case *parser.MatrixSelector, *parser.VectorSelector:
		return true
	default:
		return false
	}
}

// SubqueryChildIsInstantVectorLowering reports whether the
// SubqueryPlan's child is a logical node expected to render to an
// instant-vector-lowering subtree. It is a structural test on the
// logical tree: every subquery lowering currently requires an
// instant-vector child whose Shape has a discoverable selector, which
// covers bare selectors, aggregations, binary expressions, and
// function subtrees over them. Callers that need the selector shape
// for the inner child should use InfoFor(child).Shape directly.
func SubqueryChildIsInstantVectorLowering(analysis *Analysis, child logicalpkg.Node) bool {
	info := analysis.InfoFor(child)
	if info == nil {
		return false
	}
	return info.OutputKind == OutputKindInstantVector && info.Shape.HasSelector
}

func outputKindForValueType(valueType parser.ValueType) OutputKind {
	switch valueType {
	case parser.ValueTypeScalar:
		return OutputKindScalar
	case parser.ValueTypeVector:
		return OutputKindInstantVector
	case parser.ValueTypeMatrix:
		return OutputKindRangeMatrix
	default:
		return OutputKindUnknown
	}
}
