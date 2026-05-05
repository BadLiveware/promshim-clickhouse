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
//
// RecordingRuleNames, when non-empty, is the set of all recording rule metric
// names. Any query that directly references a recording rule metric name must
// NOT be delegated to ClickHouse's PromQL endpoint because:
//   - Virtual rules must be expanded by promshim before execution.
//   - Materialized rules live in a separate MergeTree table that ClickHouse
//     PromQL cannot see.
func ClassifyEntireQueryDelegation(expr parser.Expr, clickHouseVersion string, recordingRuleNames map[string]bool) DelegationClassifierResult {
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
	if len(recordingRuleNames) > 0 && containsRecordedMetric(expr, recordingRuleNames) {
		return DelegationClassifierResult{Eligible: false, Reason: "expression references a recording rule; recording rules must be expanded by promshim and/or query the materialized table", ClickHouseVersion: version}
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

// containsRecordedMetric walks the expression AST and returns true if any
// VectorSelector references a recording-rule metric name. Recording rules
// must not be delegated because they either need expansion (virtual) or
// query the materialized MergeTree table (materialized).
func containsRecordedMetric(expr parser.Expr, recordingRuleNames map[string]bool) bool {
	found := false
	parser.Inspect(expr, func(node parser.Node, path []parser.Node) error {
		if vs, ok := node.(*parser.VectorSelector); ok && vs.Name != "" {
			if recordingRuleNames[vs.Name] {
				found = true
			}
		}
		return nil
	})
	return found
}
