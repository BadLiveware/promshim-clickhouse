package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunStreamCancelsInFlightWorkersOnWriteError(t *testing.T) {
	var requests atomic.Int32
	secondStarted := make(chan struct{})
	unblockServer := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idx := requests.Add(1)
		switch idx {
		case 1:
			select {
			case <-secondStarted:
			case <-time.After(2 * time.Second):
			}
			http.Error(w, "forced remote-write failure", http.StatusInternalServerError)
		case 2:
			close(secondStarted)
			select {
			case <-r.Context().Done():
			case <-unblockServer:
			}
		default:
			select {
			case <-r.Context().Done():
			case <-unblockServer:
			}
		}
	}))
	defer func() {
		close(unblockServer)
		server.Close()
	}()

	start := time.Unix(0, 0).UTC()
	series := []seriesDesc{{
		labels: sortedLabels(map[string]string{"__name__": "demo_gauge", "job": "api"}),
		kind:   "gauge",
		base:   1,
	}}
	runStarted := time.Now()
	_, err := runStream(context.Background(), streamConfig{
		Endpoint:           server.URL,
		StartTime:          start,
		EndTime:            start.Add(10 * time.Second),
		Step:               time.Second,
		BatchSamples:       1,
		Series:             series,
		State:              make([]seriesState, len(series)),
		MaxConcurrency:     2,
		InitialConcurrency: 2,
		NoAdaptive:         true,
	})
	if err == nil || !strings.Contains(err.Error(), "seed batch failed") {
		t.Fatalf("expected fatal seed batch error, got %v", err)
	}
	if elapsed := time.Since(runStarted); elapsed >= 2*time.Second {
		t.Fatalf("expected write error to cancel in-flight workers promptly, elapsed %s", elapsed)
	}
	select {
	case <-secondStarted:
	default:
		t.Fatalf("expected second request to be in flight before first request failed")
	}
}
