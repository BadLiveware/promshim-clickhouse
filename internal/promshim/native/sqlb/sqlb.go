package sqlb

import (
	"fmt"
	"sort"
	"strings"
)

// Expr is a ClickHouse SQL expression node.
type Expr interface{ writeExpr(*buildCtx) }

// Source is a SELECT source node.
type Source interface{ writeSource(*buildCtx) }

type Ident string

type QIdent string

type Lit struct {
	V    any
	Type string
}

type RawLit struct{ V string }

// Param emits a stable placeholder name instead of an auto-numbered one.
type Param struct {
	Name string
	Type string
	V    any
}

type Call struct {
	Name string
	Args []Expr
}

type AggCall struct {
	Name  string
	State []Expr
	Args  []Expr
}

type ArrayRed struct {
	Agg  string
	Args []Expr
}

type Binary struct {
	Op   string
	L, R Expr
}

type Unary struct {
	Op string
	X  Expr
}

type Tuple struct{ Elems []Expr }

type Subscr struct {
	Array Expr
	Index Expr
}

type Lambda struct {
	Params []Ident
	Body   Expr
}

type ArrayFold struct {
	Lambda Lambda
	Src    Expr
	Init   Expr
}

type Map struct {
	Lambda Lambda
	Arrays []Expr
}

type MultiIfArm struct {
	When Expr
	Then Expr
}

type MultiIf struct {
	Cases []MultiIfArm
	Else  Expr
}

type CaseArm struct {
	When Expr
	Then Expr
}

type Case struct {
	Cases []CaseArm
	Else  Expr
}

type Cast struct {
	X  Expr
	To string
}

type TupleElem struct {
	X Expr
	K int
}

type ColExpr struct {
	Expr  Expr
	Alias string
}

type CTE struct {
	Name string
	Expr *Select
}

type OrderExpr struct {
	Expr Expr
	Desc bool
}

type Limit struct {
	Count  int
	Offset int
}

type Select struct {
	With    []CTE
	Columns []ColExpr
	From    Source
	Where   Expr
	GroupBy []Expr
	Having  Expr
	OrderBy []OrderExpr
	Limit   *Limit
}

type Table struct {
	DB, Name, Alias string
}

type RawSource struct {
	SQL   string
	Alias string
}

type SubSelect struct {
	S     *Select
	Alias string
}

type Join struct {
	Left  Source
	Right Source
	Kind  string
	On    Expr
}

type ArrayJoin struct {
	Base  Source
	Expr  Expr
	Alias string
	Left  bool
}

type buildCtx struct {
	b           strings.Builder
	params      map[string]string
	autoParamID int
}

func (s *Select) Build() (sql string, params map[string]string, err error) {
	if s == nil {
		return "", nil, fmt.Errorf("sqlb: select is nil")
	}
	if len(s.Columns) == 0 {
		return "", nil, fmt.Errorf("sqlb: select requires at least one column")
	}
	ctx := &buildCtx{params: map[string]string{}}
	s.writeSelect(ctx)
	return ctx.b.String(), ctx.params, nil
}

func BuildExpr(expr Expr) (sql string, params map[string]string, err error) {
	ctx := &buildCtx{params: map[string]string{}}
	writeExpr(ctx, expr)
	return ctx.b.String(), ctx.params, nil
}

func NormalizeSQL(sql string) string {
	return strings.Join(strings.Fields(sql), " ")
}

func (i Ident) writeExpr(ctx *buildCtx)  { ctx.write(string(i)) }
func (i QIdent) writeExpr(ctx *buildCtx) { ctx.write(quoteIdentifierPath(string(i))) }
func (l Lit) writeExpr(ctx *buildCtx)    { ctx.write(ctx.bindAuto(l.Type, l.V)) }
func (l RawLit) writeExpr(ctx *buildCtx) { ctx.write(l.V) }
func (p Param) writeExpr(ctx *buildCtx)  { ctx.write(ctx.bindNamed(p.Name, p.Type, p.V)) }

func (c Call) writeExpr(ctx *buildCtx) {
	ctx.write(c.Name)
	ctx.write("(")
	writeExprList(ctx, c.Args)
	ctx.write(")")
}

func (c AggCall) writeExpr(ctx *buildCtx) {
	ctx.write(c.Name)
	ctx.write("(")
	writeExprList(ctx, c.State)
	ctx.write(")(")
	writeExprList(ctx, c.Args)
	ctx.write(")")
}

