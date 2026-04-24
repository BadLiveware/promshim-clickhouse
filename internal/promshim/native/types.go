package native

import (
	"time"

	logicalpkg "ch-observability/internal/promshim/logical"
	"github.com/prometheus/prometheus/model/labels"
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

type FragmentKind string

const (
	FragmentKindLeafSource             FragmentKind = "leaf_source"
	FragmentKindUnarySourceExpr        FragmentKind = "unary_source_expression"
	FragmentKindBinaryScalarSourceExpr FragmentKind = "binary_scalar_source_expression"
	FragmentKindBinaryVectorJoin       FragmentKind = "binary_vector_join"
	FragmentKindRangeFunction          FragmentKind = "range_function"
	FragmentKindSubquery               FragmentKind = "subquery"
	FragmentKindAggregation            FragmentKind = "aggregation"
	FragmentKindSyntheticSeries        FragmentKind = "synthetic_series"
	FragmentKindScalarConvert          FragmentKind = "scalar_convert"
	FragmentKindInfoJoin               FragmentKind = "info_join"
	FragmentKindAbsent                 FragmentKind = "absent"
	FragmentKindHistogramProjection    FragmentKind = "histogram_projection"
	FragmentKindHistogramFunction      FragmentKind = "histogram_function"
	FragmentKindSortTransform          FragmentKind = "sort_transform"
	FragmentKindLabelTransform         FragmentKind = "label_transform"
	FragmentKindClampTransform         FragmentKind = "clamp_transform"
	FragmentKindValueTransform         FragmentKind = "value_transform"
)

type NativeFragment struct {
	Kind                FragmentKind
	OutputKind          OutputKind
	SourcePromQL        parser.Expr
	Selector            *SelectorSource
	ValueExpr           string
	TagsExpr            string
	DropsMetric         bool
	BinaryJoin          *BinaryJoinFragment
	RangeFunction       *RangeFunctionFragment
	Subquery            *SubqueryFragment
	Aggregation         *AggregationFragment
	Synthetic           *SyntheticSeriesFragment
	ScalarConvert       *ScalarConvertFragment
	InfoJoin            *InfoJoinFragment
	Absent              *AbsentFragment
	HistogramProjection *HistogramProjectionFragment
	HistogramFunction   *HistogramFunctionFragment
	SortTransform       *SortTransformFragment
	LabelTransform      *LabelTransformFragment
	ClampTransform      *ClampTransformFragment
	ValueTransform      *ValueTransformFragment
}

const (
	JoinShapeOneToOne   = "one_to_one"
	JoinShapeManyToOne  = "many_to_one"
	JoinShapeOneToMany  = "one_to_many"
	JoinShapeManyToMany = "many_to_many"
)

type BinaryJoinFragment struct {
	Op             parser.ItemType
	ReturnBool     bool
	VectorMatching *parser.VectorMatching
	JoinShape      string
	LHS            *NativeFragment
	RHS            *NativeFragment
}

type RangeFunctionFragment struct {
	Func         string
	ParamNumber  *float64
	ParamNumbers []*float64
	Child        *NativeFragment
}

type SubqueryFragment struct {
	Range      time.Duration
	Step       time.Duration
	Offset     time.Duration
	Timestamp  *int64
	StartOrEnd parser.ItemType
	Child      *NativeFragment
}

type AggregationFragment struct {
	Op              parser.ItemType
	Grouping        []string
	Without         bool
	ParamNumber     *float64
	ParamString     string
	Source          *NativeFragment
	EmitZeroOnEmpty bool
}

type SyntheticSeriesFragment struct {
	Func  string
	Value *float64
}

type ScalarConvertFragment struct {
	Child *NativeFragment
}

type InfoJoinFragment struct {
	Child            *NativeFragment
	InfoMetricName   string
	SelectorMatchers []*labels.Matcher
	CopyLabelNames   []string
	DropUnmatched    bool
}

type AbsentFragment struct {
	Func         string
	OutputMetric map[string]string
	Child        *NativeFragment
}

type HistogramProjectionFragment struct {
	Func  string
	Child *NativeFragment
}

type HistogramFunctionFragment struct {
	Func      string
	Label     string
	Quantile  *float64
	Quantiles []*NativeFragment
	Lower     *float64
	Upper     *float64
	Child     *NativeFragment
}

type SortTransformFragment struct {
	Func   string
	Labels []string
	Child  *NativeFragment
}

type LabelTransformFragment struct {
	Func             string
	Dst              string
	Repl             string
	Regex            string
	RegexSubexpNames []string
	Src              string
	Separator        string
	SrcLabels        []string
	Child            *NativeFragment
}

type ClampTransformFragment struct {
	Func  string
	Child *NativeFragment
	Min   *NativeFragment
	Max   *NativeFragment
}

type ValueTransformFragment struct {
	Child            *NativeFragment
	ValueExpr        string
	FilterExpr       string
	DropsMetric      bool
	RuntimeTransform *RuntimeValueTransform
}

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
	Source   *NativeFragment
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

	Fragment    *NativeFragment
	Aggregation *AggregationSupport

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
	// Fragment. Populated by a single post-walk pass in Analyze so tier-3
	// construction dispatchers can gate on shape without dereferencing
	// NativeFragment. Reflects the pre-optimize Kind (OptimizeFragment
	// operates on a clone, so info.Fragment.Kind is never mutated).
	// Empty ("") when Fragment is nil.
	SubtreeShape FragmentKind

	// LeafSelector mirrors fragment.Selector on the base leaf (Vector/
	// Matrix) selector reached from this node. Populated during the
	// Analyze walk for LeafExprPlan nodes so Lower can read the cached
	// selector without dereferencing info.Fragment.
	//
	// Holds the same *SelectorSource pointer stored in info.Fragment.
	// Selector, so in-place mutations by upstream passes — specifically
	// narrowHistogramChildAnalysisInPlace, which flips RequireFullTags /
	// RequiredTagLabels on the cached selector — flow through both fields
	// identically. OptimizeFragment operates on a clone and does not
	// mutate this pointer. Nil when the node is not a leaf selector or
	// when selector-source analysis failed.
	LeafSelector *SelectorSource
}

