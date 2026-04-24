package renderer

import (
	"strings"

	"github.com/BadLiveware/promshim-ch/internal/promshim/native"
	"github.com/BadLiveware/promshim-ch/internal/promshim/storage/schema"

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
	// projection, or a selection / count_values aggregation) has explicitly
	// declared that full tags are required from the underlying selector.
	// After 13c-14e native.SelectorSource no longer carries narrowing state,
	// so RenderParams is the single source of truth: true forces the storage
	// SelectorSource's RequireFullTags=true, overriding any
	// RequiredTagLabels narrowing. Default (false) means the storage
	// selector's own (zero-valued) narrowing state applies.
	RequireFullTags bool
	// RequiredTagLabels is the set of labels the parent requires from the
	// underlying selector. When RequireFullTags is false (indicating a grouping
	// aggregation child), this is a fresh copy of the child's Grouping labels.
	// Default (nil) means no explicit tag requirement from the parent; the
	// storage selector falls through to the full-tags base path.
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