func (a ArrayRed) writeExpr(ctx *buildCtx) {
	args := make([]Expr, 0, len(a.Args)+1)
	args = append(args, Lit{V: a.Agg, Type: "String"})
	args = append(args, a.Args...)
	Call{Name: "arrayReduce", Args: args}.writeExpr(ctx)
}

func (b Binary) writeExpr(ctx *buildCtx) {
	ctx.write("(")
	writeExpr(ctx, b.L)
	ctx.write(" ")
	ctx.write(b.Op)
	ctx.write(" ")
	writeExpr(ctx, b.R)
	ctx.write(")")
}

func (u Unary) writeExpr(ctx *buildCtx) {
	ctx.write(u.Op)
	ctx.write("(")
	writeExpr(ctx, u.X)
	ctx.write(")")
}

func (t Tuple) writeExpr(ctx *buildCtx) {
	ctx.write("(")
	writeExprList(ctx, t.Elems)
	ctx.write(")")
}

func (s Subscr) writeExpr(ctx *buildCtx) {
	writeExpr(ctx, s.Array)
	ctx.write("[")
	writeExpr(ctx, s.Index)
	ctx.write("]")
}

func (l Lambda) writeExpr(ctx *buildCtx) {
	if len(l.Params) == 1 {
		ctx.write(string(l.Params[0]))
	} else {
		ctx.write("(")
		for i, param := range l.Params {
			if i > 0 {
				ctx.write(", ")
			}
			ctx.write(string(param))
		}
		ctx.write(")")
	}
	ctx.write(" -> ")
	writeExpr(ctx, l.Body)
}

func (a ArrayFold) writeExpr(ctx *buildCtx) {
	ctx.write("arrayFold(")
	a.Lambda.writeExpr(ctx)
	ctx.write(", ")
	writeExpr(ctx, a.Src)
	ctx.write(", ")
	writeExpr(ctx, a.Init)
	ctx.write(")")
}

func (m Map) writeExpr(ctx *buildCtx) {
	ctx.write("arrayMap(")
	m.Lambda.writeExpr(ctx)
	for _, array := range m.Arrays {
		ctx.write(", ")
		writeExpr(ctx, array)
	}
	ctx.write(")")
}

func (m MultiIf) writeExpr(ctx *buildCtx) {
	ctx.write("multiIf(")
	for i, arm := range m.Cases {
		if i > 0 {
			ctx.write(", ")
		}
		writeExpr(ctx, arm.When)
		ctx.write(", ")
		writeExpr(ctx, arm.Then)
	}
	if len(m.Cases) > 0 {
		ctx.write(", ")
	}
	writeExpr(ctx, m.Else)
	ctx.write(")")
}

func (c Case) writeExpr(ctx *buildCtx) {
	ctx.write("CASE")
	for _, arm := range c.Cases {
		ctx.write(" WHEN ")
		writeExpr(ctx, arm.When)
		ctx.write(" THEN ")
		writeExpr(ctx, arm.Then)
	}
	if c.Else != nil {
		ctx.write(" ELSE ")
		writeExpr(ctx, c.Else)
	}
	ctx.write(" END")
}

func (c Cast) writeExpr(ctx *buildCtx) {
	ctx.write("CAST(")
	writeExpr(ctx, c.X)
	ctx.write(", ")
	writeExpr(ctx, Lit{V: c.To, Type: "String"})
	ctx.write(")")
}

func (t TupleElem) writeExpr(ctx *buildCtx) {
	ctx.write("tupleElement(")
	writeExpr(ctx, t.X)
	ctx.write(", ")
	ctx.write(fmt.Sprintf("%d", t.K))
	ctx.write(")")
}

func (t Table) writeSource(ctx *buildCtx) {
	if t.DB != "" {
		ctx.write(quoteIdentifierPath(t.DB))
		ctx.write(".")
	}
	ctx.write(quoteIdentifierPath(t.Name))
	if t.Alias != "" {
		ctx.write(" AS ")
		ctx.write(t.Alias)
	}
}

func (r RawSource) writeSource(ctx *buildCtx) {
	ctx.write(r.SQL)
	if r.Alias != "" {
		ctx.write(" AS ")
		ctx.write(r.Alias)
	}
}

