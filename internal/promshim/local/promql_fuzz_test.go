package local

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
)

func FuzzPromQLPlanningModes(f *testing.F) {
	for _, seed := range promQLFuzzSeeds(f) {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, query string) {
		expr, err := logical.ParseExpression(query)
		if err != nil {
			t.Skip()
		}
		for _, tc := range []struct {
			name string
			ctx  PlanContext
		}{
			{name: "instant/off", ctx: fuzzPlanContext(EvalModeInstant, NativeLoweringModeOff)},
			{name: "instant/prefer", ctx: fuzzPlanContext(EvalModeInstant, NativeLoweringModePrefer)},
			{name: "instant/explain", ctx: fuzzPlanContext(EvalModeInstant, NativeLoweringModeExplain)},
			{name: "instant/shadow", ctx: fuzzPlanContext(EvalModeInstant, NativeLoweringModeShadow)},
			{name: "instant/local_pushdown", ctx: fuzzPlanContext(EvalModeInstant, NativeLoweringModeLocalPushdown)},
			{name: "instant/force_supported", ctx: fuzzPlanContext(EvalModeInstant, NativeLoweringModeForceSupported)},
			{name: "range/off", ctx: fuzzPlanContext(EvalModeRange, NativeLoweringModeOff)},
			{name: "range/prefer", ctx: fuzzPlanContext(EvalModeRange, NativeLoweringModePrefer)},
			{name: "range/explain", ctx: fuzzPlanContext(EvalModeRange, NativeLoweringModeExplain)},
			{name: "range/shadow", ctx: fuzzPlanContext(EvalModeRange, NativeLoweringModeShadow)},
			{name: "range/local_pushdown", ctx: fuzzPlanContext(EvalModeRange, NativeLoweringModeLocalPushdown)},
			{name: "range/force_supported", ctx: fuzzPlanContext(EvalModeRange, NativeLoweringModeForceSupported)},
		} {
			plan, err := buildPlanWithContext(expr, tc.ctx)
			if err != nil {
				assertFuzzPlanningError(t, tc.name, query, err)
				continue
			}
			if plan == nil {
				t.Fatalf("%s built nil plan for %q", tc.name, query)
			}
			_ = plan.explain()
		}
	})
}

func promQLFuzzSeeds(f *testing.F) []string {
	f.Helper()
	seeds := []string{
		`up`,
		`up{job="api"}`,
		`rate(http_requests_total[5m])`,
		`sum by (job) (rate(http_requests_total[5m]))`,
		`sum by (job, type) (avg_over_time(demo_memory_usage_bytes[1h]))`,
		`histogram_quantile(0.9, sum by (le, job) (rate(http_request_duration_seconds_bucket[5m])))`,
		`label_replace(up, "dst", "$1", "job", "(.*)")`,
		`absent_over_time(up{job=~"api|web"}[10m])`,
		`predict_linear(demo_disk_free_bytes[1h], 3600)`,
		`(sum(rate(a_total[5m])) by (job)) / ignoring(instance) group_left sum(rate(b_total[5m])) by (job)`,
	}
	for _, path := range promQLCorpusFiles(f) {
		if strings.HasSuffix(path, ".metadata.json") {
			continue
		}
		seeds = append(seeds, promQLQueriesFromCorpusFile(f, path)...)
	}
	return seeds
}

type promQLCorpusEntry struct {
	Query string `json:"query"`
}

func promQLCorpusFiles(f *testing.F) []string {
	f.Helper()
	paths, err := filepath.Glob(filepath.Join("..", "..", "..", "harness", "corpus", "*.json"))
	if err != nil {
		f.Fatalf("glob benchmark corpus files: %v", err)
	}
	if len(paths) == 0 {
		f.Fatalf("expected benchmark corpus files for PromQL fuzz seeds")
	}
	return paths
}

func promQLQueriesFromCorpusFile(f *testing.F, path string) []string {
	f.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		f.Fatalf("read benchmark corpus %s: %v", path, err)
	}
	var entries []promQLCorpusEntry
	if err := json.Unmarshal(contents, &entries); err != nil {
		f.Fatalf("parse benchmark corpus %s: %v", path, err)
	}
	queries := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Query != "" {
			queries = append(queries, entry.Query)
		}
	}
	return queries
}

func fuzzPlanContext(evalMode EvalMode, nativeMode NativeLoweringMode) PlanContext {
	ctx := DefaultPlanContext(evalMode)
	ctx.NativeLoweringMode = nativeMode
	ctx.PreferNativeAggregationPushdown = nativeMode.EnablesNativePlanning()
	ctx.EnableNativeGridFunctions = true
	ctx.EnableCumulativeAvgOverTime = true
	ctx.EvaluationTime = time.Unix(300, 0).UTC()
	ctx.Start = time.Unix(0, 0).UTC()
	ctx.End = time.Unix(300, 0).UTC()
	ctx.Step = time.Minute
	ctx.MaxRangePointsPerSeries = 10_000
	ctx.RangeChunkPointsPerSeries = 0
	ctx.NativeRangeChunkPointsPerSeries = -1
	ctx.NativeRangeChunkMaxDuration = 24 * time.Hour
	ctx.NativeRangeChunkMaxChunks = 4
	ctx.NativeRangePreflightSeriesThreshold = 1000
	ctx.NativeRangePreflightTimeout = 10 * time.Millisecond
	ctx.NativeRangePreflightMaxMemoryUsage = 1 << 20
	return ctx
}

func assertFuzzPlanningError(t *testing.T, mode, query string, err error) {
	t.Helper()
	var internal internalError
	if errors.As(err, &internal) {
		switch internal.Kind() {
		case internalErrorKindUnsupported, internalErrorKindBadData:
			return
		}
	}
	t.Fatalf("%s returned internal planning error for %q: %v", mode, query, err)
}
