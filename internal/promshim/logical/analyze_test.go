package logical

import (
	"testing"
)

func mustToLogical(t *testing.T, q string) Node {
	t.Helper()
	expr, err := ParseExpression(q)
	if err != nil {
		t.Fatalf("parse %q: %v", q, err)
	}
	node, err := ToLogical(expr)
	if err != nil {
		t.Fatalf("ToLogical %q: %v", q, err)
	}
	return node
}

func TestAnalyzeLeafHasInstantDomainAndNoGrouping(t *testing.T) {
	root := mustToLogical(t, `up{job="api"}`)
	a := Analyze(root)
	info := a.Info[root]
	if info == nil {
		t.Fatal("no info for leaf")
	}
	if info.TimeDomain != DomainInstant {
		t.Errorf("leaf TimeDomain = %v, want DomainInstant", info.TimeDomain)
	}
	if len(info.GroupingKey.Labels) != 0 {
		t.Errorf("leaf GroupingKey should be empty, got %v", info.GroupingKey.Labels)
	}
	if info.DropsMetric {
		t.Error("leaf should not drop __name__")
	}
	if _, ok := info.Schema.Guaranteed["job"]; !ok {
		t.Error("leaf selector up{job=\"api\"} should guarantee job")
	}
	if _, ok := info.Schema.Guaranteed["__name__"]; !ok {
		t.Error("leaf selector up{...} should guarantee __name__")
	}
}

func TestAnalyzeSelectorRegexpMatcherIsPossibleOnly(t *testing.T) {
	root := mustToLogical(t, `up{job=~"api.*"}`)
	a := Analyze(root)
	info := a.Info[root]
	if _, guaranteed := info.Schema.Guaranteed["job"]; guaranteed {
		t.Error("regex matcher should not make job Guaranteed")
	}
	if _, possible := info.Schema.Possible["job"]; !possible {
		t.Error("regex matcher should make job Possible")
	}
}

func TestAnalyzeSelectorEmptyEqualityIsAbsent(t *testing.T) {
	root := mustToLogical(t, `up{job=""}`)
	a := Analyze(root)
	info := a.Info[root]
	if _, ok := info.Schema.Possible["job"]; ok {
		t.Error("job=\"\" means label-absent; should not appear in schema")
	}
}

func TestAnalyzeScalarLiteralDomain(t *testing.T) {
	root := mustToLogical(t, `42`)
	a := Analyze(root)
	info := a.Info[root]
	if info.TimeDomain != DomainScalar {
		t.Errorf("literal TimeDomain = %v, want DomainScalar", info.TimeDomain)
	}
	if !info.DropsMetric {
		t.Error("scalar literal should drop __name__")
	}
}

func TestAnalyzeUnaryPropagatesFromChild(t *testing.T) {
	root := mustToLogical(t, `-up{job="api"}`)
	a := Analyze(root)
	info := a.Info[root]
	if info.TimeDomain != DomainInstant {
		t.Errorf("unary over instant = %v, want DomainInstant", info.TimeDomain)
	}
	if _, ok := info.Schema.Guaranteed["job"]; !ok {
		t.Error("unary should propagate child schema")
	}
}

func TestAnalyzeBinaryVectorScalarKeepsVectorDomain(t *testing.T) {
	root := mustToLogical(t, `up{job="api"} * 2`)
	a := Analyze(root)
	info := a.Info[root]
	if info.TimeDomain != DomainInstant {
		t.Errorf("vector*scalar TimeDomain = %v, want DomainInstant", info.TimeDomain)
	}
	if !info.DropsMetric {
		t.Error("arithmetic should drop __name__")
	}
	if _, ok := info.Schema.Guaranteed["job"]; !ok {
		t.Error("binary over (vector, scalar) should carry vector's Guaranteed labels")
	}
}

func TestAnalyzeBinaryScalarScalarStaysScalar(t *testing.T) {
	root := mustToLogical(t, `1 + 2`)
	a := Analyze(root)
	info := a.Info[root]
	if info.TimeDomain != DomainScalar {
		t.Errorf("scalar+scalar TimeDomain = %v, want DomainScalar", info.TimeDomain)
	}
}

func TestAnalyzeAggregationBySetsGroupingKey(t *testing.T) {
	root := mustToLogical(t, `sum by (job) (up)`)
	a := Analyze(root)
	info := a.Info[root]
	if info == nil {
		t.Fatal("no info for aggregation")
	}
	if info.GroupingKey.Without {
		t.Error("sum by(job) should have Without=false")
	}
	if got := info.GroupingKey.Labels; len(got) != 1 || got[0] != "job" {
		t.Errorf("GroupingKey.Labels = %v, want [job]", got)
	}
	if !info.DropsMetric {
		t.Error("aggregations drop __name__")
	}
	if _, ok := info.Schema.Possible["job"]; !ok {
		t.Error("sum by(job) output schema should list job as Possible")
	}
}

func TestAnalyzeAggregationWithoutRemovesGroupingLabels(t *testing.T) {
	root := mustToLogical(t, `sum without (instance) (up{job="api",instance="a"})`)
	a := Analyze(root)
	info := a.Info[root]
	if !info.GroupingKey.Without {
		t.Error("sum without(instance) should set Without=true")
	}
	if _, ok := info.Schema.Possible["instance"]; ok {
		t.Error("sum without(instance) should remove instance from schema")
	}
	if _, ok := info.Schema.Guaranteed["job"]; !ok {
		t.Error("sum without(instance) should keep job Guaranteed")
	}
}