// SelectorShape carries per-node selector/shape metadata that tier-2
// renderer helpers currently read from NativeFragment sub-structs
// (fragment.Selector.Kind / Lookback / Offset / Timestamp / StartOrEnd,
// fragment.Selector.RequireFullTags / RequiredTagLabels, and the
// HasFixedTemporalAnchor tree walk). It is populated during the
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

	// OutputHasMetricName mirrors renderer.selectorOutputHasMetricName
	// for the base selector reached from this node: true when the
	// selector's output retains __name__ (RequireFullTags=true or
	// RequiredTagLabels contains __name__). False for narrowed
	// selectors that explicitly drop __name__. Only meaningful when
	// HasSelector is true.
	OutputHasMetricName bool
}

type Analysis struct {
	Root   *LoweringInfo
	byNode map[logicalpkg.Node]*LoweringInfo
}

func Analyze(plan logicalpkg.Node) *Analysis {
	analysis := &Analysis{byNode: map[logicalpkg.Node]*LoweringInfo{}}
	analysis.Root = analysis.walk(plan)
	for _, info := range analysis.byNode {
		if info != nil && info.Fragment != nil {
			info.SubtreeShape = info.Fragment.Kind
		}
	}
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
	if info.Fragment != nil {
		explain.FragmentKind = string(info.Fragment.Kind)
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

// OutputHasMetricNameNode mirrors the renderer's
// selectorOutputHasMetricName helper on the logical tree: it returns
// true when the base selector reachable from node still emits
// __name__ (RequireFullTags=true or RequiredTagLabels contains
// __name__). Returns true (the fragment-side default) when no selector
// is discoverable from this node, matching renderer behavior when the
// selector pointer is nil.
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

func HasFixedTemporalAnchor(fragment *NativeFragment) bool {
	if fragment == nil {
		return false
	}
	if fragment.Selector != nil && (fragment.Selector.Timestamp != nil || fragment.Selector.StartOrEnd == parser.START || fragment.Selector.StartOrEnd == parser.END) {
		return true
	}
	if fragment.Subquery != nil {
		if fragment.Subquery.Timestamp != nil || fragment.Subquery.StartOrEnd == parser.START || fragment.Subquery.StartOrEnd == parser.END {
			return true
		}
		if HasFixedTemporalAnchor(fragment.Subquery.Child) {
			return true
		}
	}
	if fragment.Aggregation != nil && HasFixedTemporalAnchor(fragment.Aggregation.Source) {
		return true
	}
	if fragment.RangeFunction != nil && HasFixedTemporalAnchor(fragment.RangeFunction.Child) {
		return true
	}
	if fragment.BinaryJoin != nil && (HasFixedTemporalAnchor(fragment.BinaryJoin.LHS) || HasFixedTemporalAnchor(fragment.BinaryJoin.RHS)) {
		return true
	}
	if fragment.ScalarConvert != nil && HasFixedTemporalAnchor(fragment.ScalarConvert.Child) {
		return true
	}
	if fragment.InfoJoin != nil && HasFixedTemporalAnchor(fragment.InfoJoin.Child) {
		return true
	}
	if fragment.Absent != nil && HasFixedTemporalAnchor(fragment.Absent.Child) {
		return true
	}
	if fragment.HistogramProjection != nil && HasFixedTemporalAnchor(fragment.HistogramProjection.Child) {
		return true
	}
	if fragment.HistogramFunction != nil && HasFixedTemporalAnchor(fragment.HistogramFunction.Child) {
		return true
	}
	if fragment.SortTransform != nil && HasFixedTemporalAnchor(fragment.SortTransform.Child) {
		return true
	}
	if fragment.LabelTransform != nil && HasFixedTemporalAnchor(fragment.LabelTransform.Child) {
		return true
	}
	if fragment.ClampTransform != nil && (HasFixedTemporalAnchor(fragment.ClampTransform.Child) || HasFixedTemporalAnchor(fragment.ClampTransform.Min) || HasFixedTemporalAnchor(fragment.ClampTransform.Max)) {
		return true
	}
	if fragment.ValueTransform != nil && HasFixedTemporalAnchor(fragment.ValueTransform.Child) {
		return true
	}
	return false
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
