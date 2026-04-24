package renderer

import (
	"testing"

	logicalpkg "github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
	"github.com/prometheus/prometheus/promql/parser"
)

func mustParseExpr(t *testing.T, query string) parser.Expr {
	t.Helper()
	expr, err := logicalpkg.ParseExpression(query)
	if err != nil {
		t.Fatalf("parse %q: %v", query, err)
	}
	return expr
}
