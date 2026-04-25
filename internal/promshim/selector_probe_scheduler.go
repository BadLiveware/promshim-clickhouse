package promshim

import (
	"context"
	"time"

	httpapi "github.com/BadLiveware/promshim-clickhouse/internal/promshim/httpapi"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/routingmetrics"
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
		h.scheduleSelectorStatsProbe(class.Family, sig)
	}
}

func (h *queryService) scheduleSelectorStatsProbe(family string, sig selectorSignature) {
	if h == nil || h.selectorProbeSem == nil {
		return
	}
	select {
	case h.selectorProbeSem <- struct{}{}:
		routingmetrics.ObserveStatsProbe(family, "scheduled")
	default:
		routingmetrics.ObserveStatsProbe(family, "dropped")
		return
	}
	go func() {
		defer func() { <-h.selectorProbeSem }()
		stats, err := h.probeSelectorStats(context.Background(), sig, 250*time.Millisecond)
		if err != nil {
			routingmetrics.ObserveStatsProbe(family, "failed")
			return
		}
		routingmetrics.ObserveStatsProbe(family, "completed")
		h.selectorStats.put(sig, stats)
	}()
}
