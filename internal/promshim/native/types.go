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
