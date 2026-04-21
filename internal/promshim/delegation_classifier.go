package promshim

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/BadLiveware/promshim-ch/internal/promshim/plan"
	"github.com/prometheus/prometheus/promql/parser"
)

type delegationClassifierResult struct {
	Eligible          bool   `json:"eligible"`
	Reason            string `json:"reason,omitempty"`
	ClickHouseVersion string `json:"clickHouseVersion,omitempty"`
}

func classifyEntireQueryDelegation(expr parser.Expr, clickHouseVersion string) delegationClassifierResult {
	version := normalizeClickHouseVersion(clickHouseVersion)
	if expr == nil {
		return delegationClassifierResult{Eligible: false, Reason: "empty expression", ClickHouseVersion: version}
	}
	if expr.Type() == parser.ValueTypeScalar {
		return delegationClassifierResult{Eligible: false, Reason: fmt.Sprintf("ClickHouse %s whole-query delegation does not support scalar-only roots", version), ClickHouseVersion: version}
	}
	result := plan.AnalyzeDelegatableExpression(expr)
	if !result.Supported {
		return delegationClassifierResult{Eligible: false, Reason: result.Reason, ClickHouseVersion: version}
	}
	return delegationClassifierResult{Eligible: true, ClickHouseVersion: version}
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
