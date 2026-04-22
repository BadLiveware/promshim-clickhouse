package renderer

import (
	"testing"

	planpkg "ch-observability/internal/promshim/plan"
	"github.com/prometheus/prometheus/promql/parser"
)

func mustParseExpr(t *testing.T, query string) parser.Expr {
	t.Helper()
	expr, err := planpkg.ParseExpression(query)
	if err != nil {
		t.Fatalf("parse %q: %v", query, err)
	}
	return expr
}
