package local

import (
	"fmt"
	"strings"

	"github.com/prometheus/prometheus/promql/parser"
)

type DelegationClassifierResult struct {
	Eligible          bool   `json:"eligible"`
	Reason            string `json:"reason,omitempty"`
	ClickHouseVersion string `json:"clickHouseVersion,omitempty"`
}

// ClassifyEntireQueryDelegation decides whether the entire expression can be
// handed to ClickHouse as a single prometheusQuery()/prometheusQueryRange()
// call. The classifier is an explicit allowlist of constructs that compliance
// has verified against the target ClickHouse version. New constructs graduate
// from the native-SQL tier into whole-query delegation only after compliance
// confirms ClickHouse matches Prometheus semantics.
func ClassifyEntireQueryDelegation(expr parser.Expr, clickHouseVersion string) DelegationClassifierResult {
	version := NormalizeClickHouseVersion(clickHouseVersion)
	if expr == nil {
		return DelegationClassifierResult{Eligible: false, Reason: "empty expression", ClickHouseVersion: version}
	}
	if expr.Type() == parser.ValueTypeScalar {
		return DelegationClassifierResult{Eligible: false, Reason: fmt.Sprintf("ClickHouse %s whole-query delegation does not support scalar-only roots", version), ClickHouseVersion: version}
	}
	if reason := unsupportedDelegationReason(expr, version); reason != "" {
		return DelegationClassifierResult{Eligible: false, Reason: reason, ClickHouseVersion: version}
	}
	return DelegationClassifierResult{Eligible: true, ClickHouseVersion: version}
}

// unsupportedDelegationReason returns a human-readable reason if the
// expression contains any construct not yet verified for whole-query
// delegation. It returns "" when the entire expression is on the allowlist.
func unsupportedDelegationReason(node parser.Node, version string) string {
	if node == nil {
		return ""
	}
	switch typed := node.(type) {
	case *parser.VectorSelector, *parser.MatrixSelector:
		return ""
	case *parser.ParenExpr:
		return unsupportedDelegationReason(typed.Expr, version)
	case *parser.StepInvariantExpr:
		return unsupportedDelegationReason(typed.Expr, version)
	case *parser.AggregateExpr:
		return fmt.Sprintf("ClickHouse %s whole-query delegation does not yet allow aggregations", version)
	case *parser.SubqueryExpr:
		return fmt.Sprintf("ClickHouse %s whole-query delegation does not yet allow subqueries", version)
	case *parser.BinaryExpr:
		return fmt.Sprintf("ClickHouse %s whole-query delegation does not yet allow binary operators", version)
	case *parser.UnaryExpr:
		return fmt.Sprintf("ClickHouse %s whole-query delegation does not yet allow unary operators", version)
	case *parser.Call:
		return fmt.Sprintf("ClickHouse %s whole-query delegation does not yet allow %s()", version, strings.ToLower(typed.Func.Name))
	case *parser.NumberLiteral, *parser.StringLiteral:
		return fmt.Sprintf("ClickHouse %s whole-query delegation does not support literal roots", version)
	}
	return fmt.Sprintf("ClickHouse %s whole-query delegation does not yet allow %T", version, node)
}

func NormalizeClickHouseVersion(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "26.3"
	}
	return trimmed
}
