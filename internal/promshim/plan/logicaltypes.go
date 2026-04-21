package plan

import (
	"time"

	modelpkg "ch-observability/internal/promshim/model"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

type LogicalPlan interface {
	logicalPlan()
	valueType() parser.ValueType
	exprString() string
}

type LogicalLeafExprPlan struct{ Expr parser.Expr }

func (*LogicalLeafExprPlan) logicalPlan()                  {}
func (p *LogicalLeafExprPlan) valueType() parser.ValueType { return p.Expr.Type() }
func (p *LogicalLeafExprPlan) ValueType() parser.ValueType { return p.valueType() }
func (p *LogicalLeafExprPlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}
func (p *LogicalLeafExprPlan) ExprString() string { return p.exprString() }

type LogicalScalarLiteralPlan struct {
	Expr  parser.Expr
	Value float64
}

func (*LogicalScalarLiteralPlan) logicalPlan()                  {}
func (p *LogicalScalarLiteralPlan) valueType() parser.ValueType { return parser.ValueTypeScalar }
func (p *LogicalScalarLiteralPlan) ValueType() parser.ValueType { return p.valueType() }
func (p *LogicalScalarLiteralPlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}
func (p *LogicalScalarLiteralPlan) ExprString() string { return p.exprString() }

type LogicalUnaryPlan struct {
	Expr  parser.Expr
	Op    parser.ItemType
	Child LogicalPlan
}

func (*LogicalUnaryPlan) logicalPlan() {}
func (p *LogicalUnaryPlan) valueType() parser.ValueType {
	if p.Expr == nil {
		return parser.ValueTypeNone
	}
	return p.Expr.Type()
}
func (p *LogicalUnaryPlan) ValueType() parser.ValueType { return p.valueType() }
func (p *LogicalUnaryPlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}
func (p *LogicalUnaryPlan) ExprString() string { return p.exprString() }

type LogicalBinaryPlan struct {
	Expr           parser.Expr
	Op             parser.ItemType
	VectorMatching *parser.VectorMatching
	ReturnBool     bool
	LHS, RHS       LogicalPlan
}

func (*LogicalBinaryPlan) logicalPlan() {}
func (p *LogicalBinaryPlan) valueType() parser.ValueType {
	if p.Expr == nil {
		return parser.ValueTypeNone
	}
	return p.Expr.Type()
}
func (p *LogicalBinaryPlan) ValueType() parser.ValueType { return p.valueType() }
func (p *LogicalBinaryPlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}
func (p *LogicalBinaryPlan) ExprString() string { return p.exprString() }

type LogicalAggregationPlan struct {
	Expr        parser.Expr
	Op          parser.ItemType
	Grouping    []string
	Without     bool
	ParamNumber *float64
	ParamString string
	Child       LogicalPlan
}

func (*LogicalAggregationPlan) logicalPlan() {}
func (p *LogicalAggregationPlan) valueType() parser.ValueType {
	if p.Expr == nil {
		return parser.ValueTypeNone
	}
	return p.Expr.Type()
}
func (p *LogicalAggregationPlan) ValueType() parser.ValueType { return p.valueType() }
func (p *LogicalAggregationPlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}
func (p *LogicalAggregationPlan) ExprString() string { return p.exprString() }

type LogicalHistogramQuantilePlan struct {
	Expr     parser.Expr
	Quantile float64
	Child    LogicalPlan
}

func (*LogicalHistogramQuantilePlan) logicalPlan() {}
func (p *LogicalHistogramQuantilePlan) valueType() parser.ValueType {
	if p.Expr == nil {
		return parser.ValueTypeNone
	}
	return p.Expr.Type()
}
func (p *LogicalHistogramQuantilePlan) ValueType() parser.ValueType { return p.valueType() }
func (p *LogicalHistogramQuantilePlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}
func (p *LogicalHistogramQuantilePlan) ExprString() string { return p.exprString() }

type LogicalHistogramFractionPlan struct {
	Expr  parser.Expr
	Lower float64
	Upper float64
	Child LogicalPlan
}

func (*LogicalHistogramFractionPlan) logicalPlan() {}
func (p *LogicalHistogramFractionPlan) valueType() parser.ValueType {
	if p.Expr == nil {
		return parser.ValueTypeNone
	}
	return p.Expr.Type()
}
func (p *LogicalHistogramFractionPlan) ValueType() parser.ValueType { return p.valueType() }
func (p *LogicalHistogramFractionPlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}
func (p *LogicalHistogramFractionPlan) ExprString() string { return p.exprString() }

type LogicalHistogramProjectionPlan struct {
	Expr  parser.Expr
	Func  string
	Child LogicalPlan
}

func (*LogicalHistogramProjectionPlan) logicalPlan() {}
func (p *LogicalHistogramProjectionPlan) valueType() parser.ValueType {
	if p.Expr == nil {
		return parser.ValueTypeNone
	}
	return p.Expr.Type()
}
func (p *LogicalHistogramProjectionPlan) ValueType() parser.ValueType { return p.valueType() }
func (p *LogicalHistogramProjectionPlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}
func (p *LogicalHistogramProjectionPlan) ExprString() string { return p.exprString() }

type LogicalRangeFunctionPlan struct {
	Expr         parser.Expr
	Func         string
	ParamNumber  *float64
	ParamNumbers []*float64
	Child        LogicalPlan
}

func (*LogicalRangeFunctionPlan) logicalPlan() {}
func (p *LogicalRangeFunctionPlan) valueType() parser.ValueType {
	if p.Expr == nil {
		return parser.ValueTypeNone
	}
	return p.Expr.Type()
}
func (p *LogicalRangeFunctionPlan) ValueType() parser.ValueType { return p.valueType() }
func (p *LogicalRangeFunctionPlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}
func (p *LogicalRangeFunctionPlan) ExprString() string { return p.exprString() }

type LogicalVectorPlan struct {
	Expr  parser.Expr
	Child LogicalPlan
}

func (*LogicalVectorPlan) logicalPlan() {}
func (p *LogicalVectorPlan) valueType() parser.ValueType {
	if p.Expr == nil {
		return parser.ValueTypeNone
	}
	return p.Expr.Type()
}
func (p *LogicalVectorPlan) ValueType() parser.ValueType { return p.valueType() }
func (p *LogicalVectorPlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}
func (p *LogicalVectorPlan) ExprString() string { return p.exprString() }

type LogicalRoundPlan struct {
	Expr     parser.Expr
	Decimals *float64
	Child    LogicalPlan
}

func (*LogicalRoundPlan) logicalPlan() {}
func (p *LogicalRoundPlan) valueType() parser.ValueType {
	if p.Expr == nil {
		return parser.ValueTypeNone
	}
	return p.Expr.Type()
}
func (p *LogicalRoundPlan) ValueType() parser.ValueType { return p.valueType() }
func (p *LogicalRoundPlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}
func (p *LogicalRoundPlan) ExprString() string { return p.exprString() }

type LogicalSortPlan struct {
	Expr   parser.Expr
	Func   string
	Labels []string
	Child  LogicalPlan
}

func (*LogicalSortPlan) logicalPlan() {}
func (p *LogicalSortPlan) valueType() parser.ValueType {
	if p.Expr == nil {
		return parser.ValueTypeNone
	}
	return p.Expr.Type()
}
func (p *LogicalSortPlan) ValueType() parser.ValueType { return p.valueType() }
func (p *LogicalSortPlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}
func (p *LogicalSortPlan) ExprString() string { return p.exprString() }

type LogicalScalarConvertPlan struct {
	Expr  parser.Expr
	Child LogicalPlan
}

func (*LogicalScalarConvertPlan) logicalPlan() {}
func (p *LogicalScalarConvertPlan) valueType() parser.ValueType {
	if p.Expr == nil {
		return parser.ValueTypeNone
	}
	return p.Expr.Type()
}
func (p *LogicalScalarConvertPlan) ValueType() parser.ValueType { return p.valueType() }
func (p *LogicalScalarConvertPlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}
func (p *LogicalScalarConvertPlan) ExprString() string { return p.exprString() }

type LogicalInfoPlan struct {
	Expr             parser.Expr
	SelectorMatchers []*labels.Matcher
	Child            LogicalPlan
}

func (*LogicalInfoPlan) logicalPlan() {}
func (p *LogicalInfoPlan) valueType() parser.ValueType {
	if p.Expr == nil {
		return parser.ValueTypeNone
	}
	return p.Expr.Type()
}
func (p *LogicalInfoPlan) ValueType() parser.ValueType { return p.valueType() }
func (p *LogicalInfoPlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}
func (p *LogicalInfoPlan) ExprString() string { return p.exprString() }

type LogicalPointwiseFunctionPlan struct {
	Expr         parser.Expr
	Func         string
	ParamNumbers []*float64
	Child        LogicalPlan
}

func (*LogicalPointwiseFunctionPlan) logicalPlan() {}
func (p *LogicalPointwiseFunctionPlan) valueType() parser.ValueType {
	if p.Expr == nil {
		return parser.ValueTypeNone
	}
	return p.Expr.Type()
}
func (p *LogicalPointwiseFunctionPlan) ValueType() parser.ValueType { return p.valueType() }
func (p *LogicalPointwiseFunctionPlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}
func (p *LogicalPointwiseFunctionPlan) ExprString() string { return p.exprString() }

type LogicalScalarBuiltinPlan struct {
	Expr parser.Expr
	Func string
}

func (*LogicalScalarBuiltinPlan) logicalPlan() {}
func (p *LogicalScalarBuiltinPlan) valueType() parser.ValueType {
	if p.Expr == nil {
		return parser.ValueTypeNone
	}
	return p.Expr.Type()
}
func (p *LogicalScalarBuiltinPlan) ValueType() parser.ValueType { return p.valueType() }
func (p *LogicalScalarBuiltinPlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}
func (p *LogicalScalarBuiltinPlan) ExprString() string { return p.exprString() }

type LogicalRatePlan struct {
	Expr  parser.Expr
	Func  string
	Child LogicalPlan
}

func (*LogicalRatePlan) logicalPlan() {}
func (p *LogicalRatePlan) valueType() parser.ValueType {
	if p.Expr == nil {
		return parser.ValueTypeNone
	}
	return p.Expr.Type()
}
func (p *LogicalRatePlan) ValueType() parser.ValueType { return p.valueType() }
func (p *LogicalRatePlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}
func (p *LogicalRatePlan) ExprString() string { return p.exprString() }

type LogicalIncreasePlan struct {
	Expr  parser.Expr
	Child LogicalPlan
}

func (*LogicalIncreasePlan) logicalPlan() {}
func (p *LogicalIncreasePlan) valueType() parser.ValueType {
	if p.Expr == nil {
		return parser.ValueTypeNone
	}
	return p.Expr.Type()
}
func (p *LogicalIncreasePlan) ValueType() parser.ValueType { return p.valueType() }
func (p *LogicalIncreasePlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}
func (p *LogicalIncreasePlan) ExprString() string { return p.exprString() }

type LogicalDeltaPlan struct {
	Expr  parser.Expr
	Func  string
	Child LogicalPlan
}

func (*LogicalDeltaPlan) logicalPlan() {}
func (p *LogicalDeltaPlan) valueType() parser.ValueType {
	if p.Expr == nil {
		return parser.ValueTypeNone
	}
	return p.Expr.Type()
}
func (p *LogicalDeltaPlan) ValueType() parser.ValueType { return p.valueType() }
func (p *LogicalDeltaPlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}
func (p *LogicalDeltaPlan) ExprString() string { return p.exprString() }

type LogicalChangesPlan struct {
	Expr  parser.Expr
	Child LogicalPlan
}

func (*LogicalChangesPlan) logicalPlan() {}
func (p *LogicalChangesPlan) valueType() parser.ValueType {
	if p.Expr == nil {
		return parser.ValueTypeNone
	}
	return p.Expr.Type()
}
func (p *LogicalChangesPlan) ValueType() parser.ValueType { return p.valueType() }
func (p *LogicalChangesPlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}
func (p *LogicalChangesPlan) ExprString() string { return p.exprString() }

type LogicalDerivPlan struct {
	Expr  parser.Expr
	Child LogicalPlan
}

func (*LogicalDerivPlan) logicalPlan() {}
func (p *LogicalDerivPlan) valueType() parser.ValueType {
	if p.Expr == nil {
		return parser.ValueTypeNone
	}
	return p.Expr.Type()
}
func (p *LogicalDerivPlan) ValueType() parser.ValueType { return p.valueType() }
func (p *LogicalDerivPlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}
func (p *LogicalDerivPlan) ExprString() string { return p.exprString() }

type LogicalQuantileOverTimePlan struct {
	Expr     parser.Expr
	Quantile float64
	Child    LogicalPlan
}

func (*LogicalQuantileOverTimePlan) logicalPlan() {}
func (p *LogicalQuantileOverTimePlan) valueType() parser.ValueType {
	if p.Expr == nil {
		return parser.ValueTypeNone
	}
	return p.Expr.Type()
}
func (p *LogicalQuantileOverTimePlan) ValueType() parser.ValueType { return p.valueType() }
func (p *LogicalQuantileOverTimePlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}
func (p *LogicalQuantileOverTimePlan) ExprString() string { return p.exprString() }

type LogicalAbsentPlan struct {
	Expr         parser.Expr
	OutputMetric map[string]string
	Child        LogicalPlan
}

func (*LogicalAbsentPlan) logicalPlan() {}
func (p *LogicalAbsentPlan) valueType() parser.ValueType {
	if p.Expr == nil {
		return parser.ValueTypeNone
	}
	return p.Expr.Type()
}
func (p *LogicalAbsentPlan) ValueType() parser.ValueType { return p.valueType() }
func (p *LogicalAbsentPlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}
func (p *LogicalAbsentPlan) ExprString() string { return p.exprString() }

type LogicalAbsentOverTimePlan struct {
	Expr         parser.Expr
	OutputMetric map[string]string
	Child        LogicalPlan
}

func (*LogicalAbsentOverTimePlan) logicalPlan() {}
func (p *LogicalAbsentOverTimePlan) valueType() parser.ValueType {
	if p.Expr == nil {
		return parser.ValueTypeNone
	}
	return p.Expr.Type()
}
func (p *LogicalAbsentOverTimePlan) ValueType() parser.ValueType { return p.valueType() }
func (p *LogicalAbsentOverTimePlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}
func (p *LogicalAbsentOverTimePlan) ExprString() string { return p.exprString() }

type LogicalSubqueryPlan struct {
	Expr                    parser.Expr
	Range                   time.Duration
	Step                    time.Duration
	Offset                  time.Duration
	Timestamp               *int64
	StartOrEnd              parser.ItemType
	DelegatedLeafCompatible bool
	Child                   LogicalPlan
}

func (*LogicalSubqueryPlan) logicalPlan() {}
func (p *LogicalSubqueryPlan) valueType() parser.ValueType {
	if p.Expr == nil {
		return parser.ValueTypeNone
	}
	return p.Expr.Type()
}
func (p *LogicalSubqueryPlan) ValueType() parser.ValueType { return p.valueType() }
func (p *LogicalSubqueryPlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}
func (p *LogicalSubqueryPlan) ExprString() string { return p.exprString() }

type LogicalLabelReplacePlan struct {
	Expr   parser.Expr
	Config modelpkg.LabelReplaceConfig
	Child  LogicalPlan
}

func (*LogicalLabelReplacePlan) logicalPlan() {}
func (p *LogicalLabelReplacePlan) valueType() parser.ValueType {
	if p.Expr == nil {
		return parser.ValueTypeNone
	}
	return p.Expr.Type()
}
func (p *LogicalLabelReplacePlan) ValueType() parser.ValueType { return p.valueType() }
func (p *LogicalLabelReplacePlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}
func (p *LogicalLabelReplacePlan) ExprString() string { return p.exprString() }

type LogicalLabelJoinPlan struct {
	Expr   parser.Expr
	Config modelpkg.LabelJoinConfig
	Child  LogicalPlan
}

func (*LogicalLabelJoinPlan) logicalPlan() {}
func (p *LogicalLabelJoinPlan) valueType() parser.ValueType {
	if p.Expr == nil {
		return parser.ValueTypeNone
	}
	return p.Expr.Type()
}
func (p *LogicalLabelJoinPlan) ValueType() parser.ValueType { return p.valueType() }
func (p *LogicalLabelJoinPlan) exprString() string {
	if p.Expr == nil {
		return ""
	}
	return p.Expr.String()
}
func (p *LogicalLabelJoinPlan) ExprString() string { return p.exprString() }
