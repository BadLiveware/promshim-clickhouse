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
	// SourceView carries the four source fields
	// nativeAggregationSourceFromLowering consumes: SourcePromQL,
	// ValueExpr, TagsExpr, DropsMetric. Nil when the aggregation has
	// no native source (Eligible=false or the source carries no
	// SourcePromQL — the reader's gate).
	SourceView *AggregationSourceView
	// EmitZeroOnEmpty is set for the `sum(... or vector(0))` zero-fill
	// shape so renderAggregationLogicalBody can branch on it.
	EmitZeroOnEmpty bool
}

// AggregationSourceView carries the four source fields the tier-3
// aggregation-pushdown reader (nativeAggregationSourceFromLowering)
// consumes off info.Aggregation.SourceInfo: SourcePromQL, ValueExpr,
// TagsExpr, DropsMetric. Populated during the Analyze walk when the
// aggregation has a native-lowerable source whose SourcePromQL is
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

	// AbsentFunc is "absent" or "absent_over_time". Populated on
	// AbsentPlan / AbsentOverTimePlan nodes so semanticBarriersFromLogical
	// can distinguish the two.
	AbsentFunc string

	// HistogramFunc is "histogram_quantile", "histogram_fraction", or
	// "histogram_quantiles". Populated alongside
	// SubtreeShapeHistogramFunction so tier-3 dispatchers can gate on
	// the specific histogram variant.
	HistogramFunc string

	LabelLineage     LabelLineage
	TimeRequirements TimeRequirements
	Children         []*LoweringInfo

	// Shape carries selector-derived structural information consumed by
	// tier-2 helpers. Populated during Analyze. See SelectorShape for
	// field semantics.
	Shape SelectorShape

	// JoinShape describes the join cardinality for nodes that lower
	// through the BinaryVectorJoin path. Non-empty only when the node
	// corresponds to a supported vector-vector join
	// (isSupportedNativeVectorJoinOp true + SupportedNativeVectorJoinShape
	// returned (shape, true)). Consumed by explain().
	JoinShape string

	// JoinLabels mirrors VectorMatching.MatchingLabels on the binary
	// join for which JoinShape is set. Populated only when JoinShape is
	// non-empty.
	JoinLabels []string

	// RuntimeValueTransform carries a post-SQL runtime correction for
	// nodes whose ValueTransform view needs one (currently: PromQL
	// modulo correction on scalar-involving modulo expressions).
	// Consumed by nativeSubtreePlan.execute() to apply the correction to
	// decoded samples.
	RuntimeValueTransform *RuntimeValueTransform

	// SubtreeShape classifies this node's lowered SQL shape for tier-3
	// consumers. Populated by a single post-walk pass in Analyze. Empty
	// ("") when the node is not lowerable.
	SubtreeShape SubtreeShape

	// LeafSelector is the base leaf (Vector/Matrix) selector reached
	// from this node. Populated during the Analyze walk for LeafExprPlan
	// nodes so Lower can read the cached selector.
	//
	// native.SelectorSource carries no narrowing state; tag-narrowing
	// flows purely through RenderParams at render time. The LeafSelector
	// pointer is stable across a query's Analyze pass and not cloned
	// for rendering. Nil when the node is not a leaf selector or when
	// selector-source analysis failed.
	LeafSelector *SelectorSource

	// SourceExpr captures the "source expression" view the renderer
	// reads to emit SQL for nodes that lower through the
	// source-expression shapes (LeafSource, UnarySourceExpr,
	// BinaryScalarSourceExpr). Populated during the Analyze walk for
	// each lowerable node of these shapes. Nil otherwise. See
	// SourceExprView.
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
	// pi()` via syntheticLiteralValue) as well as synthetic-builtin
	// series (time(), pi(), date functions). Lower dispatches off this
	// view to render without re-running the fold. Populated alongside
	// SubtreeShapeSyntheticSeries. Nil otherwise. See SyntheticSeriesView.
	SyntheticSeries *SyntheticSeriesView

	// RangeFunctionSubquery is populated during the Analyze walk on each
	// of the seven range-function plan kinds (RangeFunctionPlan, RatePlan,
	// IncreasePlan, DeltaPlan, ChangesPlan, DerivPlan, QuantileOverTimePlan)
	// whenever that range-function's child is a subquery. Holds the
	// pre-computed subquery descriptor so the renderer can drive the
	// subquery-fast-path branches. Nil otherwise.
	RangeFunctionSubquery *SubqueryFragment
}

