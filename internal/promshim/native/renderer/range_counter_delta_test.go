package renderer

import (
	"strings"
	"testing"
	"time"

	"github.com/BadLiveware/promshim-ch/internal/promshim/native"
	"github.com/BadLiveware/promshim-ch/internal/promshim/storage"
)

func TestRenderFragmentBuildsRangeRateSQLWithCounterDeltaAliasesForMaterializedWindows(t *testing.T) {
	fragment := &native.NativeFragment{
		Kind:       native.FragmentKindRangeFunction,
		OutputKind: native.OutputKindInstantVector,
		RangeFunction: &native.RangeFunctionFragment{
			Func: "rate",
			Child: &native.NativeFragment{
				Kind:       native.FragmentKindLeafSource,
				OutputKind: native.OutputKindRangeMatrix,
				Selector: &native.SelectorSource{
					Kind:       native.SelectorKindRangeVector,
					MetricName: "demo_cpu_usage_seconds_total",
					Lookback:   5 * time.Minute,
				},
				ValueExpr: "{value}",
				TagsExpr:  "{tags}",
			},
		},
	}

	rendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{
		Mode:            native.RenderModeRange,
		StartMS:         0,
		EndMS:           300000,
		StepMS:          30000,
		RequiredStartMS: -300000,
		RequiredEndMS:   300000,
	})
	if err != nil {
		t.Fatalf("expected rendered SQL, got error: %v", err)
	}
	if strings.Contains(rendered.SQL, "window_values_prev") || strings.Contains(rendered.SQL, "window_values_cur") {
		t.Fatalf("expected rate SQL to avoid separate pairwise-neighbor aliases, got %q", rendered.SQL)
	}
	if !strings.Contains(rendered.SQL, "arraySum(arrayMap((p, c) -> if(c < p, c, c - p), arrayPopBack(window_values), arrayPopFront(window_values))) AS counter_delta_sum") {
		t.Fatalf("expected rate SQL to inline counter_delta_sum from window_values, got %q", rendered.SQL)
	}
	if strings.Contains(rendered.SQL, "arraySlice(window_values") {
		t.Fatalf("expected rate SQL to avoid arraySlice(window_values, ...), got %q", rendered.SQL)
	}
	if strings.Count(rendered.SQL, "arrayMap((p, c) -> if(c < p, c, c - p)") != 1 {
		t.Fatalf("expected counter-delta lambda to be emitted once, got %q", rendered.SQL)
	}
}

func TestRenderFragmentBuildsRangeChangesSQLWithPrecomputedChangesCount(t *testing.T) {
	fragment := &native.NativeFragment{
		Kind:       native.FragmentKindRangeFunction,
		OutputKind: native.OutputKindInstantVector,
		RangeFunction: &native.RangeFunctionFragment{
			Func: "changes",
			Child: &native.NativeFragment{
				Kind:       native.FragmentKindLeafSource,
				OutputKind: native.OutputKindRangeMatrix,
				Selector: &native.SelectorSource{
					Kind:       native.SelectorKindRangeVector,
					MetricName: "demo_cpu_usage_seconds_total",
					Lookback:   5 * time.Minute,
				},
				ValueExpr: "{value}",
				TagsExpr:  "{tags}",
			},
		},
	}

	rendered, err := RenderFragment(storage.QueryConfig{Database: "observability", Table: "prometheus"}, fragment, RenderParams{
		Mode:            native.RenderModeRange,
		StartMS:         0,
		EndMS:           300000,
		StepMS:          30000,
		RequiredStartMS: -300000,
		RequiredEndMS:   300000,
	})
	if err != nil {
		t.Fatalf("expected rendered SQL, got error: %v", err)
	}
	if !strings.Contains(rendered.SQL, "changes_count") {
		t.Fatalf("expected precomputed changes_count alias in SQL, got %q", rendered.SQL)
	}
	if strings.Contains(rendered.SQL, "arraySlice(window_values") {
		t.Fatalf("expected changes SQL to avoid arraySlice(window_values, ...), got %q", rendered.SQL)
	}
}
