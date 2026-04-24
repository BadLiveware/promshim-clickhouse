package promharness

import "fmt"

type Regression struct {
	Query  string `json:"query"`
	Kind   string `json:"kind"`
	Before string `json:"before"`
	After  string `json:"after"`
	Detail string `json:"detail"`
}

const (
	RegressionStrategyChange = "strategy_change"
	RegressionStrategyFlap   = "strategy_flap"
	RegressionCHRoundtrips   = "ch_roundtrips"
	RegressionLatency        = "latency"
)

// LatencyThresholdPercent is the relative threshold for a latency
// regression; 0.25 = 25%.
const LatencyThresholdPercent = 0.25

// LatencyThresholdMillis is the absolute noise floor in ms. A latency
// regression must exceed *both* thresholds to count. Keeps low-latency
// queries (2ms bumping to 3ms) from tripping the gate.
const LatencyThresholdMillis = 3.0

// CompareBaseline returns any regressions between a baseline and current
// report. Ordered by query position in the current report, then by kind.
// Rules (all independent; a query can trip several):
//
//   - strategy_change   : current row's Strategy differs from baseline's
//   - strategy_flap     : current row flapped strategy across repeats (orthogonal
//     to baseline — if the shim is non-deterministic in path
//     choice, that alone is a bug)
//   - ch_roundtrips     : current row's CHRoundtrips > baseline's (strict; any
//     increase is signal)
//   - latency           : current p50 > baseline p50 * (1 + LatencyThresholdPercent)
//     AND (current p50 - baseline p50) > LatencyThresholdMillis
func CompareBaseline(current, baseline BenchReport) []Regression {
	byKey := map[string]BenchRow{}
	for _, row := range baseline.Rows {
		byKey[rowKey(row)] = row
	}
	var regressions []Regression
	for _, row := range current.Rows {
		if row.StrategyFlap {
			regressions = append(regressions, Regression{
				Query:  row.Name,
				Kind:   RegressionStrategyFlap,
				Before: "",
				After:  row.Strategy,
				Detail: "strategy differed across repeats",
			})
		}
		base, ok := byKey[rowKey(row)]
		if !ok {
			// New corpus entry — tolerate silently. It becomes part of the
			// baseline on the next --update-baseline run.
			continue
		}
		if row.Strategy != base.Strategy {
			regressions = append(regressions, Regression{
				Query:  row.Name,
				Kind:   RegressionStrategyChange,
				Before: base.Strategy,
				After:  row.Strategy,
				Detail: "root plan strategy changed",
			})
		}
		if row.CHRoundtrips > base.CHRoundtrips {
			regressions = append(regressions, Regression{
				Query:  row.Name,
				Kind:   RegressionCHRoundtrips,
				Before: fmt.Sprintf("%d", base.CHRoundtrips),
				After:  fmt.Sprintf("%d", row.CHRoundtrips),
				Detail: "more ClickHouse round-trips than baseline",
			})
		}
		if isLatencyRegression(base.NativeP50MS, row.NativeP50MS) {
			regressions = append(regressions, Regression{
				Query:  row.Name,
				Kind:   RegressionLatency,
				Before: fmt.Sprintf("%.2fms", base.NativeP50MS),
				After:  fmt.Sprintf("%.2fms", row.NativeP50MS),
				Detail: fmt.Sprintf(">+%.0f%% and >+%.0fms over baseline native p50", LatencyThresholdPercent*100, LatencyThresholdMillis),
			})
		}
	}
	return regressions
}

func isLatencyRegression(base, current float64) bool {
	if base <= 0 {
		// Baseline never got a measurement — can't infer a regression.
		return false
	}
	if current <= base*(1+LatencyThresholdPercent) {
		return false
	}
	if current-base <= LatencyThresholdMillis {
		return false
	}
	return true
}

func rowKey(row BenchRow) string {
	return row.Name + "\x00" + row.Endpoint
}
