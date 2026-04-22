package sqlb

import (
	"reflect"
	"strings"
	"testing"
)

func TestSelectBuildBindsAutoAndNamedParams(t *testing.T) {
	query := &Select{
		Columns: []ColExpr{
			{Expr: Ident("src.metric_name"), Alias: "metric"},
			{Expr: Call{Name: "toFloat64", Args: []Expr{Lit{V: 42, Type: "Int64"}}}, Alias: "value"},
		},
		From:  Table{DB: "observability", Name: "prometheus", Alias: "src"},
		Where: Binary{Op: "=", L: Ident("src.metric_name"), R: Param{Name: "metric", Type: "String", V: "up"}},
		OrderBy: []OrderExpr{
			{Expr: Ident("metric")},
		},
	}

	sql, params, err := query.Build()
	if err != nil {
		t.Fatalf("expected built SQL, got error: %v", err)
	}
	expectedSQL := "SELECT src.metric_name AS metric, toFloat64({p0:Int64}) AS value FROM `observability`.`prometheus` AS src WHERE (src.metric_name = {metric:String}) ORDER BY metric"
	if NormalizeSQL(sql) != expectedSQL {
		t.Fatalf("unexpected SQL:\nwant: %s\n got: %s", expectedSQL, NormalizeSQL(sql))
	}
	expectedParams := map[string]string{"param_metric": "up", "param_p0": "42"}
	if !reflect.DeepEqual(params, expectedParams) {
		t.Fatalf("unexpected params: want %#v got %#v", expectedParams, params)
	}
}

func TestSelectBuildSupportsNestedSubqueries(t *testing.T) {
	inner := &Select{
		Columns: []ColExpr{
			{Expr: Ident("tags"), Alias: "tags"},
			{Expr: Ident("value"), Alias: "value"},
		},
		From:  Table{Name: "input", Alias: "src"},
		Where: Binary{Op: ">", L: Ident("value"), R: Lit{V: 0, Type: "Int64"}},
	}
	outer := &Select{
		Columns: []ColExpr{
			{Expr: Ident("tags"), Alias: "tags"},
			{Expr: Call{Name: "sum", Args: []Expr{Ident("value")}}, Alias: "value"},
		},
		From:    SubSelect{S: inner, Alias: "filtered"},
		GroupBy: []Expr{Ident("tags")},
		OrderBy: []OrderExpr{{Expr: Ident("tags")}},
		Limit:   &Limit{Count: 10},
	}

	sql, params, err := outer.Build()
	if err != nil {
		t.Fatalf("expected built SQL, got error: %v", err)
	}
	expectedSQL := "SELECT tags AS tags, sum(value) AS value FROM (SELECT tags AS tags, value AS value FROM `input` AS src WHERE (value > {p0:Int64})) AS filtered GROUP BY tags ORDER BY tags LIMIT 10"
	if NormalizeSQL(sql) != expectedSQL {
		t.Fatalf("unexpected SQL:\nwant: %s\n got: %s", expectedSQL, NormalizeSQL(sql))
	}
	if !reflect.DeepEqual(params, map[string]string{"param_p0": "0"}) {
		t.Fatalf("unexpected params: %#v", params)
	}
}

func TestSelectBuildSupportsJoinSources(t *testing.T) {
	query := &Select{
		Columns: []ColExpr{{Expr: Ident("lhs.id"), Alias: "id"}, {Expr: Ident("rhs.value"), Alias: "value"}},
		From: Join{
			Left:  Table{Name: "lhs", Alias: "lhs"},
			Right: Table{Name: "rhs", Alias: "rhs"},
			Kind:  "INNER",
			On:    Binary{Op: "=", L: Ident("lhs.id"), R: Ident("rhs.id")},
		},
	}

	sql, params, err := query.Build()
	if err != nil {
		t.Fatalf("expected built SQL, got error: %v", err)
	}
	expectedSQL := "SELECT lhs.id AS id, rhs.value AS value FROM `lhs` AS lhs INNER JOIN `rhs` AS rhs ON (lhs.id = rhs.id)"
	if NormalizeSQL(sql) != expectedSQL {
		t.Fatalf("unexpected SQL:\nwant: %s\n got: %s", expectedSQL, NormalizeSQL(sql))
	}
	if len(params) != 0 {
		t.Fatalf("unexpected params: %#v", params)
	}
}

func TestAdvancedExprSurfaceBuildsComposableArrayAndConditionalForms(t *testing.T) {
	expr := MultiIf{
		Cases: []MultiIfArm{
			{When: Binary{Op: "<", L: Call{Name: "length", Args: []Expr{Ident("window_values")}}, R: Lit{V: 2, Type: "Int64"}}, Then: RawLit{V: "NULL"}},
			{When: Call{Name: "arrayAll", Args: []Expr{Lambda{Params: []Ident{"v"}, Body: Binary{Op: "=", L: Ident("v"), R: Subscr{Array: Ident("window_values"), Index: Lit{V: 1, Type: "Int64"}}}}, Ident("window_values")}}, Then: TupleElem{X: ArrayFold{Lambda: Lambda{Params: []Ident{"acc", "x"}, Body: Tuple{Elems: []Expr{Ident("acc"), Ident("x")}}}, Src: Ident("window_values"), Init: Tuple{Elems: []Expr{Lit{V: 0, Type: "Int64"}, Lit{V: 0, Type: "Int64"}}}}, K: 2}},
		},
		Else: Cast{X: ArrayRed{Agg: "sum", Args: []Expr{Map{Lambda: Lambda{Params: []Ident{"x"}, Body: Ident("x")}, Arrays: []Expr{Ident("window_values")}}}}, To: "Nullable(Float64)"},
	}
	query := &Select{Columns: []ColExpr{{Expr: expr, Alias: "value"}}}

	sql, params, err := query.Build()
	if err != nil {
		t.Fatalf("expected built SQL, got error: %v", err)
	}
	checks := []string{
		"multiIf(",
		"arrayAll(v -> (v = window_values[{p1:Int64}]), window_values)",
		"arrayFold((acc, x) -> (acc, x), window_values, ({p2:Int64}, {p3:Int64}))",
		"tupleElement(",
		"arrayReduce({p4:String}, arrayMap(x -> x, window_values))",
		"CAST(",
	}
	for _, check := range checks {
		if !containsNormalized(sql, check) {
			t.Fatalf("expected SQL to contain %q, got %s", check, NormalizeSQL(sql))
		}
	}
	expectedParams := map[string]string{
		"param_p0": "2",
		"param_p1": "1",
		"param_p2": "0",
		"param_p3": "0",
		"param_p4": "sum",
		"param_p5": "Nullable(Float64)",
	}
	if !reflect.DeepEqual(params, expectedParams) {
		t.Fatalf("unexpected params: want %#v got %#v", expectedParams, params)
	}
}

func TestNormalizeSQL(t *testing.T) {
	input := "SELECT\n  value\nFROM  table\nWHERE   a = 1"
	if got := NormalizeSQL(input); got != "SELECT value FROM table WHERE a = 1" {
		t.Fatalf("unexpected normalized SQL: %q", got)
	}
}

func containsNormalized(sql, fragment string) bool {
	return strings.Contains(NormalizeSQL(sql), NormalizeSQL(fragment))
}
