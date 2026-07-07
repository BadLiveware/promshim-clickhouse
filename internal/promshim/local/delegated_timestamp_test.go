package local

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/model"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/storage"
)

// newDelegatedTestClient returns a storage client whose HTTP transport
// always answers with the provided JSONEachRow lines, emulating a
// ClickHouse prometheusQuery() response.
func newDelegatedTestClient(t *testing.T, rows ...string) *storage.Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		for _, row := range rows {
			_, _ = fmt.Fprintln(w, row)
		}
	}))
	t.Cleanup(server.Close)
	client, err := storage.NewClient(storage.Config{Endpoint: server.URL, RequestTimeout: time.Second})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	return client
}

func executeDelegatedInstant(t *testing.T, client *storage.Client, promQL string, evalTime time.Time) model.RuntimeValue {
	t.Helper()
	expr, err := logical.ParseExpression(promQL)
	if err != nil {
		t.Fatalf("parse expression: %v", err)
	}
	plan := &delegatedExprPlan{Expr: expr}
	value, err := plan.execute(context.Background(), &Evaluator{database: "observability", table: "prometheus", client: client}, EvalParams{Mode: EvalModeInstant, EvaluationTime: evalTime})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	return value
}

// Prometheus instant-vector semantics: the result timestamp is always
// the evaluation time; offset shifts sample selection only. ClickHouse
// prometheusQuery() has been observed (26.1) returning the shifted
// sample timestamp, so the delegated tier must normalize it.
func TestDelegatedInstantVectorWithOffsetNormalizesTimestampToEvaluationTime(t *testing.T) {
	// The endpoint reports the shifted sample time inside [t-5m-lookback, t-5m].
	client := newDelegatedTestClient(t, `{"tags":[["__name__","up"],["job","api"]],"timestamp":"2026-04-20 11:29:00.000","value":1}`)

	evalTime := time.Unix(1776857640, 0).UTC()
	value := executeDelegatedInstant(t, client, "up offset 5m", evalTime)
	vector, ok := value.(model.VectorValue)
	if !ok {
		t.Fatalf("expected vector result, got %T", value)
	}
	if len(vector.Samples) != 1 {
		t.Fatalf("expected one sample, got %#v", vector.Samples)
	}
	if got, want := vector.Samples[0].Timestamp, float64(evalTime.Unix()); got != want {
		t.Fatalf("expected evaluation timestamp %v, got %v", want, got)
	}
	if got := vector.Samples[0].Value; got != 1 {
		t.Fatalf("expected value untouched by normalization, got %v", got)
	}
}

func TestDelegatedInstantVectorWithAtModifierNormalizesTimestampToEvaluationTime(t *testing.T) {
	// The endpoint reports the @-anchored sample time, far from eval time.
	client := newDelegatedTestClient(t,
		`{"tags":[["__name__","up"],["job","api"]],"timestamp":"2026-04-20 11:00:00.000","value":1}`,
		`{"tags":[["__name__","up"],["job","worker"]],"timestamp":"2026-04-20 11:00:05.000","value":0}`,
	)

	evalTime := time.Unix(1776857640, 0).UTC()
	value := executeDelegatedInstant(t, client, "up @ 1776855600", evalTime)
	vector, ok := value.(model.VectorValue)
	if !ok {
		t.Fatalf("expected vector result, got %T", value)
	}
	if len(vector.Samples) != 2 {
		t.Fatalf("expected two samples, got %#v", vector.Samples)
	}
	for i, sample := range vector.Samples {
		if got, want := sample.Timestamp, float64(evalTime.Unix()); got != want {
			t.Fatalf("sample %d: expected evaluation timestamp %v, got %v", i, want, got)
		}
	}
	if vector.Samples[0].Value != 1 || vector.Samples[1].Value != 0 {
		t.Fatalf("expected values untouched by normalization, got %#v", vector.Samples)
	}
}

// A non-step-aligned evaluation time (sub-second precision) must be
// preserved exactly, not rounded to a sample or step boundary.
func TestDelegatedInstantVectorNormalizesToNonAlignedEvaluationTime(t *testing.T) {
	client := newDelegatedTestClient(t, `{"tags":[["__name__","up"],["job","api"]],"timestamp":"2026-04-20 11:29:00.000","value":1}`)

	evalTime := time.Unix(1776857642, 123000000).UTC()
	value := executeDelegatedInstant(t, client, "up offset 3m17s", evalTime)
	vector, ok := value.(model.VectorValue)
	if !ok {
		t.Fatalf("expected vector result, got %T", value)
	}
	if len(vector.Samples) != 1 {
		t.Fatalf("expected one sample, got %#v", vector.Samples)
	}
	want := float64(evalTime.UnixNano()) / float64(time.Second)
	if got := vector.Samples[0].Timestamp; got != want {
		t.Fatalf("expected fractional evaluation timestamp %v, got %v", want, got)
	}
	if seconds := int64(want); seconds != 1776857642 {
		t.Fatalf("expected evaluation time anchored at 1776857642s, got %d", seconds)
	}
}

// Range-vector selectors evaluated at an instant return a matrix whose
// point timestamps are the real sample times — normalization must not
// touch the delegated instant matrix arm.
func TestDelegatedInstantMatrixKeepsSampleTimestamps(t *testing.T) {
	client := newDelegatedTestClient(t, `{"tags":[["__name__","up"],["job","api"]],"time_series":[["2026-04-20 11:33:00.000",1],["2026-04-20 11:33:30.000",1]]}`)

	evalTime := time.Unix(1776857640, 0).UTC()
	value := executeDelegatedInstant(t, client, "up[5m]", evalTime)
	matrix, ok := value.(model.MatrixValue)
	if !ok {
		t.Fatalf("expected matrix result, got %T", value)
	}
	if len(matrix.Series) != 1 || len(matrix.Series[0].Values) != 2 {
		t.Fatalf("expected one series with two points, got %#v", matrix.Series)
	}
	for _, point := range matrix.Series[0].Values {
		if point.Timestamp == float64(evalTime.Unix()) {
			t.Fatalf("expected matrix point timestamps to keep sample times, got evaluation time %v", point.Timestamp)
		}
	}
}
