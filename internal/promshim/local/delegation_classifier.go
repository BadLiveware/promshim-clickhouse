package local

import (
	"fmt"
	"strconv"
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

func compareClickHouseVersion(left, right string) int {
	lp := parseVersionParts(left)
	rp := parseVersionParts(right)
	maxLen := len(lp)
	if len(rp) > maxLen {
		maxLen = len(rp)
	}
	for i := 0; i < maxLen; i++ {
		lv, rv := 0, 0
		if i < len(lp) {
			lv = lp[i]
		}
		if i < len(rp) {
			rv = rp[i]
		}
		if lv < rv {
			return -1
		}
		if lv > rv {
			return 1
		}
	}
	return 0
}

func parseVersionParts(raw string) []int {
	parts := strings.Split(NormalizeClickHouseVersion(raw), ".")
	out := make([]int, 0, len(parts))
	for _, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil {
			break
		}
		out = append(out, value)
	}
	return out
}
