package plan

import (
	modelpkg "ch-observability/internal/promshim/model"
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
	Expr       parser.Expr
	Op         parser.ItemType
	ReturnBool bool
	LHS, RHS   LogicalPlan
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
	Expr     parser.Expr
	Op       parser.ItemType
	Grouping []string
	Without  bool
	Child    LogicalPlan
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
