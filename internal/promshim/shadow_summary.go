package promshim

import "sync"

type shadowSummary struct {
	Total             int64            `json:"total"`
	ByStatus          map[string]int64 `json:"byStatus,omitempty"`
	ByCategory        map[string]int64 `json:"byCategory,omitempty"`
	ByCompareMode     map[string]int64 `json:"byCompareMode,omitempty"`
	TotalServedPlanMs int64            `json:"totalServedPlanMs"`
	TotalServedEvalMs int64            `json:"totalServedEvalMs"`
	TotalShadowPlanMs int64            `json:"totalShadowPlanMs"`
	TotalShadowEvalMs int64            `json:"totalShadowEvalMs"`
}

type shadowSummaryRecorder struct {
	mu                sync.Mutex
	metrics           *shadowMetrics
	total             int64
	byStatus          map[string]int64
	byCategory        map[string]int64
	byCompareMode     map[string]int64
	totalServedPlanMs int64
	totalServedEvalMs int64
	totalShadowPlanMs int64
	totalShadowEvalMs int64
}

func newShadowSummaryRecorder(metrics *shadowMetrics) *shadowSummaryRecorder {
	return &shadowSummaryRecorder{
		metrics:       metrics,
		byStatus:      map[string]int64{},
		byCategory:    map[string]int64{},
		byCompareMode: map[string]int64{},
	}
}

func (r *shadowSummaryRecorder) record(report *shadowComparison) {
	if r == nil || report == nil {
		return
	}
	r.mu.Lock()
	r.total++
	r.byStatus[report.Status]++
	r.byCategory[shadowComparisonCategory(report)]++
	r.totalServedPlanMs += report.ServedPlanMillis
	r.totalServedEvalMs += report.ServedEvalMillis
	r.totalShadowPlanMs += report.ShadowPlanMillis
	r.totalShadowEvalMs += report.ShadowEvalMillis
	if report.CompareMode != "" {
		r.byCompareMode[report.CompareMode]++
	}
	metrics := r.metrics
	r.mu.Unlock()
	if metrics != nil {
		metrics.record(report)
	}
}

func (r *shadowSummaryRecorder) snapshot() *shadowSummary {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.total == 0 {
		return nil
	}
	return &shadowSummary{
		Total:             r.total,
		ByStatus:          cloneShadowCounts(r.byStatus),
		ByCategory:        cloneShadowCounts(r.byCategory),
		ByCompareMode:     cloneShadowCounts(r.byCompareMode),
		TotalServedPlanMs: r.totalServedPlanMs,
		TotalServedEvalMs: r.totalServedEvalMs,
		TotalShadowPlanMs: r.totalShadowPlanMs,
		TotalShadowEvalMs: r.totalShadowEvalMs,
	}
}

func cloneShadowCounts(src map[string]int64) map[string]int64 {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]int64, len(src))
	for key, value := range src {
		out[key] = value
	}
	return out
}

func shadowComparisonCategory(report *shadowComparison) string {
	if report == nil {
		return "unknown"
	}
	switch report.Status {
	case "match":
		return "match"
	case "diff":
		return "result_mismatch"
	case "skipped":
		if report.CompareMode == "structural" {
			return "unsupported_compare_mode"
		}
		return "skipped"
	case "plan_error":
		return "shadow_plan_error"
	case "execution_error":
		return "shadow_execution_error"
	case "render_error":
		return "shadow_render_error"
	default:
		return report.Status
	}
}
