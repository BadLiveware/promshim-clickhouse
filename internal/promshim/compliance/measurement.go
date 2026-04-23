package compliance

import (
	"strings"

	"ch-observability/internal/promshim/local"
	nativeplan "ch-observability/internal/promshim/native"
	logicalpkg "ch-observability/internal/promshim/logical"
)

type NativeMeasurementSnapshot struct {
	Query               string `json:"query"`
	OutputKind          string `json:"outputKind,omitempty"`
	PlanSupported       bool   `json:"planSupported"`
	PlanDifficulty      string `json:"planDifficulty,omitempty"`
	PlanReason          string `json:"planReason,omitempty"`
	NativeLowerable     bool   `json:"nativeLowerable"`
	NativeReason        string `json:"nativeReason,omitempty"`
	FragmentKind        string `json:"fragmentKind,omitempty"`
	AggregationEligible bool   `json:"aggregationEligible,omitempty"`
}

func ClassifyPath2Status(snapshot NativeMeasurementSnapshot, query string) string {
	if snapshot.NativeLowerable {
		return "yes"
	}
	return "no"
}

func ClassifyImplementationBucket(query string, snapshot NativeMeasurementSnapshot, shouldFail bool) string {
	if shouldFail {
		return "expected_error_surface"
	}
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return "other"
	}
	if startsWithScalarLiteral(trimmed) {
		return "scalar_roots"
	}
	name := MeasurementFunctionName(trimmed)
	if strings.Contains(trimmed, " group_left") || strings.Contains(trimmed, " group_right") || strings.Contains(trimmed, " on(") || strings.Contains(trimmed, " ignoring(") || strings.Contains(trimmed, " fill(") {
		return "vector_matching_and_joins"
	}
	if strings.Contains(trimmed, " and ") || strings.Contains(trimmed, " or ") || strings.Contains(trimmed, " unless ") {
		return "set_operators"
	}
	switch name {
	case "topk", "bottomk", "count_values", "limitk", "limit_ratio":
		return "selection_aggregations"
	case "label_replace", "label_join":
		return "label_mutation"
	case "histogram_quantile", "histogram_fraction", "histogram_count", "histogram_sum", "histogram_avg", "histogram_quantiles", "histogram_stddev", "histogram_stdvar":
		return "histogram_helpers"
	case "absent", "absent_over_time":
		return "absent_family"
	case "sort", "sort_desc", "sort_by_label", "sort_by_label_desc":
		return "sort_family"
	case "round":
		return "round_function"
	case "vector":
		return "vector_wrapper"
	case "quantile_over_time":
		return "quantile_over_time"
	case "info":
		return "info_function"
	case "clamp", "clamp_min", "clamp_max":
		return "clamp_family"
	}
	if snapshot.OutputKind == "scalar" {
		return "scalar_roots"
	}
	if strings.Contains(snapshot.NativeReason, "aggregation operator is not supported") {
		return "selection_aggregations"
	}
	if strings.Contains(snapshot.NativeReason, "binary expression currently lowers natively only") {
		return "binary_expression_gaps"
	}
	return "other"
}

func MeasurementFunctionName(query string) string {
	query = strings.TrimSpace(query)
	if query == "" {
		return ""
	}
	idx := strings.IndexByte(query, '(')
	if idx <= 0 {
		return ""
	}
	candidate := strings.TrimSpace(query[:idx])
	if strings.Contains(candidate, " ") {
		return ""
	}
	return strings.ToLower(candidate)
}

func startsWithScalarLiteral(query string) bool {
	for _, prefix := range []string{"42", "1.234", ".123", "Inf", "+Inf", "-Inf", "NaN", "0x", "1 *", "1.", "0."} {
		if strings.HasPrefix(query, prefix) {
			return true
		}
	}
	return false
}

func MeasureNativeSupport(query string) (NativeMeasurementSnapshot, error) {
	expr, err := logicalpkg.ParseExpression(query)
	if err != nil {
		return NativeMeasurementSnapshot{Query: query}, err
	}

	support := logicalpkg.AnalyzeExpression(expr)
	snapshot := NativeMeasurementSnapshot{
		Query:          query,
		PlanSupported:  support.Supported,
		PlanDifficulty: string(support.Difficulty),
		PlanReason:     support.Reason,
	}
	if !support.Supported {
		return snapshot, nil
	}

	logical, err := local.BuildLogicalPlan(expr)
	if err != nil {
		return snapshot, err
	}
	analysis := nativeplan.Analyze(logical)
	_ = logicalpkg.Analyze(logical) // Task 3 warm-up: exercise the new enrichment walk.
	if analysis == nil || analysis.Root == nil {
		return snapshot, nil
	}
	root := analysis.Root
	snapshot.OutputKind = string(root.OutputKind)
	snapshot.NativeLowerable = root.NativeLowerable
	snapshot.NativeReason = root.NativeReason
	if root.Fragment != nil {
		snapshot.FragmentKind = string(root.Fragment.Kind)
	}
	if root.Aggregation != nil {
		snapshot.AggregationEligible = root.Aggregation.Eligible
	}
	return snapshot, nil
}
