package sqlb

import "fmt"

// Predicate is an expression intended for WHERE, HAVING, JOIN ON, or other
// boolean SQL positions. It aliases Expr so callers can document intent without
// losing access to the existing expression surface.
type Predicate = Expr

// Placeholder emits a ClickHouse named query parameter reference without adding
// a value to the Build parameter map. Use Param when the builder should also bind
// the value; use Placeholder when the caller owns the parameter map.
type Placeholder struct {
	Name string
	Type string
}

// Number emits a trusted numeric literal. Prefer Param or Placeholder for
// user-controlled values.
type Number string

// Infix emits a binary expression without adding grouping parentheses. It is
// useful where the surrounding SQL shape already provides the necessary
// precedence or where stable golden SQL matters.
type Infix struct {
	Op   string
	L, R Expr
}

// Compare emits a boolean comparison without wrapping it in parentheses.
type Compare struct {
	Op   string
	L, R Expr
}

type AndList struct{ Parts []Predicate }

type OrList struct{ Parts []Predicate }

type NotExpr struct{ X Predicate }

type InSubquery struct {
	X     Expr
	Query *Select
}

func (p Placeholder) writeExpr(ctx *buildCtx) {
	ctx.write("{")
	ctx.write(p.Name)
	ctx.write(":")
	ctx.write(p.Type)
	ctx.write("}")
}

func (n Number) writeExpr(ctx *buildCtx) { ctx.write(string(n)) }

func (e Infix) writeExpr(ctx *buildCtx) {
	writeExpr(ctx, e.L)
	ctx.write(" ")
	ctx.write(e.Op)
	ctx.write(" ")
	writeExpr(ctx, e.R)
}

func (c Compare) writeExpr(ctx *buildCtx) {
	writeExpr(ctx, c.L)
	ctx.write(" ")
	ctx.write(c.Op)
	ctx.write(" ")
	writeExpr(ctx, c.R)
}

func (a AndList) writeExpr(ctx *buildCtx) {
	writePredicateList(ctx, a.Parts, " AND ", false)
}

func (o OrList) writeExpr(ctx *buildCtx) {
	writePredicateList(ctx, o.Parts, " OR ", true)
}

func (n NotExpr) writeExpr(ctx *buildCtx) {
	ctx.write("NOT ")
	writeExpr(ctx, n.X)
}

func (i InSubquery) writeExpr(ctx *buildCtx) {
	writeExpr(ctx, i.X)
	ctx.write(" IN (")
	if i.Query != nil {
		i.Query.writeSelect(ctx)
	}
	ctx.write(")")
}

func writePredicateList(ctx *buildCtx, predicates []Predicate, sep string, grouped bool) {
	parts := make([]Predicate, 0, len(predicates))
	for _, predicate := range predicates {
		if predicate != nil {
			parts = append(parts, predicate)
		}
	}
	if grouped && len(parts) > 1 {
		ctx.write("(")
	}
	for i, predicate := range parts {
		if i > 0 {
			ctx.write(sep)
		}
		writeExpr(ctx, predicate)
	}
	if grouped && len(parts) > 1 {
		ctx.write(")")
	}
}

func Raw(sql string) Expr { return RawLit{V: sql} }

func Func(name string, args ...Expr) Call { return Call{Name: name, Args: args} }

func Int64Placeholder(name string) Placeholder { return Placeholder{Name: name, Type: "Int64"} }

func UInt64Placeholder(name string) Placeholder { return Placeholder{Name: name, Type: "UInt64"} }

func StringPlaceholder(name string) Placeholder { return Placeholder{Name: name, Type: "String"} }

func Num(v int64) Number { return Number(fmt.Sprintf("%d", v)) }

func Add(l, r Expr) Infix { return Infix{Op: "+", L: l, R: r} }

func Sub(l, r Expr) Infix { return Infix{Op: "-", L: l, R: r} }

func GroupedSub(l, r Expr) Binary { return Binary{Op: "-", L: l, R: r} }

func Eq(l, r Expr) Compare { return Compare{Op: "=", L: l, R: r} }

func GTE(l, r Expr) Compare { return Compare{Op: ">=", L: l, R: r} }

func LTE(l, r Expr) Compare { return Compare{Op: "<=", L: l, R: r} }

func GT(l, r Expr) Compare { return Compare{Op: ">", L: l, R: r} }

func LT(l, r Expr) Compare { return Compare{Op: "<", L: l, R: r} }

func And(predicates ...Predicate) AndList { return AndList{Parts: predicates} }

func Or(predicates ...Predicate) OrList { return OrList{Parts: predicates} }

func Not(predicate Predicate) NotExpr { return NotExpr{X: predicate} }

func FromUnixTimestamp64Milli(x Expr) Call { return Func("fromUnixTimestamp64Milli", x) }

func ToUnixTimestamp64Milli(x Expr) Call { return Func("toUnixTimestamp64Milli", x) }

func ToIntervalMillisecond(x Expr) Call { return Func("toIntervalMillisecond", x) }

func PositiveModulo(x, mod Expr) Call { return Func("positiveModulo", x, mod) }

func IsNaN(x Expr) Call { return Func("isNaN", x) }

func ArgMax(value, order Expr) Call { return Func("argMax", value, order) }
