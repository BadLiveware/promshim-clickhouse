package renderer

import (
	"strings"

	"ch-observability/internal/promshim/native"
	"ch-observability/internal/promshim/storage/schema"

	"github.com/prometheus/prometheus/promql/parser"
)

type RenderParams struct {
	Mode                native.RenderMode
	EvaluationTimeMS    int64
	StartMS             int64
	EndMS               int64
	StepMS              int64
	RequiredStartMS     int64
	RequiredEndMS       int64
	ResolveSourcePromQL func(parser.Expr) (string, error)
	// RequireFullTags indicates whether a parent renderer (histogram function or
	// projection) has explicitly declared that full tags are required from the
	// underlying selector. Default (false) means no explicit requirement from the
	// parent — the selector's own SelectorSource.RequireFullTags still governs.
	// In Phase A direct-render code, these fields will be merged with the
	// SelectorSource equivalents.
	RequireFullTags bool
	// RequiredTagLabels is the set of labels the parent requires from the
	// underlying selector. When RequireFullTags is false (indicating a grouping
	// aggregation child), this is a fresh copy of the child's Grouping labels.
	// Default (nil) means no explicit tag requirement from the parent.
	RequiredTagLabels []string
}

type RenderedQuery struct {
	SQL         string
	QueryParams map[string]string
}

func mergeRenderedQueryParams(dst, src map[string]string) {
	for key, value := range src {
		dst[key] = value
	}
}

func trimRenderedQuerySQL(sql string) string {
	sql = strings.TrimSpace(sql)
	if idx := strings.LastIndex(sql, schema.SettingsLine); idx >= 0 {
		sql = strings.TrimSpace(sql[:idx])
	}
	if idx := strings.LastIndex(sql, schema.FormatLine); idx >= 0 {
		sql = strings.TrimSpace(sql[:idx])
	}
	return sql
}
