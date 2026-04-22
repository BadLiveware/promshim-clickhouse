package renderer

import (
	"strings"
	"testing"
	"time"

	"github.com/BadLiveware/promshim-ch/internal/promshim/native"
	"github.com/BadLiveware/promshim-ch/internal/promshim/storage"
)

func TestRenderFragmentBuildsInstantAvgOverTimeSQLWithValueAliases(t *testing.T) {
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
	for _, expected := range []string{
		"arrayMap(point -> ifNull(toFloat64(tupleElement(point, 2)), nan), time_series) AS range_values",
		"arrayExists(v -> isNaN(v), range_values) AS range_has_nan",
		"arrayFilter(v -> NOT isNaN(v), range_values) AS range_values_finite",
	} {
		if !strings.Contains(rendered.SQL, expected) {
			t.Fatalf("expected %q in SQL, got %q", expected, rendered.SQL)
		}
	}
	if got := strings.Count(rendered.SQL, "arrayMap(point -> ifNull(toFloat64(tupleElement(point, 2)), nan), time_series)"); got != 1 {
		t.Fatalf("expected values array expression once, got count=%d sql=%q", got, rendered.SQL)
	}
}

func TestRenderFragmentBuildsInstantRateSQLWithRangeRateAliases(t *testing.T) {
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
	for _, expected := range []string{
		"arrayMap(point -> tupleElement(point, 1), time_series) AS range_timestamps",
		"arrayPopBack(range_values) AS range_values_prev",
		"arrayPopFront(range_values) AS range_values_cur",
		"arraySum(arrayMap((p, c) -> if(c < p, c, c - p), range_values_prev, range_values_cur)) AS range_counter_delta_sum",
		"arrayElement(range_timestamps, length(time_series)) - arrayElement(range_timestamps, 1) AS range_duration_ms",
		"(range_counter_delta_sum) / (range_duration_ms)",
	} {
		if !strings.Contains(rendered.SQL, expected) {
			t.Fatalf("expected %q in SQL, got %q", expected, rendered.SQL)
		}
	}
	if strings.Contains(rendered.SQL, "arrayPopBack(arrayMap(point -> ifNull(toFloat64(tupleElement(point, 2)), nan), time_series))") {
		t.Fatalf("expected instant rate SQL to reuse range_values_prev alias, got %q", rendered.SQL)
	}
}
