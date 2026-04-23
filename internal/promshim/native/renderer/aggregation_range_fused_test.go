package renderer

import (
	"strings"
	"testing"
	"time"

	"github.com/BadLiveware/promshim-ch/internal/promshim/native"
	"github.com/BadLiveware/promshim-ch/internal/promshim/storage"
	"github.com/prometheus/prometheus/promql/parser"
)

func TestRenderFragmentFusesRangeSumRateAggregation(t *testing.T) {
	fragment := &native.NativeFragment{
		Kind:       native.FragmentKindAggregation,
		OutputKind: native.OutputKindRangeMatrix,
		Aggregation: &native.AggregationFragment{
			Op:       parser.SUM,
			Grouping: []string{"job"},
			Source: &native.NativeFragment{
				Kind:       native.FragmentKindRangeFunction,
				OutputKind: native.OutputKindInstantVector,
				RangeFunction: &native.RangeFunctionFragment{
					Func: "rate",
					Child: &native.NativeFragment{
						Kind:       native.FragmentKindLeafSource,
						OutputKind: native.OutputKindRangeMatrix,
						Selector: &native.SelectorSource{
							Kind:              native.SelectorKindRangeVector,
							MetricName:        "demo_cpu_usage_seconds_total",
							RequireFullTags:   false,
							RequiredTagLabels: []string{"job"},
							Lookback:          5 * time.Minute,
						},
						ValueExpr: "{value}",
						TagsExpr:  "{tags}",
					},
				},
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
	for _, expected := range []string{
		"arraySort(item -> item.1, groupArray((timestamp, value))) AS time_series",
		"counter_delta_sum",
		"sum(value)",
		"arrayFilter(tag -> has(['job'], tag.1), tags) AS tags",
	} {
		if !strings.Contains(rendered.SQL, expected) {
			t.Fatalf("expected %q in SQL, got %q", expected, rendered.SQL)
		}
	}
	if got := strings.Count(rendered.SQL, "GROUP BY tags, timestamp"); got != 1 {
		t.Fatalf("expected fused range sum(rate) SQL to aggregate by tags,timestamp exactly once, got count=%d sql=%q", got, rendered.SQL)
	}
	if strings.Contains(rendered.SQL, "ARRAY JOIN time_series AS point") {
		t.Fatalf("expected fused range sum(rate) SQL to avoid time_series ARRAY JOIN, got %q", rendered.SQL)
	}
	if strings.Contains(rendered.SQL, "arrayFilter(tag -> tag.1 != '__name__', tags)") {
		t.Fatalf("expected fused range sum(rate) SQL to reuse already metric-name-free selector tags, got %q", rendered.SQL)
	}
}
