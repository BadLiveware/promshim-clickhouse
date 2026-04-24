package renderer

import (
	"testing"

	logicalpkg "github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
	"github.com/prometheus/prometheus/promql/parser"
)

func TestDecideHistogramChildNarrowing_sumByLe(t *testing.T) {
	child := &logicalpkg.AggregationPlan{
		Op:       parser.SUM,
		Grouping: []string{"le"},
		Without:  false,
	}
	requireFull, labels := decideHistogramChildNarrowing(child)
	if requireFull {
		t.Fatalf("expected requireFullTags=false for sum by (le), got true")
	}
	if len(labels) != 1 || labels[0] != "le" {
		t.Fatalf("expected labels=[le], got %v", labels)
	}
}

func TestDecideHistogramChildNarrowing_sumByLeAndJob(t *testing.T) {
	child := &logicalpkg.AggregationPlan{
		Op:       parser.SUM,
		Grouping: []string{"le", "job"},
		Without:  false,
	}
	requireFull, labels := decideHistogramChildNarrowing(child)
	if requireFull {
		t.Fatalf("expected requireFullTags=false for sum by (le,job), got true")
	}
	if len(labels) != 2 || labels[0] != "le" || labels[1] != "job" {
		t.Fatalf("expected labels=[le,job], got %v", labels)
	}
}

func TestDecideHistogramChildNarrowing_sumWithout(t *testing.T) {
	child := &logicalpkg.AggregationPlan{
		Op:       parser.SUM,
		Grouping: []string{"instance"},
		Without:  true,
	}
	requireFull, labels := decideHistogramChildNarrowing(child)
	if !requireFull {
		t.Fatalf("expected requireFullTags=true for sum without (instance), got false")
	}
	if labels != nil {
		t.Fatalf("expected labels=nil, got %v", labels)
	}
}

func TestDecideHistogramChildNarrowing_nilChild(t *testing.T) {
	requireFull, labels := decideHistogramChildNarrowing(nil)
	if !requireFull {
		t.Fatalf("expected requireFullTags=true for nil child")
	}
	if labels != nil {
		t.Fatalf("expected labels=nil, got %v", labels)
	}
}

func TestChildAggregationUsesOnlyLETags(t *testing.T) {
	cases := []struct {
		name     string
		plan     *logicalpkg.AggregationPlan
		expected bool
	}{
		{"sum by (le)", &logicalpkg.AggregationPlan{Op: parser.SUM, Grouping: []string{"le"}, Without: false}, true},
		{"sum by (le, job)", &logicalpkg.AggregationPlan{Op: parser.SUM, Grouping: []string{"le", "job"}, Without: false}, false},
		{"sum without (le)", &logicalpkg.AggregationPlan{Op: parser.SUM, Grouping: []string{"le"}, Without: true}, false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := childAggregationUsesOnlyLETags(tc.plan); got != tc.expected {
				t.Fatalf("got %v, want %v", got, tc.expected)
			}
		})
	}
}
