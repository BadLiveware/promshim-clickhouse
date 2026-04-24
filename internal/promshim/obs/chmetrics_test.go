package obs

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestWithCHMetricsAttaches(t *testing.T) {
	ctx, m := WithCHMetrics(context.Background())
	if m == nil {
		t.Fatal("WithCHMetrics returned nil pointer")
	}
	if got := FromContext(ctx); got != m {
		t.Fatalf("FromContext returned %p, want %p", got, m)
	}
}

func TestWithCHMetricsIsIdempotent(t *testing.T) {
	parent, first := WithCHMetrics(context.Background())
	child, second := WithCHMetrics(parent)
	if first != second {
		t.Fatal("nested WithCHMetrics must inherit the parent's CHMetrics")
	}
	second.Observe(5 * time.Millisecond)
	if FromContext(child).Roundtrips() != 1 {
		t.Fatal("observation on inherited metrics did not propagate to parent")
	}
}

func TestFromContextNilSafe(t *testing.T) {
	if got := FromContext(context.Background()); got != nil {
		t.Fatalf("FromContext without attachment returned %p, want nil", got)
	}
	// nil receiver must not panic.
	var m *CHMetrics
	m.Observe(10 * time.Millisecond)
	if m.Roundtrips() != 0 || m.Millis() != 0 {
		t.Fatal("nil receiver must report zero counters")
	}
}

func TestObserveCounts(t *testing.T) {
	m := &CHMetrics{}
	m.Observe(4 * time.Millisecond)
	m.Observe(6 * time.Millisecond)
	if m.Roundtrips() != 2 {
		t.Fatalf("roundtrips = %d, want 2", m.Roundtrips())
	}
	if m.Millis() != 10 {
		t.Fatalf("millis = %d, want 10", m.Millis())
	}
}

func TestObserveConcurrent(t *testing.T) {
	m := &CHMetrics{}
	var wg sync.WaitGroup
	const goroutines = 50
	const per = 100
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < per; j++ {
				m.Observe(1 * time.Millisecond)
			}
		}()
	}
	wg.Wait()
	if m.Roundtrips() != goroutines*per {
		t.Fatalf("roundtrips = %d, want %d", m.Roundtrips(), goroutines*per)
	}
	if m.Millis() != int64(goroutines*per) {
		t.Fatalf("millis = %d, want %d", m.Millis(), goroutines*per)
	}
}
