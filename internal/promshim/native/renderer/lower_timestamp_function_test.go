package renderer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// timestampFuncCases locks the instant lowering of the timestamp()
// function. Prometheus special-cases timestamp(<vector selector>) to
// return the selected sample's real time, while the emitted row
// timestamp of every instant vector is the evaluation time. The
// selector therefore exposes the sample time as a dedicated
// sample_timestamp column and the folded value template compiles
// against it — never against the (evaluation-time) timestamp column.
var timestampFuncCases = []struct {
	name  string
	query string
}{
	{name: "timestamp_up", query: `timestamp(up)`},
	{name: "timestamp_up_offset", query: `timestamp(up offset 5m)`},
	{name: "sum_timestamp_up", query: `sum(timestamp(up))`},
}

// TestLowerTimestampFunctionGolden locks the exact instant SQL for
// timestamp() shapes. Run with -update to regenerate golden files.
func TestLowerTimestampFunctionGolden(t *testing.T) {
	for _, tc := range timestampFuncCases {
		t.Run(tc.name+"_instant", func(t *testing.T) {
			root, analysis, nativeAnalysis := buildLowerInputs(t, tc.query)
			rq, err := Lower(LoweringCtx{
				Config:         testRenderConfig(),
				Analysis:       analysis,
				NativeAnalysis: nativeAnalysis,
				Params:         testRenderParamsInstant(),
			}, root)
			if err != nil {
				t.Fatalf("Lower: %v", err)
			}
			goldenPath := filepath.Join("testdata", "lower_timestamp", tc.name+"_instant.sql")
			if *updateLowerGolden {
				if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
					t.Fatalf("mkdir testdata: %v", err)
				}
				if err := os.WriteFile(goldenPath, []byte(rq.SQL), 0o644); err != nil {
					t.Fatalf("write golden: %v", err)
				}
				return
			}
			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("read golden (run with -update to create): %v", err)
			}
			if string(want) != rq.SQL {
				t.Errorf("SQL differs from golden %s\nwant:\n%s\ngot:\n%s", goldenPath, want, rq.SQL)
			}
		})
	}
}

// TestLowerTimestampFunctionReadsSampleTimestamp asserts the semantic
// invariants directly: the timestamp() value reads the real sample time
// (sample_timestamp), the selector emits that column, and the row
// timestamp column stays the evaluation time.
func TestLowerTimestampFunctionReadsSampleTimestamp(t *testing.T) {
	for _, tc := range timestampFuncCases {
		t.Run(tc.name, func(t *testing.T) {
			root, analysis, nativeAnalysis := buildLowerInputs(t, tc.query)
			rq, err := Lower(LoweringCtx{
				Config:         testRenderConfig(),
				Analysis:       analysis,
				NativeAnalysis: nativeAnalysis,
				Params:         testRenderParamsInstant(),
			}, root)
			if err != nil {
				t.Fatalf("Lower: %v", err)
			}
			if !strings.Contains(rq.SQL, "toFloat64(toUnixTimestamp64Milli(sample_timestamp)) / 1000.0 AS value") {
				t.Fatalf("expected timestamp() value to read sample_timestamp, got:\n%s", rq.SQL)
			}
			if !strings.Contains(rq.SQL, "max(d.timestamp) AS sample_timestamp") {
				t.Fatalf("expected selector to emit sample_timestamp column, got:\n%s", rq.SQL)
			}
			if !strings.Contains(rq.SQL, "fromUnixTimestamp64Milli(1700000000000) AS timestamp") {
				t.Fatalf("expected evaluation-time row timestamp column, got:\n%s", rq.SQL)
			}
			if strings.Contains(rq.SQL, "toUnixTimestamp64Milli(timestamp)") {
				t.Fatalf("expected no timestamp() read of the evaluation-time column, got:\n%s", rq.SQL)
			}
		})
	}
}
