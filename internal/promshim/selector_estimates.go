package promshim

import (
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	httpapi "github.com/BadLiveware/promshim-clickhouse/internal/promshim/httpapi"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/routingmetrics"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

type selectorSignature struct {
	Matchers   []string
	StartMS    int64
	EndMS      int64
	LookbackMS int64
	OffsetMS   int64
}

type selectorStats struct {
	MatchedSeries    int64
	SamplesPerSeries int64
	ObservedAt       time.Time
}

const defaultSelectorStatsMaxEntries = 4096

type selectorStatsCache struct {
	mu         sync.Mutex
	ttl        time.Duration
	maxEntries int
	entries    map[string]selectorStats
}

func newSelectorStatsCache(ttl time.Duration) *selectorStatsCache {
	return newSelectorStatsCacheWithMax(ttl, defaultSelectorStatsMaxEntries)
}

func newSelectorStatsCacheWithMax(ttl time.Duration, maxEntries int) *selectorStatsCache {
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	if maxEntries <= 0 {
		maxEntries = defaultSelectorStatsMaxEntries
	}
	return &selectorStatsCache{ttl: ttl, maxEntries: maxEntries, entries: map[string]selectorStats{}}
}

func (c *selectorStatsCache) get(sig selectorSignature, now time.Time) (selectorStats, bool) {
	stats, state := c.getWithState(sig, now)
	return stats, state == "hit"
}

func (c *selectorStatsCache) getWithState(sig selectorSignature, now time.Time) (selectorStats, string) {
	if c == nil {
		return selectorStats{}, "missing"
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	key := sig.key()
	stats, ok := c.entries[key]
	if !ok || stats.ObservedAt.IsZero() {
		return selectorStats{}, "missing"
	}
	if now.Sub(stats.ObservedAt) > c.ttl {
		delete(c.entries, key)
		return stats, "stale"
	}
	return stats, "hit"
}

func (c *selectorStatsCache) put(sig selectorSignature, stats selectorStats) {
	if c == nil {
		return
	}
	if stats.ObservedAt.IsZero() {
		stats.ObservedAt = time.Now().UTC()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := stats.ObservedAt
	c.pruneLocked(now)
	key := sig.key()
	if _, exists := c.entries[key]; !exists {
		c.evictToCapacityLocked(c.maxEntries - 1)
	}
	c.entries[key] = stats
}

func (c *selectorStatsCache) len() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

func (c *selectorStatsCache) pruneLocked(now time.Time) {
	if c.ttl <= 0 {
		return
	}
	for key, stats := range c.entries {
		if stats.ObservedAt.IsZero() || now.Sub(stats.ObservedAt) > c.ttl {
			delete(c.entries, key)
		}
	}
}

func (c *selectorStatsCache) evictToCapacityLocked(target int) {
	for len(c.entries) > target {
		oldestKey := ""
		var oldest time.Time
		for key, stats := range c.entries {
			if oldestKey == "" || stats.ObservedAt.Before(oldest) {
				oldestKey = key
				oldest = stats.ObservedAt
			}
		}
		if oldestKey == "" {
			return
		}
		delete(c.entries, oldestKey)
	}
}

func (sig selectorSignature) key() string {
	parts := append([]string(nil), sig.Matchers...)
	sort.Strings(parts)
	return strings.Join(parts, "\xff") + "|" + int64Key(sig.StartMS) + "|" + int64Key(sig.EndMS) + "|" + int64Key(sig.LookbackMS) + "|" + int64Key(sig.OffsetMS)
}

func int64Key(v int64) string { return strconv.FormatInt(v, 10) }

func applyCachedSelectorEstimates(class httpapi.QueryCostClass, signatures []selectorSignature, cache *selectorStatsCache, now time.Time) httpapi.QueryCostClass {
	class.EstimateState = httpapi.EstimateState{Source: "none", Fresh: len(signatures) == 0, SelectorCount: len(signatures)}
	if len(signatures) == 0 {
		return class
	}
	if cache == nil {
		class.EstimateState.Missing = len(signatures)
		return class
	}
	var matchedSeries, inputSamples int64
	var latest time.Time
	missing, stale, hits := 0, 0, 0
	allFresh := true
	for _, sig := range signatures {
		stats, state := cache.getWithState(sig, now)
		switch state {
		case "hit":
			routingmetrics.ObserveEstimateCache(class.Family, "cache", "hit")
			hits++
			if stats.ObservedAt.After(latest) {
				latest = stats.ObservedAt
			}
		case "stale":
			routingmetrics.ObserveEstimateCache(class.Family, "cache", "stale")
			stale++
			allFresh = false
			continue
		default:
			routingmetrics.ObserveEstimateCache(class.Family, "none", "miss")
			missing++
			allFresh = false
			continue
		}
		matchedSeries += stats.MatchedSeries
		samplesPerSeries := stats.SamplesPerSeries
		if samplesPerSeries <= 0 {
			samplesPerSeries = estimateSamplesPerSeries(sig)
		}
		inputSamples += stats.MatchedSeries * samplesPerSeries
	}
	if cache.ttl > 0 {
		class.EstimateState.TTLSeconds = int64(cache.ttl.Seconds())
	}
	class.EstimateState.Missing = missing
	class.EstimateState.Stale = stale
	if !allFresh {
		if stale > 0 || hits > 0 {
			class.EstimateState.Source = "cache"
		}
		if !latest.IsZero() {
			class.EstimateState.GeneratedAt = latest.UTC().Format(time.RFC3339Nano)
		}
		return class
	}
	class.EstimateState.Source = "cache"
	class.EstimateState.Fresh = true
	class.EstimateState.GeneratedAt = latest.UTC().Format(time.RFC3339Nano)
	class.EstimatedSeries = matchedSeries
	class.EstimatedInputSamples = inputSamples
	if class.EstimatedOutputPoints == 0 {
		if class.RangePointsPerSeries > 0 {
			class.EstimatedOutputPoints = matchedSeries * class.RangePointsPerSeries
		} else {
			class.EstimatedOutputPoints = matchedSeries
		}
	}
	return class
}

func estimateSamplesPerSeries(sig selectorSignature) int64 {
	start, end := sig.StartMS, sig.EndMS
	if end < start {
		return 1
	}
	span := end - start
	if span <= 0 {
		return 1
	}
	// Signature bounds are already expanded by lookback and offset, so estimate
	// over the exact probed time span instead of adding lookback a second time.
	// A conservative fallback until optional scrape-interval estimates land.
	const defaultScrapeIntervalMS = int64(15000)
	return span/defaultScrapeIntervalMS + 1
}

func extractSelectorSignatures(expr parser.Expr, timing queryCostTiming) []selectorSignature {
	var out []selectorSignature
	walkSelectorSignatures(expr, timing, 0, 0, &out)
	return out
}

func walkSelectorSignatures(expr parser.Expr, timing queryCostTiming, inheritedLookback, inheritedOffset time.Duration, out *[]selectorSignature) {
	if expr == nil {
		return
	}
	switch node := expr.(type) {
	case *parser.VectorSelector:
		lookback := inheritedLookback + logical.DefaultInstantSelectorLookback
		offset := inheritedOffset + node.OriginalOffset
		*out = append(*out, newSelectorSignature(node.LabelMatchers, timing, lookback, offset))
	case *parser.MatrixSelector:
		if vector, ok := node.VectorSelector.(*parser.VectorSelector); ok {
			lookback := inheritedLookback + node.Range
			offset := inheritedOffset + vector.OriginalOffset
			*out = append(*out, newSelectorSignature(vector.LabelMatchers, timing, lookback, offset))
		}
	case *parser.Call:
		for _, arg := range node.Args {
			walkSelectorSignatures(arg, timing, inheritedLookback, inheritedOffset, out)
		}
	case *parser.AggregateExpr:
		walkSelectorSignatures(node.Expr, timing, inheritedLookback, inheritedOffset, out)
	case *parser.BinaryExpr:
		walkSelectorSignatures(node.LHS, timing, inheritedLookback, inheritedOffset, out)
		walkSelectorSignatures(node.RHS, timing, inheritedLookback, inheritedOffset, out)
	case *parser.SubqueryExpr:
		walkSelectorSignatures(node.Expr, timing, inheritedLookback+node.Range, inheritedOffset, out)
	case *parser.ParenExpr:
		walkSelectorSignatures(node.Expr, timing, inheritedLookback, inheritedOffset, out)
	case *parser.UnaryExpr:
		walkSelectorSignatures(node.Expr, timing, inheritedLookback, inheritedOffset, out)
	}
}

func newSelectorSignature(matchers []*labels.Matcher, timing queryCostTiming, lookback, offset time.Duration) selectorSignature {
	matcherStrings := make([]string, 0, len(matchers))
	for _, matcher := range matchers {
		if matcher != nil {
			matcherStrings = append(matcherStrings, matcher.String())
		}
	}
	sort.Strings(matcherStrings)
	start := timing.Start.Add(-offset).UnixMilli()
	end := timing.End.Add(-offset).UnixMilli()
	if lookback > 0 {
		start -= lookback.Milliseconds()
	}
	return selectorSignature{Matchers: matcherStrings, StartMS: start, EndMS: end, LookbackMS: lookback.Milliseconds(), OffsetMS: offset.Milliseconds()}
}
