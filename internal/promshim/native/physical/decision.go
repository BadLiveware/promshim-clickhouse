package physical

func (d RangeInstantSelectorDecision) Explain(kind string) Decision {
	return Decision{
		Kind:     kind,
		Strategy: string(d.Strategy),
		Reason:   d.Reason,
		Guards:   append([]string(nil), d.Guards...),
		Rejected: append([]Alternative(nil), d.Rejected...),
	}
}

func (d RangeWindowAggregateDecision) Explain(kind string) Decision {
	strategy := string(d.Strategy)
	if strategy == "" {
		strategy = "windowed_arrays"
	}
	return Decision{
		Kind:     kind,
		Strategy: strategy,
		Reason:   d.Reason,
		Guards:   append([]string(nil), d.Guards...),
		Rejected: append([]Alternative(nil), d.Rejected...),
	}
}

func (d FusedRangeAggregationDecision) Explain(kind string) Decision {
	strategy := string(d.Strategy)
	if strategy == "" {
		strategy = "row_oriented_aggregation"
	}
	return Decision{
		Kind:     kind,
		Strategy: strategy,
		Reason:   d.Reason,
		Guards:   append([]string(nil), d.Guards...),
		Rejected: append([]Alternative(nil), d.Rejected...),
	}
}

func (d RangeFunctionRowsDecision) Explain(kind string) Decision {
	return Decision{
		Kind:     kind,
		Strategy: string(d.Strategy),
		Reason:   d.Reason,
		Guards:   append([]string(nil), d.Guards...),
		Rejected: append([]Alternative(nil), d.Rejected...),
	}
}

func NativeGridRangeFunctionDecision(kind string) Decision {
	return Decision{
		Kind:     kind,
		Strategy: "native_grid",
		Reason:   "native-grid range function is eligible",
		Guards:   []string{"identity_selector_input", "native_grid_enabled", "native_grid_range_function"},
	}
}

func ThreadPreferenceDecision(threads ThreadPreference) (Decision, bool) {
	switch threads.Mode {
	case ThreadPreferenceSet:
		if threads.MaxThreads <= 0 {
			return Decision{}, false
		}
		return Decision{
			Kind:     "query_settings",
			Strategy: "set_max_threads",
			Reason:   threads.ReasonCode,
			Guards:   []string{string(threads.Policy)},
		}, true
	case ThreadPreferenceNoCap:
		return Decision{
			Kind:     "query_settings",
			Strategy: "no_thread_cap",
			Reason:   threads.ReasonCode,
			Guards:   []string{"preserve_no_cap"},
		}, true
	default:
		return Decision{}, false
	}
}