func TestAnalyzeCountValuesAddsDestinationLabel(t *testing.T) {
	root := mustToLogical(t, `count_values("x", up)`)
	a := Analyze(root)
	info := a.Info[root]
	if _, ok := info.Schema.Guaranteed["x"]; !ok {
		t.Error("count_values(\"x\", ...) should list x as Guaranteed")
	}
}

func TestAnalyzeRangeFunctionHasInstantDomainAndLookback(t *testing.T) {
	root := mustToLogical(t, `rate(up[5m])`)
	a := Analyze(root)
	info := a.Info[root]
	if info.TimeDomain != DomainInstant {
		t.Errorf("rate TimeDomain = %v, want DomainInstant", info.TimeDomain)
	}
	if info.TimeRequirements.Lookback.Seconds() != 300 {
		t.Errorf("rate lookback = %v, want 5m", info.TimeRequirements.Lookback)
	}
	if !info.DropsMetric {
		t.Error("rate should drop __name__")
	}
}

func TestAnalyzeRangeFunctionPreservingNameKeepsMetric(t *testing.T) {
	root := mustToLogical(t, `avg_over_time(up[5m])`)
	a := Analyze(root)
	info := a.Info[root]
	if info.DropsMetric {
		t.Error("avg_over_time preserves __name__; DropsMetric should be false")
	}
}

func TestAnalyzeSubqueryChildHasRangeDomainAndAccumulatesRange(t *testing.T) {
	root := mustToLogical(t, `max_over_time(rate(up[5m])[10m:1m])`)
	a := Analyze(root)
	info := a.Info[root]
	if info.TimeDomain != DomainInstant {
		t.Errorf("max_over_time TimeDomain = %v, want DomainInstant", info.TimeDomain)
	}
	// Walk into the range function → subquery child; subquery must be DomainRange.
	var subqueryInfo *NodeInfo
	switch rf := root.(type) {
	case *RangeFunctionPlan:
		subqueryInfo = a.Info[rf.Child]
	}
	if subqueryInfo == nil {
		t.Fatal("no info for subquery child")
	}
	if subqueryInfo.TimeDomain != DomainRange {
		t.Errorf("subquery TimeDomain = %v, want DomainRange", subqueryInfo.TimeDomain)
	}
	if subqueryInfo.TimeRequirements.Lookback < 10*60*1000*1000*1000 {
		// lookback must include at least the subquery range (10m = 600s = 600e9 ns)
		t.Errorf("subquery lookback = %v, want >= 10m", subqueryInfo.TimeRequirements.Lookback)
	}
	if !subqueryInfo.TimeRequirements.NeedsSubqueryStepGrid {
		t.Error("subquery should set NeedsSubqueryStepGrid")
	}
}

func TestAnalyzeLabelReplaceAddsPossibleLabel(t *testing.T) {
	root := mustToLogical(t, `label_replace(up, "target", "${1}", "instance", "(.*):.*")`)
	a := Analyze(root)
	info := a.Info[root]
	if _, ok := info.Schema.Possible["target"]; !ok {
		t.Error("label_replace should list the destination label as Possible")
	}
	if _, ok := info.Schema.Guaranteed["target"]; ok {
		t.Error("label_replace output label must NOT be Guaranteed (regex may not match)")
	}
}

func TestAnalyzeLabelJoinAddsPossibleLabel(t *testing.T) {
	root := mustToLogical(t, `label_join(up, "combined", "-", "job", "instance")`)
	a := Analyze(root)
	info := a.Info[root]
	if _, ok := info.Schema.Possible["combined"]; !ok {
		t.Error("label_join should list the destination label as Possible")
	}
}

func TestAnalyzeHistogramQuantileOverAggregationByLeHasInstantDomain(t *testing.T) {
	root := mustToLogical(t, `histogram_quantile(0.9, sum by (le) (rate(foo_bucket[5m])))`)
	a := Analyze(root)
	info := a.Info[root]
	if info.TimeDomain != DomainInstant {
		t.Errorf("histogram_quantile TimeDomain = %v, want DomainInstant", info.TimeDomain)
	}
	if !info.DropsMetric {
		t.Error("histogram_quantile drops __name__")
	}
}

func TestAnalyzeRootSchemaInvariant(t *testing.T) {
	// Guaranteed ⊆ Possible must hold on every visited node for
	// several representative shapes.
	shapes := []string{
		`up{job="api"}`,
		`sum by (job) (up)`,
		`rate(up[5m])`,
		`label_replace(up, "x", "${1}", "instance", "(.*):.*")`,
	}
	for _, shape := range shapes {
		root := mustToLogical(t, shape)
		a := Analyze(root)
		for node, info := range a.Info {
			if info == nil {
				continue
			}
			for label := range info.Schema.Guaranteed {
				if _, ok := info.Schema.Possible[label]; !ok {
					t.Errorf("query %q: node %T label %q is Guaranteed but not Possible", shape, node, label)
				}
			}
		}
	}
}
