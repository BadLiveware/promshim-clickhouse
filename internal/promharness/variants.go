package promharness

import (
	"fmt"
	"strings"
)

type expandedQuerySpec struct {
	Variant string
	Spec    QuerySpec
}

type rangeStepMatrixVariant struct {
	Name        string
	StepSeconds int64
}

func expandQueryVariants(spec QuerySpec) ([]expandedQuerySpec, error) {
	switch spec.Endpoint {
	case "query":
		return expandInstantQueryVariants(spec), nil
	case "query_range":
		return expandRangeQueryVariants(spec)
	default:
		return nil, fmt.Errorf("unsupported endpoint %q", spec.Endpoint)
	}
}

func expandInstantQueryVariants(spec QuerySpec) []expandedQuerySpec {
	if len(spec.TimeOffsets) == 0 {
		return []expandedQuerySpec{{Spec: spec}}
	}
	variants := make([]expandedQuerySpec, 0, len(spec.TimeOffsets))
	for index, variant := range spec.TimeOffsets {
		clone := spec
		clone.TimeOffsetSeconds = variant.TimeOffsetSeconds
		clone.TimeOffsets = nil
		clone.RangeOffsets = nil
		variants = append(variants, expandedQuerySpec{Variant: instantVariantName(index, variant), Spec: clone})
	}
	return variants
}

func expandRangeQueryVariants(spec QuerySpec) ([]expandedQuerySpec, error) {
	baseVariants, err := expandBaseRangeQueryVariants(spec)
	if err != nil {
		return nil, err
	}
	if !spec.RangeStepMatrix {
		return baseVariants, nil
	}
	out := make([]expandedQuerySpec, 0, len(baseVariants)*4)
	for _, base := range baseVariants {
		matrixVariants, err := expandRangeStepMatrixVariants(base.Spec)
		if err != nil {
			return nil, err
		}
		for _, matrix := range matrixVariants {
			out = append(out, expandedQuerySpec{
				Variant: joinVariantNames(base.Variant, matrix.Variant),
				Spec:    matrix.Spec,
			})
		}
	}
	return out, nil
}

func expandBaseRangeQueryVariants(spec QuerySpec) ([]expandedQuerySpec, error) {
	if len(spec.RangeOffsets) == 0 {
		return []expandedQuerySpec{{Spec: spec}}, nil
	}
	variants := make([]expandedQuerySpec, 0, len(spec.RangeOffsets))
	for index, variant := range spec.RangeOffsets {
		if variant.EndOffsetSeconds < variant.StartOffsetSeconds {
			return nil, fmt.Errorf("query %q range variant %q has end before start", spec.Name, rangeVariantName(index, variant))
		}
		clone := spec
		clone.StartOffsetSeconds = variant.StartOffsetSeconds
		clone.EndOffsetSeconds = variant.EndOffsetSeconds
		clone.TimeOffsets = nil
		clone.RangeOffsets = nil
		variants = append(variants, expandedQuerySpec{Variant: rangeVariantName(index, variant), Spec: clone})
	}
	return variants, nil
}

func expandRangeStepMatrixVariants(spec QuerySpec) ([]expandedQuerySpec, error) {
	rangeSeconds := spec.EndOffsetSeconds - spec.StartOffsetSeconds
	if rangeSeconds <= 0 {
		return nil, fmt.Errorf("query %q range-step matrix requires end > start", spec.Name)
	}
	if spec.StepSeconds <= 0 {
		return nil, fmt.Errorf("query %q range-step matrix requires stepSeconds > 0", spec.Name)
	}
	matrix := defaultRangeStepMatrix(rangeSeconds, spec.StepSeconds)
	out := make([]expandedQuerySpec, 0, len(matrix))
	seen := map[int64]struct{}{}
	for _, variant := range matrix {
		if variant.StepSeconds <= 0 {
			continue
		}
		if _, dup := seen[variant.StepSeconds]; dup {
			continue
		}
		seen[variant.StepSeconds] = struct{}{}
		clone := spec
		clone.TimeOffsets = nil
		clone.RangeOffsets = nil
		clone.RangeStepMatrix = false
		clone.StepSeconds = variant.StepSeconds
		out = append(out, expandedQuerySpec{Variant: variant.Name, Spec: clone})
	}
	return out, nil
}

func defaultRangeStepMatrix(rangeSeconds, baseStep int64) []rangeStepMatrixVariant {
	return []rangeStepMatrixVariant{
		{Name: "step_evenly_divides_range", StepSeconds: evenDividingStep(rangeSeconds, baseStep)},
		{Name: "step_not_evenly_divides_range", StepSeconds: nonDividingStep(rangeSeconds, baseStep)},
		{Name: "step_gt_range_over_2", StepSeconds: (rangeSeconds / 2) + 1},
		{Name: "step_eq_range", StepSeconds: rangeSeconds},
	}
}

func evenDividingStep(rangeSeconds, baseStep int64) int64 {
	if rangeSeconds <= 0 {
		return 1
	}
	if baseStep > 0 && rangeSeconds%baseStep == 0 {
		return baseStep
	}
	for step := minInt64(baseStep, rangeSeconds); step >= 1; step-- {
		if rangeSeconds%step == 0 {
			return step
		}
	}
	return 1
}

func nonDividingStep(rangeSeconds, baseStep int64) int64 {
	if rangeSeconds <= 1 {
		return 1
	}
	start := baseStep + 1
	if start <= 0 {
		start = 1
	}
	for step := start; step <= rangeSeconds; step++ {
		if rangeSeconds%step != 0 {
			return step
		}
	}
	return rangeSeconds - 1
}

func minInt64(left, right int64) int64 {
	if left <= 0 || left > right {
		return right
	}
	return left
}

func instantVariantName(index int, variant QueryInstantVariantSpec) string {
	if name := strings.TrimSpace(variant.Name); name != "" {
		return name
	}
	return fmt.Sprintf("time_offset=%d", variant.TimeOffsetSeconds)
}

func rangeVariantName(index int, variant QueryRangeVariantSpec) string {
	if name := strings.TrimSpace(variant.Name); name != "" {
		return name
	}
	return fmt.Sprintf("start=%d,end=%d", variant.StartOffsetSeconds, variant.EndOffsetSeconds)
}

func joinVariantNames(parts ...string) string {
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			continue
		}
		filtered = append(filtered, part)
	}
	return strings.Join(filtered, "/")
}
