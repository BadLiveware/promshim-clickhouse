package promshim

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/BadLiveware/promshim-ch/internal/promshim/model"
	nativeplan "github.com/BadLiveware/promshim-ch/internal/promshim/native"
	"github.com/BadLiveware/promshim-ch/internal/promshim/storage"
)

func TestNativeSubtreePlanNormalizesInstantVectorTimestampToEvaluationTime(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"tags":[["job","api"]],"timestamp":"2026-04-20 11:34:00.000","value":1}`)
	}))
	defer server.Close()

	client, err := storage.NewClient(storage.Config{Endpoint: server.URL, RequestTimeout: time.Second})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	plan := &nativeSubtreePlan{
		Kind: "leaf",
		Expr: "up",
		Fragment: &nativeplan.NativeFragment{
			Kind:       nativeplan.FragmentKindLeafSource,
			OutputKind: nativeplan.OutputKindInstantVector,
			Selector:   &nativeplan.SelectorSource{Kind: nativeplan.SelectorKindInstantVector, MetricName: "up", Lookback: 5 * time.Minute},
			ValueExpr:  "{value}",
			TagsExpr:   "{tags}",
		},
		OptimizationReport: &nativeplan.OptimizationReport{RequiredInputStartMS: 0, RequiredInputEndMS: 0},
	}

	evalTime := time.Unix(1234, 0).UTC()
	value, err := plan.execute(context.Background(), &evaluator{opts: Options{Database: "observability", Table: "prometheus"}, client: client}, evalParams{Mode: evalModeInstant, EvaluationTime: evalTime})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
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
}
