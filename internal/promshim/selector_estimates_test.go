package promshim

import (
	"testing"
	"time"

	httpapi "github.com/BadLiveware/promshim-clickhouse/internal/promshim/httpapi"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
)

func TestSelectorSignatureMatcherOrderIsStable(t *testing.T) {
	left := selectorSignaturesForTest(t, `up{job="api",instance=~"a|b"}`)
	right := selectorSignaturesForTest(t, `up{instance=~"a|b",job="api"}`)
	if len(left) != 1 || len(right) != 1 {
		t.Fatalf("signature counts left=%d right=%d", len(left), len(right))
	}
	if left[0].key() != right[0].key() {
		t.Fatalf("signature keys differ for reordered matchers:\n%s\n%s", left[0].key(), right[0].key())
	}
}

func TestSelectorSignatureIncludesTimeBoundsLookbackAndOffset(t *testing.T) {
	plain := selectorSignaturesForTest(t, `up`)
	lookback := selectorSignaturesForTest(t, `rate(up[10m])`)
	offset := selectorSignaturesForTest(t, `up offset 1m`)
	if plain[0].key() == lookback[0].key() {
		t.Fatal("plain and lookback signatures must differ")
	}
	if plain[0].key() == offset[0].key() {
		t.Fatal("plain and offset signatures must differ")
	}
	if plain[0].LookbackMS != int64((5 * time.Minute).Milliseconds()) {
		t.Fatalf("plain lookback = %d", plain[0].LookbackMS)
	}
	if lookback[0].LookbackMS != int64((10 * time.Minute).Milliseconds()) {
		t.Fatalf("range lookback = %d", lookback[0].LookbackMS)
	}
	if offset[0].OffsetMS != int64(time.Minute.Milliseconds()) {
		t.Fatalf("offset = %d", offset[0].OffsetMS)
	}
}

func TestEstimateSamplesPerSeriesUsesExpandedSignatureSpanOnce(t *testing.T) {
	instant := time.Unix(300, 0)
	expr, err := logical.ParseExpression(`rate(up[6h])`)
	if err != nil {
		t.Fatal(err)
	}
	sigs := extractSelectorSignatures(expr, queryCostTiming{Endpoint: "query", Start: instant, End: instant})
	if len(sigs) != 1 {
		t.Fatalf("signature count = %d, want 1", len(sigs))
	}
	got := estimateSamplesPerSeries(sigs[0])
	want := int64((6*time.Hour).Milliseconds()/15000 + 1)
	if got != want {
		t.Fatalf("samples per series = %d, want %d (signature=%+v)", got, want, sigs[0])
	}
}

func TestApplyCachedSelectorEstimates(t *testing.T) {
	sigs := selectorSignaturesForTest(t, `up`)
	cache := newSelectorStatsCache(time.Minute)
	now := time.Unix(300, 0)
	cache.put(sigs[0], selectorStats{MatchedSeries: 3, SamplesPerSeries: 4, ObservedAt: now})
	expr, _ := logical.ParseExpression(`up`)
	class := classifyQueryCost(expr, queryCostTiming{Endpoint: "query", Start: now, End: now}, "local")
	class = applyCachedSelectorEstimates(class, sigs, cache, now)
	if class.EstimatedSeries != 3 || class.EstimatedInputSamples != 12 || class.EstimatedOutputPoints != 3 {
		t.Fatalf("unexpected estimates: %+v", class)
	}
	if class.EstimateState.Source != "cache" || !class.EstimateState.Fresh || class.EstimateState.GeneratedAt == "" || class.EstimateState.TTLSeconds != 60 {
		t.Fatalf("unexpected estimate state: %+v", class.EstimateState)
	}
}

func TestApplyCachedSelectorEstimatesRequiresAllSelectors(t *testing.T) {
	cache := newSelectorStatsCache(time.Minute)
	now := time.Now().UTC()
	sig1 := selectorSignature{Matchers: []string{`__name__="a"`}, StartMS: 0, EndMS: 60000}
	sig2 := selectorSignature{Matchers: []string{`__name__="b"`}, StartMS: 0, EndMS: 60000}
	cache.put(sig1, selectorStats{MatchedSeries: 2, SamplesPerSeries: 5, ObservedAt: now})
	class := applyCachedSelectorEstimates(httpapi.QueryCostClass{Endpoint: "query", Family: "selector"}, []selectorSignature{sig1, sig2}, cache, now)
	if class.EstimatedSeries != 0 || class.EstimatedInputSamples != 0 {
		t.Fatalf("partial cache should not populate estimates: %+v", class)
	}
	if class.EstimateState.Source != "cache" || class.EstimateState.Missing != 1 || class.EstimateState.Fresh {
		t.Fatalf("unexpected partial estimate state: %+v", class.EstimateState)
	}
}

