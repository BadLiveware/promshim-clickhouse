package promshim

import (
	"fmt"
	"strconv"
	"strings"

	"ch-observability/internal/promshim/plan"
	"github.com/prometheus/prometheus/promql/parser"
)

type delegationClassifierResult struct {
	Eligible          bool   `json:"eligible"`
	Reason            string `json:"reason,omitempty"`
	ClickHouseVersion string `json:"clickHouseVersion,omitempty"`
}

type clickHouseCapabilityMap struct {
	Version string
}

func classifyEntireQueryDelegation(expr parser.Expr, clickHouseVersion string) delegationClassifierResult {
	version := normalizeClickHouseVersion(clickHouseVersion)
	if expr != nil && expr.Type() == parser.ValueTypeScalar {
		return delegationClassifierResult{Eligible: false, Reason: fmt.Sprintf("ClickHouse %s whole-query delegation capability map does not yet allow scalar-only roots", version), ClickHouseVersion: version}
	}
	delegatable := plan.AnalyzeDelegatableExpression(expr)
	if !delegatable.Supported {
		return delegationClassifierResult{Eligible: false, Reason: delegatable.Reason, ClickHouseVersion: version}
	}
	capabilities := clickHouseCapabilityMap{Version: version}
	unsupportedReason := ""
	parser.Inspect(expr, func(node parser.Node, _ []parser.Node) error {
		if unsupportedReason != "" || node == nil {
			return nil
		}
		if reason := capabilities.unsupportedReason(node); reason != "" {
			unsupportedReason = reason
		}
		return nil
	})
	if unsupportedReason != "" {
		return delegationClassifierResult{Eligible: false, Reason: unsupportedReason, ClickHouseVersion: version}
	}
	return delegationClassifierResult{Eligible: true, ClickHouseVersion: version}
}

func (m clickHouseCapabilityMap) unsupportedReason(node parser.Node) string {
	switch typed := node.(type) {
	case *parser.AggregateExpr:
		return fmt.Sprintf("ClickHouse %s whole-query delegation capability map does not yet allow aggregations", m.Version)
	case *parser.SubqueryExpr:
		return fmt.Sprintf("ClickHouse %s whole-query delegation capability map does not yet allow subqueries", m.Version)
	case *parser.BinaryExpr:
		return fmt.Sprintf("ClickHouse %s whole-query delegation capability map does not yet allow binary operators", m.Version)
	case *parser.Call:
		name := strings.ToLower(typed.Func.Name)
		if !m.supportsCall(name) {
			return fmt.Sprintf("ClickHouse %s whole-query delegation capability map does not yet allow %s()", m.Version, name)
		}
	}
	return ""
}

func (m clickHouseCapabilityMap) supportsCall(name string) bool {
	switch name {
	case "rate", "irate", "increase", "delta", "idelta", "changes", "deriv", "resets", "last_over_time", "sum_over_time", "avg_over_time", "min_over_time", "max_over_time", "count_over_time", "quantile_over_time", "vector", "round", "label_replace", "label_join", "histogram_quantile", "histogram_count", "histogram_sum", "histogram_avg", "histogram_fraction", "absent", "absent_over_time":
		return false
	default:
		return false
	}
}

func normalizeClickHouseVersion(raw string) string {
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
	parts := strings.Split(normalizeClickHouseVersion(raw), ".")
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
