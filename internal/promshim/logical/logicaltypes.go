package logical

import (
	"time"

	modelpkg "github.com/BadLiveware/promshim-clickhouse/internal/promshim/model"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

type Node interface {
	logicalNode()
	valueType() parser.ValueType
	exprString() string
}

type LeafExprPlan struct{ Expr parser.Expr }

func (*LeafExprPlan) logicalNode()                  {}
func (p *LeafExprPlan) valueType() parser.ValueType { return p.Expr.Type() }
func (p *LeafExprPlan) ValueType() parser.ValueType { return p.valueType() }
func (p *LeafExprPlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}
func (p *LeafExprPlan) ExprString() string { return p.exprString() }

type ScalarLiteralPlan struct {
	Expr  parser.Expr
	Value float64
}

func (*ScalarLiteralPlan) logicalNode()                  {}
func (p *ScalarLiteralPlan) valueType() parser.ValueType { return parser.ValueTypeScalar }
func (p *ScalarLiteralPlan) ValueType() parser.ValueType { return p.valueType() }
func (p *ScalarLiteralPlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}
func (p *ScalarLiteralPlan) ExprString() string { return p.exprString() }

type UnaryPlan struct {
	Expr  parser.Expr
	Op    parser.ItemType
	Child Node
}

func (*UnaryPlan) logicalNode() {}
func (p *UnaryPlan) valueType() parser.ValueType {
	if p.Expr == nil {
		return parser.ValueTypeNone
	}
	return p.Expr.Type()
}
func (p *UnaryPlan) ValueType() parser.ValueType { return p.valueType() }
func (p *UnaryPlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}
func (p *UnaryPlan) ExprString() string { return p.exprString() }

type BinaryPlan struct {
	Expr           parser.Expr
	Op             parser.ItemType
	VectorMatching *parser.VectorMatching
	ReturnBool     bool
	LHS, RHS       Node
}

func (*BinaryPlan) logicalNode() {}
func (p *BinaryPlan) valueType() parser.ValueType {
	if p.Expr == nil {
		return parser.ValueTypeNone
	}
	return p.Expr.Type()
}
func (p *BinaryPlan) ValueType() parser.ValueType { return p.valueType() }
func (p *BinaryPlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}
func (p *BinaryPlan) ExprString() string { return p.exprString() }

type AggregationPlan struct {
	Expr        parser.Expr
	Op          parser.ItemType
	Grouping    []string
	Without     bool
	ParamNumber *float64
	ParamString string
	Child       Node
}

func (*AggregationPlan) logicalNode() {}
func (p *AggregationPlan) valueType() parser.ValueType {
	if p.Expr == nil {
		return parser.ValueTypeNone
	}
	return p.Expr.Type()
}
func (p *AggregationPlan) ValueType() parser.ValueType { return p.valueType() }
func (p *AggregationPlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}
func (p *AggregationPlan) ExprString() string { return p.exprString() }

type HistogramQuantilePlan struct {
	Expr     parser.Expr
	Quantile float64
	Child    Node
}

func (*HistogramQuantilePlan) logicalNode() {}
func (p *HistogramQuantilePlan) valueType() parser.ValueType {
	if p.Expr == nil {
		return parser.ValueTypeNone
	}
	return p.Expr.Type()
}
func (p *HistogramQuantilePlan) ValueType() parser.ValueType { return p.valueType() }
func (p *HistogramQuantilePlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}
func (p *HistogramQuantilePlan) ExprString() string { return p.exprString() }

type HistogramFractionPlan struct {
	Expr  parser.Expr
	Lower float64
	Upper float64
	Child Node
}

func (*HistogramFractionPlan) logicalNode() {}
func (p *HistogramFractionPlan) valueType() parser.ValueType {
	if p.Expr == nil {
		return parser.ValueTypeNone
	}
	return p.Expr.Type()
}
func (p *HistogramFractionPlan) ValueType() parser.ValueType { return p.valueType() }
func (p *HistogramFractionPlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}
func (p *HistogramFractionPlan) ExprString() string { return p.exprString() }

type HistogramProjectionPlan struct {
	Expr  parser.Expr
	Func  string
	Child Node
}

type HistogramQuantilesPlan struct {
	Expr          parser.Expr
	Label         string
	ParamNumbers  []*float64
	ParamChildren []Node
	Child         Node
}

func (*HistogramProjectionPlan) logicalNode() {}
func (p *HistogramProjectionPlan) valueType() parser.ValueType {
	if p.Expr == nil {
		return parser.ValueTypeNone
	}
	return p.Expr.Type()
}
func (p *HistogramProjectionPlan) ValueType() parser.ValueType { return p.valueType() }
func (p *HistogramProjectionPlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}
func (p *HistogramProjectionPlan) ExprString() string { return p.exprString() }

func (*HistogramQuantilesPlan) logicalNode() {}
func (p *HistogramQuantilesPlan) valueType() parser.ValueType {
	if p.Expr == nil {
		return parser.ValueTypeNone
	}
	return p.Expr.Type()
}
func (p *HistogramQuantilesPlan) ValueType() parser.ValueType { return p.valueType() }
func (p *HistogramQuantilesPlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}
func (p *HistogramQuantilesPlan) ExprString() string { return p.exprString() }

type RangeFunctionPlan struct {
	Expr         parser.Expr
	Func         string
	ParamNumber  *float64
	ParamNumbers []*float64
	Child        Node
}

func (*RangeFunctionPlan) logicalNode() {}
func (p *RangeFunctionPlan) valueType() parser.ValueType {
	if p.Expr == nil {
		return parser.ValueTypeNone
	}
	return p.Expr.Type()
}
func (p *RangeFunctionPlan) ValueType() parser.ValueType { return p.valueType() }
func (p *RangeFunctionPlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}
func (p *RangeFunctionPlan) ExprString() string { return p.exprString() }

type VectorPlan struct {
	Expr  parser.Expr
	Child Node
}

func (*VectorPlan) logicalNode() {}
func (p *VectorPlan) valueType() parser.ValueType {
	if p.Expr == nil {
		return parser.ValueTypeNone
	}
	return p.Expr.Type()
}
func (p *VectorPlan) ValueType() parser.ValueType { return p.valueType() }
func (p *VectorPlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}
func (p *VectorPlan) ExprString() string { return p.exprString() }

type RoundPlan struct {
	Expr     parser.Expr
	Decimals *float64
	Child    Node
}

func (*RoundPlan) logicalNode() {}
func (p *RoundPlan) valueType() parser.ValueType {
	if p.Expr == nil {
		return parser.ValueTypeNone
	}
	return p.Expr.Type()
}
func (p *RoundPlan) ValueType() parser.ValueType { return p.valueType() }
func (p *RoundPlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}
func (p *RoundPlan) ExprString() string { return p.exprString() }

type SortPlan struct {
	Expr   parser.Expr
	Func   string
	Labels []string
	Child  Node
}

func (*SortPlan) logicalNode() {}
func (p *SortPlan) valueType() parser.ValueType {
	if p.Expr == nil {
		return parser.ValueTypeNone
	}
	return p.Expr.Type()
}
func (p *SortPlan) ValueType() parser.ValueType { return p.valueType() }
func (p *SortPlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}
func (p *SortPlan) ExprString() string { return p.exprString() }

type ScalarConvertPlan struct {
	Expr  parser.Expr
	Child Node
}

func (*ScalarConvertPlan) logicalNode() {}
func (p *ScalarConvertPlan) valueType() parser.ValueType {
	if p.Expr == nil {
		return parser.ValueTypeNone
	}
	return p.Expr.Type()
}
func (p *ScalarConvertPlan) ValueType() parser.ValueType { return p.valueType() }
func (p *ScalarConvertPlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}
func (p *ScalarConvertPlan) ExprString() string { return p.exprString() }

type InfoPlan struct {
	Expr             parser.Expr
	SelectorMatchers []*labels.Matcher
	Child            Node
}

func (*InfoPlan) logicalNode() {}
func (p *InfoPlan) valueType() parser.ValueType {
	if p.Expr == nil {
		return parser.ValueTypeNone
	}
	return p.Expr.Type()
}
func (p *InfoPlan) ValueType() parser.ValueType { return p.valueType() }
func (p *InfoPlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}
func (p *InfoPlan) ExprString() string { return p.exprString() }

type PointwiseFunctionPlan struct {
	Expr          parser.Expr
	Func          string
	ParamNumbers  []*float64
	ParamChildren []Node
	Child         Node
}

func (*PointwiseFunctionPlan) logicalNode() {}
func (p *PointwiseFunctionPlan) valueType() parser.ValueType {
	if p.Expr == nil {
		return parser.ValueTypeNone
	}
	return p.Expr.Type()
}
func (p *PointwiseFunctionPlan) ValueType() parser.ValueType { return p.valueType() }
func (p *PointwiseFunctionPlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}
func (p *PointwiseFunctionPlan) ExprString() string { return p.exprString() }

type ScalarBuiltinPlan struct {
	Expr parser.Expr
	Func string
}

func (*ScalarBuiltinPlan) logicalNode() {}
func (p *ScalarBuiltinPlan) valueType() parser.ValueType {
	if p.Expr == nil {
		return parser.ValueTypeNone
	}
	return p.Expr.Type()
}
func (p *ScalarBuiltinPlan) ValueType() parser.ValueType { return p.valueType() }
func (p *ScalarBuiltinPlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}
func (p *ScalarBuiltinPlan) ExprString() string { return p.exprString() }

type RatePlan struct {
	Expr  parser.Expr
	Func  string
	Child Node
}

func (*RatePlan) logicalNode() {}
func (p *RatePlan) valueType() parser.ValueType {
	if p.Expr == nil {
		return parser.ValueTypeNone
	}
	return p.Expr.Type()
}
func (p *RatePlan) ValueType() parser.ValueType { return p.valueType() }
func (p *RatePlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}
func (p *RatePlan) ExprString() string { return p.exprString() }

type IncreasePlan struct {
	Expr  parser.Expr
	Child Node
}

func (*IncreasePlan) logicalNode() {}
func (p *IncreasePlan) valueType() parser.ValueType {
	if p.Expr == nil {
		return parser.ValueTypeNone
	}
	return p.Expr.Type()
}
func (p *IncreasePlan) ValueType() parser.ValueType { return p.valueType() }
func (p *IncreasePlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}
func (p *IncreasePlan) ExprString() string { return p.exprString() }

type DeltaPlan struct {
	Expr  parser.Expr
	Func  string
	Child Node
}

func (*DeltaPlan) logicalNode() {}
func (p *DeltaPlan) valueType() parser.ValueType {
	if p.Expr == nil {
		return parser.ValueTypeNone
	}
	return p.Expr.Type()
}
func (p *DeltaPlan) ValueType() parser.ValueType { return p.valueType() }
func (p *DeltaPlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}
func (p *DeltaPlan) ExprString() string { return p.exprString() }

type ChangesPlan struct {
	Expr  parser.Expr
	Child Node
}

func (*ChangesPlan) logicalNode() {}
func (p *ChangesPlan) valueType() parser.ValueType {
	if p.Expr == nil {
		return parser.ValueTypeNone
	}
	return p.Expr.Type()
}
func (p *ChangesPlan) ValueType() parser.ValueType { return p.valueType() }
func (p *ChangesPlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}
func (p *ChangesPlan) ExprString() string { return p.exprString() }

type DerivPlan struct {
	Expr  parser.Expr
	Child Node
}

func (*DerivPlan) logicalNode() {}
func (p *DerivPlan) valueType() parser.ValueType {
	if p.Expr == nil {
		return parser.ValueTypeNone
	}
	return p.Expr.Type()
}
func (p *DerivPlan) ValueType() parser.ValueType { return p.valueType() }
func (p *DerivPlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}
func (p *DerivPlan) ExprString() string { return p.exprString() }

type QuantileOverTimePlan struct {
	Expr     parser.Expr
	Quantile float64
	Child    Node
}

func (*QuantileOverTimePlan) logicalNode() {}
func (p *QuantileOverTimePlan) valueType() parser.ValueType {
	if p.Expr == nil {
		return parser.ValueTypeNone
	}
	return p.Expr.Type()
}
func (p *QuantileOverTimePlan) ValueType() parser.ValueType { return p.valueType() }
func (p *QuantileOverTimePlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}
func (p *QuantileOverTimePlan) ExprString() string { return p.exprString() }

type AbsentPlan struct {
	Expr         parser.Expr
	OutputMetric map[string]string
	Child        Node
}

func (*AbsentPlan) logicalNode() {}
func (p *AbsentPlan) valueType() parser.ValueType {
	if p.Expr == nil {
		return parser.ValueTypeNone
	}
	return p.Expr.Type()
}
func (p *AbsentPlan) ValueType() parser.ValueType { return p.valueType() }
func (p *AbsentPlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}
func (p *AbsentPlan) ExprString() string { return p.exprString() }

type AbsentOverTimePlan struct {
	Expr         parser.Expr
	OutputMetric map[string]string
	Child        Node
}

func (*AbsentOverTimePlan) logicalNode() {}
func (p *AbsentOverTimePlan) valueType() parser.ValueType {
	if p.Expr == nil {
		return parser.ValueTypeNone
	}
	return p.Expr.Type()
}
func (p *AbsentOverTimePlan) ValueType() parser.ValueType { return p.valueType() }
func (p *AbsentOverTimePlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}
func (p *AbsentOverTimePlan) ExprString() string { return p.exprString() }

type SubqueryPlan struct {
	Expr                    parser.Expr
	Range                   time.Duration
	Step                    time.Duration
	Offset                  time.Duration
	Timestamp               *int64
	StartOrEnd              parser.ItemType
	DelegatedLeafCompatible bool
	Child                   Node
}

func (*SubqueryPlan) logicalNode() {}
func (p *SubqueryPlan) valueType() parser.ValueType {
	if p.Expr == nil {
		return parser.ValueTypeNone
	}
	return p.Expr.Type()
}
func (p *SubqueryPlan) ValueType() parser.ValueType { return p.valueType() }
func (p *SubqueryPlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}
func (p *SubqueryPlan) ExprString() string { return p.exprString() }

type LabelReplacePlan struct {
	Expr   parser.Expr
	Config modelpkg.LabelReplaceConfig
	Child  Node
}

func (*LabelReplacePlan) logicalNode() {}
func (p *LabelReplacePlan) valueType() parser.ValueType {
	if p.Expr == nil {
		return parser.ValueTypeNone
	}
	return p.Expr.Type()
}
func (p *LabelReplacePlan) ValueType() parser.ValueType { return p.valueType() }
func (p *LabelReplacePlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}
func (p *LabelReplacePlan) ExprString() string { return p.exprString() }

type LabelJoinPlan struct {
	Expr   parser.Expr
	Config modelpkg.LabelJoinConfig
	Child  Node
}

func (*LabelJoinPlan) logicalNode() {}
func (p *LabelJoinPlan) valueType() parser.ValueType {
	if p.Expr == nil {
		return parser.ValueTypeNone
	}
	return p.Expr.Type()
}
func (p *LabelJoinPlan) ValueType() parser.ValueType { return p.valueType() }
func (p *LabelJoinPlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}
func (p *LabelJoinPlan) ExprString() string { return p.exprString() }
