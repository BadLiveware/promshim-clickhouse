package promharness

import "testing"

func TestExpandQueryVariantsPreservesLegacySingleInstantSpec(t *testing.T) {
	variants, err := expandQueryVariants(QuerySpec{Name: "legacy", Endpoint: "query", Query: "up", TimeOffsetSeconds: 123})
	if err != nil {
		t.Fatal(err)
	}
	if len(variants) != 1 {
		t.Fatalf("expected one variant, got %#v", variants)
	}
	if variants[0].Variant != "" || variants[0].Spec.TimeOffsetSeconds != 123 {
		t.Fatalf("unexpected legacy instant expansion: %#v", variants[0])
	}
}

func TestExpandQueryVariantsExpandsNamedInstantOffsets(t *testing.T) {
	variants, err := expandQueryVariants(QuerySpec{
		Name:     "multi",
		Endpoint: "query",
		Query:    "up",
		TimeOffsets: []QueryInstantVariantSpec{
			{Name: "early", TimeOffsetSeconds: 60},
			{Name: "late", TimeOffsetSeconds: 540},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(variants) != 2 {
		t.Fatalf("expected two variants, got %#v", variants)
	}
	if variants[0].Variant != "early" || variants[0].Spec.TimeOffsetSeconds != 60 {
		t.Fatalf("unexpected first instant variant: %#v", variants[0])
	}
	if variants[1].Variant != "late" || variants[1].Spec.TimeOffsetSeconds != 540 {
		t.Fatalf("unexpected second instant variant: %#v", variants[1])
	}
	if len(variants[0].Spec.TimeOffsets) != 0 || len(variants[1].Spec.TimeOffsets) != 0 {
		t.Fatalf("expected expanded variants to clear nested timeOffsets: %#v", variants)
	}
}

func TestExpandQueryVariantsExpandsNamedRangeOffsets(t *testing.T) {
	variants, err := expandQueryVariants(QuerySpec{
		Name:               "range-multi",
		Endpoint:           "query_range",
		Query:              "sum_over_time(up[5m])",
		StartOffsetSeconds: 300,
		EndOffsetSeconds:   540,
		StepSeconds:        60,
		RangeOffsets: []QueryRangeVariantSpec{
			{Name: "early", StartOffsetSeconds: 60, EndOffsetSeconds: 300},
			{Name: "late", StartOffsetSeconds: 300, EndOffsetSeconds: 540},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(variants) != 2 {
		t.Fatalf("expected two variants, got %#v", variants)
	}
	if variants[0].Variant != "early" || variants[0].Spec.StartOffsetSeconds != 60 || variants[0].Spec.EndOffsetSeconds != 300 {
		t.Fatalf("unexpected first range variant: %#v", variants[0])
	}
	if variants[1].Variant != "late" || variants[1].Spec.StartOffsetSeconds != 300 || variants[1].Spec.EndOffsetSeconds != 540 {
		t.Fatalf("unexpected second range variant: %#v", variants[1])
	}
	if variants[0].Spec.StepSeconds != 60 || variants[1].Spec.StepSeconds != 60 {
		t.Fatalf("expected stepSeconds to be preserved across range variants: %#v", variants)
	}
}

func TestExpandQueryVariantsRejectsInvertedRangeOffsets(t *testing.T) {
	_, err := expandQueryVariants(QuerySpec{
		Name:         "bad",
		Endpoint:     "query_range",
		Query:        "sum_over_time(up[5m])",
		StepSeconds:  60,
		RangeOffsets: []QueryRangeVariantSpec{{Name: "bad", StartOffsetSeconds: 540, EndOffsetSeconds: 300}},
	})
	if err == nil {
		t.Fatal("expected invalid range offsets to fail")
	}
}

func TestExpandQueryVariantsExpandsRangeStepMatrix(t *testing.T) {
	variants, err := expandQueryVariants(QuerySpec{
		Name:               "matrix",
		Endpoint:           "query_range",
		Query:              "sum_over_time(up[5m])",
		StartOffsetSeconds: 300,
		EndOffsetSeconds:   540,
		StepSeconds:        60,
		RangeStepMatrix:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(variants) != 4 {
		t.Fatalf("expected four matrix variants, got %#v", variants)
	}
	if variants[0].Variant != "step_evenly_divides_range" || variants[0].Spec.StepSeconds != 60 {
		t.Fatalf("unexpected evenly-dividing variant: %#v", variants[0])
	}
	if variants[1].Variant != "step_not_evenly_divides_range" || variants[1].Spec.StepSeconds == 60 {
		t.Fatalf("unexpected non-dividing variant: %#v", variants[1])
	}
	if variants[2].Variant != "step_gt_range_over_2" || variants[2].Spec.StepSeconds <= (variants[2].Spec.EndOffsetSeconds-variants[2].Spec.StartOffsetSeconds)/2 {
		t.Fatalf("unexpected step_gt_range_over_2 variant: %#v", variants[2])
	}
	if variants[3].Variant != "step_eq_range" || variants[3].Spec.StepSeconds != variants[3].Spec.EndOffsetSeconds-variants[3].Spec.StartOffsetSeconds {
		t.Fatalf("unexpected step_eq_range variant: %#v", variants[3])
	}
}

func TestExpandQueryVariantsCombinesRangeOffsetsAndRangeStepMatrix(t *testing.T) {
	variants, err := expandQueryVariants(QuerySpec{
		Name:            "combo",
		Endpoint:        "query_range",
		Query:           "sum_over_time(up[5m])",
		StepSeconds:     60,
		RangeStepMatrix: true,
		RangeOffsets: []QueryRangeVariantSpec{
			{Name: "early", StartOffsetSeconds: 60, EndOffsetSeconds: 300},
			{Name: "late", StartOffsetSeconds: 300, EndOffsetSeconds: 540},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(variants) != 8 {
		t.Fatalf("expected eight combined variants, got %#v", variants)
	}
	if variants[0].Variant != "early/step_evenly_divides_range" {
		t.Fatalf("unexpected first combined variant name: %#v", variants[0])
	}
	if variants[4].Variant != "late/step_evenly_divides_range" {
		t.Fatalf("unexpected fifth combined variant name: %#v", variants[4])
	}
}