func TestSelectorStatsCacheEvictsOldestAtCapacity(t *testing.T) {
	cache := newSelectorStatsCacheWithMax(time.Minute, 2)
	now := time.Unix(300, 0).UTC()
	sig1 := selectorSignature{Matchers: []string{`__name__="a"`}, StartMS: 0, EndMS: 60000}
	sig2 := selectorSignature{Matchers: []string{`__name__="b"`}, StartMS: 0, EndMS: 60000}
	sig3 := selectorSignature{Matchers: []string{`__name__="c"`}, StartMS: 0, EndMS: 60000}
	cache.put(sig1, selectorStats{MatchedSeries: 1, ObservedAt: now})
	cache.put(sig2, selectorStats{MatchedSeries: 2, ObservedAt: now.Add(time.Second)})
	cache.put(sig3, selectorStats{MatchedSeries: 3, ObservedAt: now.Add(2 * time.Second)})

	if cache.len() != 2 {
		t.Fatalf("cache len = %d, want 2", cache.len())
	}
	if _, ok := cache.get(sig1, now.Add(3*time.Second)); ok {
		t.Fatalf("oldest selector stats entry was not evicted")
	}
	if stats, ok := cache.get(sig2, now.Add(3*time.Second)); !ok || stats.MatchedSeries != 2 {
		t.Fatalf("expected second selector stats to remain, got %+v ok=%t", stats, ok)
	}
	if stats, ok := cache.get(sig3, now.Add(3*time.Second)); !ok || stats.MatchedSeries != 3 {
		t.Fatalf("expected newest selector stats to remain, got %+v ok=%t", stats, ok)
	}
}

func TestSelectorStatsCacheDropsStaleEntries(t *testing.T) {
	cache := newSelectorStatsCacheWithMax(time.Minute, 10)
	now := time.Unix(300, 0).UTC()
	stale := selectorSignature{Matchers: []string{`__name__="stale"`}, StartMS: 0, EndMS: 60000}
	fresh := selectorSignature{Matchers: []string{`__name__="fresh"`}, StartMS: 0, EndMS: 60000}
	cache.put(stale, selectorStats{MatchedSeries: 1, ObservedAt: now.Add(-2 * time.Minute)})
	cache.put(fresh, selectorStats{MatchedSeries: 2, ObservedAt: now})

	if cache.len() != 1 {
		t.Fatalf("cache len = %d, want only fresh entry", cache.len())
	}
	if _, ok := cache.get(stale, now); ok {
		t.Fatalf("stale selector stats entry should not remain readable")
	}
}

func TestApplyCachedSelectorEstimatesMarksStaleSelectors(t *testing.T) {
	cache := newSelectorStatsCache(time.Minute)
	now := time.Now().UTC()
	sig := selectorSignature{Matchers: []string{`__name__="a"`}, StartMS: 0, EndMS: 60000}
	cache.put(sig, selectorStats{MatchedSeries: 2, SamplesPerSeries: 5, ObservedAt: now.Add(-2 * time.Minute)})
	class := applyCachedSelectorEstimates(httpapi.QueryCostClass{Endpoint: "query", Family: "selector"}, []selectorSignature{sig}, cache, now)
	if class.EstimatedSeries != 0 || class.EstimateState.Stale != 1 || class.EstimateState.Fresh {
		t.Fatalf("stale cache should not populate estimates: %+v", class)
	}
}

func selectorSignaturesForTest(t *testing.T, query string) []selectorSignature {
	t.Helper()
	expr, err := logical.ParseExpression(query)
	if err != nil {
		t.Fatal(err)
	}
	sigs := extractSelectorSignatures(expr, queryCostTiming{Endpoint: "query", Start: time.Unix(300, 0), End: time.Unix(300, 0)})
	if len(sigs) == 0 {
		t.Fatalf("no signatures for %q", query)
	}
	return sigs
}
