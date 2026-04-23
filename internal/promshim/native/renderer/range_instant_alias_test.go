package renderer

import (
	"strings"
	"testing"
	"time"

	"github.com/BadLiveware/promshim-ch/internal/promshim/native"
	"github.com/BadLiveware/promshim-ch/internal/promshim/storage"
)

func TestRenderFragmentBuildsInstantAvgOverTimeSQLUsingRowFastPath(t *testing.T) {
	fragment := &native.NativeFragment{
		Kind:       native.FragmentKindRangeFunction,
		OutputKind: native.OutputKindInstantVector,
		RangeFunction: &native.RangeFunctionFragment{
			Func: "avg_over_time",
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
		Mode:             native.RenderModeInstant,
		EvaluationTimeMS: 300000,
		RequiredStartMS:  0,
		RequiredEndMS:    300000,
	})
	if err != nil {
		t.Fatalf("expected rendered SQL, got error: %v", err)
	}
	if !strings.Contains(rendered.SQL, "avgIf(value, NOT isNaN(value))") {
		t.Fatalf("expected instant avg_over_time row fast path to aggregate directly over rows, got %q", rendered.SQL)
	}
	for _, unwanted := range []string{
		"groupArray((timestamp, value))) AS time_series",
		"time_series AS time_series",
		"range_values",
		"range_has_nan",
		"range_values_finite",
	} {
		if strings.Contains(rendered.SQL, unwanted) {
			t.Fatalf("expected instant avg_over_time row fast path to avoid %q, got %q", unwanted, rendered.SQL)
		}
	}
}

func TestRenderFragmentBuildsInstantRateSQLUsingRowFastPath(t *testing.T) {
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
		Mode:             native.RenderModeInstant,
		EvaluationTimeMS: 300000,
		RequiredStartMS:  0,
		RequiredEndMS:    300000,
	})
	if err != nil {
		t.Fatalf("expected rendered SQL, got error: %v", err)
	}
	for _, unwanted := range []string{
		"time_series AS time_series",
		"groupArray((timestamp, value))) AS time_series",
		"range_values",
		"range_has_nan",
		"arrayPopBack(range_values)",
		"arrayPopFront(range_values)",
	} {
		if strings.Contains(rendered.SQL, unwanted) {
			t.Fatalf("expected instant rate row fast path to avoid %q, got %q", unwanted, rendered.SQL)
		}
	}
	for _, expected := range []string{
		"lagInFrame(value, 1, nan) OVER (PARTITION BY final_tags ORDER BY timestamp)",
		"sum(if(isNaN(value) OR isNaN(prev_value), toFloat64(0), if(value < prev_value, value, value - prev_value))) AS range_counter_delta_sum",
		"max(timestamp) - min(timestamp) AS range_duration_ms",
		"if(nan_count > 0 OR sample_count <= 1 OR range_duration_ms <= 0, nan, range_counter_delta_sum / range_duration_ms)",
	} {
		if !strings.Contains(rendered.SQL, expected) {
			t.Fatalf("expected %q in SQL, got %q", expected, rendered.SQL)
		}
	}
}