func (s SubSelect) writeSource(ctx *buildCtx) {
	ctx.write("(")
	s.S.writeSelect(ctx)
	ctx.write(")")
	if s.Alias != "" {
		ctx.write(" AS ")
		ctx.write(s.Alias)
	}
}

func (j Join) writeSource(ctx *buildCtx) {
	j.Left.writeSource(ctx)
	ctx.write(" ")
	if j.Kind != "" {
		ctx.write(j.Kind)
		ctx.write(" ")
	}
	ctx.write("JOIN ")
	j.Right.writeSource(ctx)
	if j.On != nil {
		ctx.write(" ON ")
		writeExpr(ctx, j.On)
	}
}

func (a ArrayJoin) writeSource(ctx *buildCtx) {
	a.Base.writeSource(ctx)
	if a.Left {
		ctx.write(" LEFT")
	}
	ctx.write(" ARRAY JOIN ")
	writeExpr(ctx, a.Expr)
	if a.Alias != "" {
		ctx.write(" AS ")
		ctx.write(a.Alias)
	}
}

func (s *Select) writeSelect(ctx *buildCtx) {
	if len(s.With) > 0 {
		ctx.write("WITH ")
		for i, cte := range s.With {
			if i > 0 {
				ctx.write(", ")
			}
			ctx.write(cte.Name)
			ctx.write(" AS (")
			cte.Expr.writeSelect(ctx)
			ctx.write(")")
		}
		ctx.write(" ")
	}
	ctx.write("SELECT ")
	for i, col := range s.Columns {
		if i > 0 {
			ctx.write(", ")
		}
		writeExpr(ctx, col.Expr)
		if col.Alias != "" {
			ctx.write(" AS ")
			ctx.write(col.Alias)
		}
	}
	if s.From != nil {
		ctx.write(" FROM ")
		s.From.writeSource(ctx)
	}
	if s.Where != nil {
		ctx.write(" WHERE ")
		writeExpr(ctx, s.Where)
	}
	if len(s.GroupBy) > 0 {
		ctx.write(" GROUP BY ")
		writeExprList(ctx, s.GroupBy)
	}
	if s.Having != nil {
		ctx.write(" HAVING ")
		writeExpr(ctx, s.Having)
	}
	if len(s.OrderBy) > 0 {
		ctx.write(" ORDER BY ")
		for i, order := range s.OrderBy {
			if i > 0 {
				ctx.write(", ")
			}
			writeExpr(ctx, order.Expr)
			if order.Desc {
				ctx.write(" DESC")
			}
		}
	}
	if s.Limit != nil {
		ctx.write(" LIMIT ")
		if s.Limit.Offset > 0 {
			ctx.write(fmt.Sprintf("%d, ", s.Limit.Offset))
		}
		ctx.write(fmt.Sprintf("%d", s.Limit.Count))
	}
}

func (ctx *buildCtx) bindAuto(typ string, value any) string {
	name := fmt.Sprintf("p%d", ctx.autoParamID)
	ctx.autoParamID++
	return ctx.bindNamed(name, typ, value)
}

func (ctx *buildCtx) bindNamed(name, typ string, value any) string {
	ctx.params["param_"+name] = formatParamValue(value)
	return fmt.Sprintf("{%s:%s}", name, typ)
}

func (ctx *buildCtx) write(parts ...string) {
	for _, part := range parts {
		ctx.b.WriteString(part)
	}
}

func writeExpr(ctx *buildCtx, expr Expr) {
	if expr == nil {
		ctx.write("NULL")
		return
	}
	expr.writeExpr(ctx)
}

func writeExprList(ctx *buildCtx, exprs []Expr) {
	for i, expr := range exprs {
		if i > 0 {
			ctx.write(", ")
		}
		writeExpr(ctx, expr)
	}
}

func formatParamValue(v any) string {
	switch value := v.(type) {
	case nil:
		return ""
	case string:
		return value
	case fmt.Stringer:
		return value.String()
	case bool:
		if value {
			return "1"
		}
		return "0"
	default:
		return fmt.Sprint(value)
	}
}

func quoteIdentifierPath(value string) string {
	if value == "" {
		return "``"
	}
	parts := strings.Split(value, ".")
	quoted := make([]string, 0, len(parts))
	for _, part := range parts {
		escaped := strings.ReplaceAll(part, "`", "``")
		quoted = append(quoted, "`"+escaped+"`")
	}
	return strings.Join(quoted, ".")
}

func SortedParamKeys(params map[string]string) []string {
	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