// SourceExprView is the analysis-side view for nodes that lower
// through the source-expression shapes (LeafSource, UnarySourceExpr,
// BinaryScalarSourceExpr). Selector carries no narrowing state —
// RenderParams is the single source of truth for narrowing.
type SourceExprView struct {
	Selector *SelectorSource
	// ValueExpr is the per-sample value template. For LeafSource this is
	// "{value}"; for UnarySourceExpr it is the composed pointwise
	// template (e.g. "abs({value})"); for BinaryScalarSourceExpr it
	// carries the scalar-involving operator template.
	ValueExpr string
	// TagsExpr is the per-sample tags template.
	TagsExpr string
	// SourcePromQL is non-nil only for ResolveSourcePromQL-driven
	// fallback paths that do not have a native Selector. Rare in practice.
	SourcePromQL parser.Expr
	// DropsMetric governs whether the outer SELECT strips __name__ from
	// the tags column.
	DropsMetric bool
}

// ValueTransformView is the analysis-side view for BinaryPlan nodes
// whose scalar-involving shape lowers via renderValueTransformFromSource.
//
// VectorChildOnLeft identifies which side of the enclosing BinaryPlan
// carries the non-scalar child whose SQL the ValueTransform wraps. When
// true the vector child is n.LHS (n.RHS is the scalar side); when false
// the vector child is n.RHS (n.LHS is the scalar side). Lower uses this
// flag to pick which child to recurse through for the wrapper's inner
// SELECT.
type ValueTransformView struct {
	VectorChildOnLeft bool
	// ValueExpr is the inner value template wrapped around the child's
	// "value" column.
	ValueExpr string
	// FilterExpr is the filter template for comparison-filter arms
	// (empty otherwise).
	FilterExpr string
	// DropsMetric governs whether the outer SELECT strips __name__
	// from the tags column.
	DropsMetric bool
}

// SyntheticSeriesView captures the analysis-side view of a synthetic
// series: a folded scalar literal (Func=="literal") or a synthetic
// builtin (time, pi, minute, hour, ...). Lower reads Func + Value to
// render the right variant directly.
type SyntheticSeriesView struct {
	// Func is "literal" for folded scalar literals (ScalarLiteralPlan /
	// folded UnaryPlan / folded BinaryPlan), or the builtin name
	// ("time", "pi", "minute", "hour", "day_of_week", "day_of_month",
	// "day_of_year", "days_in_month", "month", "year") for synthetic
	// scalar / date functions.
	Func string
	// Value is the folded scalar result (e.g. 3 for `1+2`). Only
	// meaningful when Func=="literal".
	Value float64
}

// SelectorShape carries per-node selector/shape metadata consumed by
// tier-2 renderer helpers: SelectorKind / Lookback / Offset /
// Timestamp / StartOrEnd and the HasFixedTemporalAnchor tree walk. It
// is populated during the native.Analyze walk and available via
// Analysis.InfoFor(node).Shape.
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

	// SelectorKind is InstantVector for plain vector selectors and
	// RangeVector for matrix selectors reached from this node.
	SelectorKind SelectorKind

	// SelectorLookback is the base selector's lookback. For
	// instant-vector selectors this is DefaultInstantSelectorLookback
	// (the staleness window). For range-vector selectors this is the
	// matrix range.
	SelectorLookback time.Duration

	// SelectorOffset is VectorSelector.OriginalOffset on the base
	// selector.
	SelectorOffset time.Duration

	// SelectorTimestamp is set when the selector uses an @ anchor; nil
	// otherwise.
	SelectorTimestamp *int64

	// SelectorStartOrEnd is parser.START / parser.END for start()/end()
	// anchors, zero otherwise.
	SelectorStartOrEnd parser.ItemType

	// HasFixedTemporalAnchor is true when any node in the subtree
	// rooted at this node pins evaluation to an @ timestamp or
	// start()/end(). Computed transitively during Analyze.
	HasFixedTemporalAnchor bool

	// OutputHasMetricName is structurally true whenever HasSelector is
	// true. native.SelectorSource carries no narrowing state — a base
	// selector always emits __name__ as part of its tags; render-time
	// RenderParams narrowing strips it later. The field is retained so
	// existing callers keep their signature.
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
	SubtreeShape                string               `json:"subtreeShape,omitempty"`
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
		explain.SubtreeShape = string(info.SubtreeShape)
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
// from node emits __name__. Narrowing flows purely through RenderParams
// at render time, so this is structurally true whenever a base
// selector is reachable (HasSelector=true) and also true when no
// selector is discoverable (the conservative default).
func OutputHasMetricNameNode(analysis *Analysis, node logicalpkg.Node) bool {
	info := analysis.InfoFor(node)
	if info == nil || !info.Shape.HasSelector {
		return true
	}
	return info.Shape.OutputHasMetricName
}

// RangeFunctionChildIsLeafSelector reports whether the
// RangeFunctionPlan's child is a LeafExprPlan wrapping a selector
// expression (a Vector or Matrix selector). Peers through the logical
// tree via a Go type switch.
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
