package promharness

import "testing"

func TestCompareBaselineDetectsStrategyChange(t *testing.T) {
	baseline := BenchReport{Rows: []BenchRow{{Name: "q", Endpoint: "query", Strategy: "native_sql", CHRoundtrips: 1, NativeP50MS: 10}}}
	current := BenchReport{Rows: []BenchRow{{Name: "q", Endpoint: "query", Strategy: "local", CHRoundtrips: 1, NativeP50MS: 10}}}
	regs := CompareBaseline(current, baseline)
	if len(regs) != 1 || regs[0].Kind != RegressionStrategyChange {
		t.Fatalf("expected one strategy_change regression, got %+v", regs)
	}
	if regs[0].Before != "native_sql" || regs[0].After != "local" {
		t.Fatalf("unexpected before/after: %+v", regs[0])
	}
}

func TestCompareBaselineDetectsCHRoundtripIncrease(t *testing.T) {
	baseline := BenchReport{Rows: []BenchRow{{Name: "q", Endpoint: "query", Strategy: "native_sql", CHRoundtrips: 1, NativeP50MS: 10}}}
	current := BenchReport{Rows: []BenchRow{{Name: "q", Endpoint: "query", Strategy: "native_sql", CHRoundtrips: 2, NativeP50MS: 10}}}
	regs := CompareBaseline(current, baseline)
	if len(regs) != 1 || regs[0].Kind != RegressionCHRoundtrips {
		t.Fatalf("expected one ch_roundtrips regression, got %+v", regs)
	}
}

func TestCompareBaselineCHRoundtripDecreaseIsWin(t *testing.T) {
	// Decrease in round-trips is a *win*, not a regression.
	baseline := BenchReport{Rows: []BenchRow{{Name: "q", Endpoint: "query", Strategy: "native_sql", CHRoundtrips: 5, NativeP50MS: 10}}}
	current := BenchReport{Rows: []BenchRow{{Name: "q", Endpoint: "query", Strategy: "native_sql", CHRoundtrips: 1, NativeP50MS: 10}}}
	if regs := CompareBaseline(current, baseline); len(regs) != 0 {
		t.Fatalf("decrease must not trigger regression, got %+v", regs)
	}
}

func TestCompareBaselineLatencyThreshold(t *testing.T) {
	baseline := BenchReport{Rows: []BenchRow{{Name: "q", Endpoint: "query", Strategy: "native_sql", CHRoundtrips: 1, NativeP50MS: 100}}}

	// 10% over — below percent threshold; no regression.
	if regs := CompareBaseline(BenchReport{Rows: []BenchRow{{Name: "q", Endpoint: "query", Strategy: "native_sql", CHRoundtrips: 1, NativeP50MS: 110}}}, baseline); len(regs) != 0 {
		t.Fatalf("10%% over must not regress, got %+v", regs)
	}

	// 30% over, >3ms absolute — regression.
	regs := CompareBaseline(BenchReport{Rows: []BenchRow{{Name: "q", Endpoint: "query", Strategy: "native_sql", CHRoundtrips: 1, NativeP50MS: 130}}}, baseline)
	if len(regs) != 1 || regs[0].Kind != RegressionLatency {
		t.Fatalf("expected one latency regression, got %+v", regs)
	}
}

func TestCompareBaselineLatencyNoiseFloor(t *testing.T) {
	// 50% relative change but tiny absolute: 2ms -> 3ms. The 3ms noise
	// floor must prevent this from counting.
	baseline := BenchReport{Rows: []BenchRow{{Name: "q", Endpoint: "query", Strategy: "native_sql", CHRoundtrips: 1, NativeP50MS: 2}}}
	current := BenchReport{Rows: []BenchRow{{Name: "q", Endpoint: "query", Strategy: "native_sql", CHRoundtrips: 1, NativeP50MS: 3}}}
	if regs := CompareBaseline(current, baseline); len(regs) != 0 {
		t.Fatalf("expected no regression under noise floor, got %+v", regs)
	}
}

func TestCompareBaselineStrategyFlap(t *testing.T) {
	baseline := BenchReport{Rows: []BenchRow{{Name: "q", Endpoint: "query", Strategy: "native_sql", CHRoundtrips: 1, NativeP50MS: 10}}}
	current := BenchReport{Rows: []BenchRow{{Name: "q", Endpoint: "query", Strategy: "native_sql", CHRoundtrips: 1, NativeP50MS: 10, StrategyFlap: true}}}
	regs := CompareBaseline(current, baseline)
	if len(regs) != 1 || regs[0].Kind != RegressionStrategyFlap {
		t.Fatalf("expected strategy_flap regression, got %+v", regs)
	}
}

func TestCompareBaselineTolerates_MissingBaseline(t *testing.T) {
	baseline := BenchReport{Rows: nil}
	current := BenchReport{Rows: []BenchRow{{Name: "q", Endpoint: "query", Strategy: "native_sql", CHRoundtrips: 3, NativeP50MS: 50}}}
	if regs := CompareBaseline(current, baseline); len(regs) != 0 {
		t.Fatalf("new corpus entries must be tolerated, got %+v", regs)
	}
}

func TestCompareBaselineEndpointDistinguishesRows(t *testing.T) {
	// Same name, different endpoint must not collide.
	baseline := BenchReport{Rows: []BenchRow{
		{Name: "q", Endpoint: "query", Strategy: "native_sql", CHRoundtrips: 1},
		{Name: "q", Endpoint: "query_range", Strategy: "local", CHRoundtrips: 20},
	}}
	current := BenchReport{Rows: []BenchRow{
		{Name: "q", Endpoint: "query", Strategy: "native_sql", CHRoundtrips: 1},
		{Name: "q", Endpoint: "query_range", Strategy: "local", CHRoundtrips: 20},
	}}
	if regs := CompareBaseline(current, baseline); len(regs) != 0 {
		t.Fatalf("no regressions expected, got %+v", regs)
	}
}
