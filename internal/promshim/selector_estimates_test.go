package promshim

import (
	"testing"
	"time"

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
	lookback := selectorSignaturesForTest(t, `rate(up[5m])`)
	offset := selectorSignaturesForTest(t, `up offset 1m`)
	if plain[0].key() == lookback[0].key() {
		t.Fatal("plain and lookback signatures must differ")
	}
	if plain[0].key() == offset[0].key() {
		t.Fatal("plain and offset signatures must differ")
	}
	if lookback[0].LookbackMS != int64((5 * time.Minute).Milliseconds()) {
		t.Fatalf("lookback = %d", lookback[0].LookbackMS)
	}
	if offset[0].OffsetMS != int64(time.Minute.Milliseconds()) {
		t.Fatalf("offset = %d", offset[0].OffsetMS)
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
