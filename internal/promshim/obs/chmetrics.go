// Package obs carries per-request observation counters through the request
// context so the HTTP layer can surface them as response headers. It lives in
// its own package to avoid import cycles between httpapi (which writes the
// headers), storage (which observes CH round-trips), and local/native (which
// might observe in the future).
package obs

import (
	"context"
	"sync/atomic"
	"time"
)

type CHMetrics struct {
	roundtrips atomic.Int64
	millis     atomic.Int64
}

type ctxKey struct{}

// WithCHMetrics returns a derived context carrying a fresh *CHMetrics, plus
// that pointer so the caller can read it after the request completes. If the
// parent already carries a CHMetrics, it is inherited instead of shadowed —
// this keeps counters consistent across nested contexts (e.g. a subquery
// evaluator opening its own ctx.WithTimeout).
func WithCHMetrics(ctx context.Context) (context.Context, *CHMetrics) {
	if existing := FromContext(ctx); existing != nil {
		return ctx, existing
	}
	m := &CHMetrics{}
	return context.WithValue(ctx, ctxKey{}, m), m
}

// FromContext returns the CHMetrics attached to ctx, or nil if none is set.
// Callers should nil-check before observing; a nil receiver on Observe is
// a no-op so this is safe to call unguarded for code paths that sometimes
// run outside a CHMetrics-bearing request (tests, internal tools).
func FromContext(ctx context.Context) *CHMetrics {
	if ctx == nil {
		return nil
	}
	v, _ := ctx.Value(ctxKey{}).(*CHMetrics)
	return v
}

// Observe records one CH round-trip of the given duration. Safe on a nil
// receiver.
func (m *CHMetrics) Observe(d time.Duration) {
	if m == nil {
		return
	}
	m.roundtrips.Add(1)
	m.millis.Add(d.Milliseconds())
}

// Roundtrips returns the current count. Safe on a nil receiver (returns 0).
func (m *CHMetrics) Roundtrips() int64 {
	if m == nil {
		return 0
	}
	return m.roundtrips.Load()
}

// Millis returns the total observed wall-clock (ms). Safe on a nil receiver.
func (m *CHMetrics) Millis() int64 {
	if m == nil {
		return 0
	}
	return m.millis.Load()
}
