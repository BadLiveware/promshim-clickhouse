package exec

import (
	"math"
	"strings"
	"testing"

	"ch-observability/internal/promshim/model"
	commonmodel "github.com/prometheus/common/model"
	promlabels "github.com/prometheus/prometheus/model/labels"
)

func TestApplyInfoMergesLabelsAndPreservesValue(t *testing.T) {
	base := model.VectorValue{Samples: []model.InstantSample{{Metric: map[string]string{commonmodel.MetricNameLabel: "http_requests_total", "job": "api", "instance": "a"}, Timestamp: 10, Value: 7}}}
	info := model.VectorValue{Samples: []model.InstantSample{{Metric: map[string]string{commonmodel.MetricNameLabel: "target_info", "job": "api", "instance": "a", "k8s_cluster_name": "prod"}, Timestamp: 10, Value: 1}}}

	out, err := ApplyInfo(base, info, nil)
	if err != nil {
		t.Fatalf("expected info() output, got error: %v", err)
	}
	if len(out.Samples) != 1 || out.Samples[0].Value != 7 {
		t.Fatalf("unexpected info() output: %#v", out.Samples)
	}
	if out.Samples[0].Metric[commonmodel.MetricNameLabel] != "http_requests_total" || out.Samples[0].Metric["k8s_cluster_name"] != "prod" {
		t.Fatalf("expected metric name preserved and info label merged, got %#v", out.Samples[0].Metric)
	}
}

func TestApplyInfoKeepsBaseLabelOnConflict(t *testing.T) {
	base := model.VectorValue{Samples: []model.InstantSample{{Metric: map[string]string{commonmodel.MetricNameLabel: "http_requests_total", "job": "api", "instance": "a", "cluster": "base"}, Timestamp: 10, Value: 7}}}
	info := model.VectorValue{Samples: []model.InstantSample{{Metric: map[string]string{commonmodel.MetricNameLabel: "target_info", "job": "api", "instance": "a", "cluster": "info", "region": "eu"}, Timestamp: 10, Value: 1}}}

	out, err := ApplyInfo(base, info, nil)
	if err != nil {
		t.Fatalf("expected info() output, got error: %v", err)
	}
	if out.Samples[0].Metric["cluster"] != "base" || out.Samples[0].Metric["region"] != "eu" {
		t.Fatalf("unexpected conflict handling: %#v", out.Samples[0].Metric)
	}
}

func TestApplyInfoHandlesAbsentAndFilteredInfo(t *testing.T) {
	base := model.VectorValue{Samples: []model.InstantSample{{Metric: map[string]string{commonmodel.MetricNameLabel: "http_requests_total", "job": "api", "instance": "a"}, Timestamp: 10, Value: 7}}}
	out, err := ApplyInfo(base, model.VectorValue{}, nil)
	if err != nil {
		t.Fatalf("expected passthrough output, got error: %v", err)
	}
	if len(out.Samples) != 1 || out.Samples[0].Metric[commonmodel.MetricNameLabel] != "http_requests_total" {
		t.Fatalf("unexpected passthrough output: %#v", out.Samples)
	}
	selector := []*promlabels.Matcher{promlabels.MustNewMatcher(promlabels.MatchEqual, "k8s_cluster_name", "prod")}
	out, err = ApplyInfo(base, model.VectorValue{}, selector)
	if err != nil {
		t.Fatalf("expected filtered output, got error: %v", err)
	}
	if len(out.Samples) != 0 {
		t.Fatalf("expected selector-mismatched output to be dropped, got %#v", out.Samples)
	}
}

func TestApplyInfoRejectsDuplicateInfoSeries(t *testing.T) {
	base := model.VectorValue{Samples: []model.InstantSample{{Metric: map[string]string{commonmodel.MetricNameLabel: "http_requests_total", "job": "api", "instance": "a"}, Timestamp: 10, Value: 7}}}
	info := model.VectorValue{Samples: []model.InstantSample{
		{Metric: map[string]string{commonmodel.MetricNameLabel: "target_info", "job": "api", "instance": "a", "cluster": "prod"}, Timestamp: 10, Value: 1},
		{Metric: map[string]string{commonmodel.MetricNameLabel: "target_info", "job": "api", "instance": "a", "cluster": "staging"}, Timestamp: 10, Value: 1},
	}}

	if _, err := ApplyInfo(base, info, nil); err == nil || !strings.Contains(err.Error(), "duplicate series") {
		t.Fatalf("expected duplicate info series error, got %v", err)
	}
}

func TestBuildInfoFetchExprStringSupportsDefaultAndNegativeNameMatchers(t *testing.T) {
	base := model.VectorValue{Samples: []model.InstantSample{{Metric: map[string]string{"job": "api", "instance": "a"}, Timestamp: 10, Value: 1}}}
	query, err := BuildInfoFetchExprString(base, nil)
	if err != nil {
		t.Fatalf("expected fetch query, got error: %v", err)
	}
	checks := []string{"__name__=\"target_info\"", "instance=~\"a\"", "job=~\"api\""}
	for _, check := range checks {
		if !strings.Contains(query, check) {
			t.Fatalf("expected query %q to contain %q", query, check)
		}
	}
	negative := []*promlabels.Matcher{promlabels.MustNewMatcher(promlabels.MatchNotEqual, commonmodel.MetricNameLabel, "target_info")}
	query, err = BuildInfoFetchExprString(base, negative)
	if err != nil {
		t.Fatalf("expected negative-name fetch query, got error: %v", err)
	}
	if !strings.Contains(query, "__name__=~\".+_info\"") {
		t.Fatalf("expected synthetic _info matcher, got %q", query)
	}
}

func TestApplyInfoIgnoresInfoSeriesInputAndPreservesNaNValue(t *testing.T) {
	base := model.VectorValue{Samples: []model.InstantSample{{Metric: map[string]string{commonmodel.MetricNameLabel: "target_info", "job": "api", "instance": "a"}, Timestamp: 10, Value: math.NaN()}}}
	info := model.VectorValue{Samples: []model.InstantSample{{Metric: map[string]string{commonmodel.MetricNameLabel: "target_info", "job": "api", "instance": "a", "cluster": "prod"}, Timestamp: 10, Value: 1}}}
	out, err := ApplyInfo(base, info, nil)
	if err != nil {
		t.Fatalf("expected passthrough for info-series input, got error: %v", err)
	}
	if len(out.Samples) != 1 || !math.IsNaN(out.Samples[0].Value) || out.Samples[0].Metric["cluster"] != "" {
		t.Fatalf("unexpected info-series passthrough output: %#v", out.Samples)
	}
}
