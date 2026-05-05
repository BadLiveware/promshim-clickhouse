package renderer

import (
	"strings"
	"testing"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/native"
)

func TestLowerMaterializedLeafInstantGroupsByProjectedTags(t *testing.T) {
	root, analysis, nativeAnalysis := buildLowerInputs(t, `count:up0`)
	rq, err := Lower(LoweringCtx{
		Config:         testRenderConfig(),
		Analysis:       analysis,
		NativeAnalysis: nativeAnalysis,
		Params: RenderParams{
			Mode:             native.RenderModeInstant,
			EvaluationTimeMS: 1778004770639,
			RequiredStartMS:  1778004470639,
			RequiredEndMS:    1778004770639,
			ResolveTableOverride: func(metricName string) string {
				if metricName == "count:up0" {
					return "promshim_rules"
				}
				return ""
			},
		},
	}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	normalized := strings.Join(strings.Fields(rq.SQL), " ")
	for _, want := range []string{
		"FROM `observability`.`promshim_rules`",
		"arrayFilter(tag -> tag.1 != '__name__', tags) AS materialized_tags",
		"argMax(value, timestamp) AS value",
		"GROUP BY materialized_tags",
		"SELECT materialized_tags AS tags",
	} {
		if !strings.Contains(normalized, want) {
			t.Fatalf("lowered SQL missing %q:\n%s", want, rq.SQL)
		}
	}
	if strings.Contains(normalized, "any(value) AS value") {
		t.Fatalf("lowered SQL still uses non-grouped any(value):\n%s", rq.SQL)
	}
}

func TestLowerMaterializedLeafRangeUsesProjectedTagAlias(t *testing.T) {
	root, analysis, nativeAnalysis := buildLowerInputs(t, `count:up0`)
	rq, err := Lower(LoweringCtx{
		Config:         testRenderConfig(),
		Analysis:       analysis,
		NativeAnalysis: nativeAnalysis,
		Params: RenderParams{
			Mode:            native.RenderModeRange,
			StartMS:         1778004470639,
			EndMS:           1778004770639,
			StepMS:          60000,
			RequiredStartMS: 1778004470639,
			RequiredEndMS:   1778004770639,
			ResolveTableOverride: func(metricName string) string {
				if metricName == "count:up0" {
					return "promshim_rules"
				}
				return ""
			},
		},
	}, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	normalized := strings.Join(strings.Fields(rq.SQL), " ")
	for _, want := range []string{
		"FROM `observability`.`promshim_rules`",
		"arrayFilter(tag -> tag.1 != '__name__', tags) AS materialized_tags",
		"SELECT materialized_tags AS tags, timestamp, value",
	} {
		if !strings.Contains(normalized, want) {
			t.Fatalf("lowered SQL missing %q:\n%s", want, rq.SQL)
		}
	}
}
