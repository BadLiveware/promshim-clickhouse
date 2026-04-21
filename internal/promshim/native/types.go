package native

import (
	"time"

	planpkg "github.com/BadLiveware/promshim-ch/internal/promshim/plan"
	"github.com/prometheus/prometheus/promql/parser"
)

type OutputKind string

const (
	OutputKindUnknown       OutputKind = "unknown"
	OutputKindScalar        OutputKind = "scalar"
	OutputKindInstantVector OutputKind = "instant_vector"
	OutputKindRangeMatrix   OutputKind = "range_matrix"
)

type FragmentKind string

const (
	FragmentKindLeafSource             FragmentKind = "leaf_source"
	FragmentKindUnarySourceExpr        FragmentKind = "unary_source_expression"
	FragmentKindBinaryScalarSourceExpr FragmentKind = "binary_scalar_source_expression"
	FragmentKindAggregation            FragmentKind = "aggregation"
)

type NativeFragment struct {
	Kind         FragmentKind
	OutputKind   OutputKind
	SourcePromQL parser.Expr
	Selector     *SelectorSource
	ValueExpr    string
	TagsExpr     string
	DropsMetric  bool
	Aggregation  *AggregationFragment
}

type AggregationFragment struct {
	Op       parser.ItemType
	Grouping []string
	Without  bool
	Source   *NativeFragment
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
	byNode map[planpkg.LogicalPlan]*LoweringInfo
}

func Analyze(plan planpkg.LogicalPlan) *Analysis {
	analysis := &Analysis{byNode: map[planpkg.LogicalPlan]*LoweringInfo{}}
	analysis.Root = analysis.walk(plan)
	return analysis
}

func (a *Analysis) InfoFor(node planpkg.LogicalPlan) *LoweringInfo {
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
