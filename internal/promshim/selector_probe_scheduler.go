package promshim

import (
	"context"
	"time"

	httpapi "github.com/BadLiveware/promshim-clickhouse/internal/promshim/httpapi"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
)

func (h *queryService) maybeScheduleSelectorStatsProbes(query string, timing queryCostTiming, policy RoutingPolicy, class httpapi.QueryCostClass) {
	if h == nil || policy != RoutingPolicyCostShadow || class.SelectorCount == 0 || class.EstimatedSeries > 0 {
		return
	}
	expr, err := logical.ParseExpression(query)
	if err != nil {
		return
	}
	for _, sig := range extractSelectorSignatures(expr, timing) {
		if _, ok := h.selectorStats.get(sig, time.Now().UTC()); ok {
			continue
		}
		h.scheduleSelectorStatsProbe(sig)
	}
}

func (h *queryService) scheduleSelectorStatsProbe(sig selectorSignature) {
	if h == nil || h.selectorProbeSem == nil {
		return
	}
	select {
	case h.selectorProbeSem <- struct{}{}:
	default:
		return
	}
	go func() {
		defer func() { <-h.selectorProbeSem }()
		stats, err := h.probeSelectorStats(context.Background(), sig, 250*time.Millisecond)
		if err != nil {
			return
		}
		h.selectorStats.put(sig, stats)
	}()
}
