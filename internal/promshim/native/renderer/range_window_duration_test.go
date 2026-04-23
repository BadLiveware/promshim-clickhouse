package renderer

import (
	"strings"
	"testing"
	"time"

	"github.com/BadLiveware/promshim-ch/internal/promshim/native"
	"github.com/BadLiveware/promshim-ch/internal/promshim/storage"
)

func TestRenderFragmentBuildsRangeRateSQLWithWindowDurationAliases(t *testing.T) {
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
	if !strings.Contains(rendered.SQL, "tupleElement(arrayElement(window_series, length(window_series)), 1) - tupleElement(arrayElement(window_series, 1), 1) AS window_duration_ms") {
		t.Fatalf("expected rate SQL to compute window_duration_ms directly from window_series, got %q", rendered.SQL)
	}
	if strings.Contains(rendered.SQL, "window_timestamps") {
		t.Fatalf("expected rate SQL to skip unused window_timestamps alias on the matrix-window path, got %q", rendered.SQL)
	}
	if strings.Contains(rendered.SQL, "changes_count") {
		t.Fatalf("expected rate SQL to skip unused changes_count alias on the matrix-window path, got %q", rendered.SQL)
	}
	if got := strings.Count(rendered.SQL, "tupleElement(arrayElement(window_series, length(window_series)), 1) - tupleElement(arrayElement(window_series, 1), 1)"); got != 1 {
		t.Fatalf("expected rate SQL to emit the raw duration expression once via the window_duration_ms alias, got count=%d sql=%q", got, rendered.SQL)
	}
	if !strings.Contains(rendered.SQL, "(counter_delta_sum) / (window_duration_ms)") {
		t.Fatalf("expected rate SQL to divide by window_duration_ms, got %q", rendered.SQL)
	}
}
