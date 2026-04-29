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

func HistogramPreparationDecision(functionName, renderMode string, leOnly bool) Decision {
	strategy := "classic_histogram_preparation"
	guards := []string{"histogram_preparation", "function=" + functionName, "mode=" + renderMode}
	if leOnly {
		strategy = "classic_histogram_preparation_le_only"
		guards = append(guards, "le_only_tags")
	}
	return Decision{
		Kind:     "histogram_preparation_shape",
		Strategy: strategy,
		Reason:   "histogram function prepares classic histogram rows before final calculation",
		Guards:   guards,
	}
}

func HistogramNativeGridRowsDecision() Decision {
	return Decision{
		Kind:     "histogram_native_grid_rows_shape",
		Strategy: "late_series_join",
		Reason:   "histogram preparation computes native-grid rows per id before joining tags",
		Guards:   []string{"histogram_preparation", "le_only_tags", "native_grid_rows"},
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
		reason := threads.ReasonCode
		if reason == "" {
			reason = "preserve_no_cap"
		}
		return Decision{
			Kind:     "query_settings",
			Strategy: "no_thread_cap",
			Reason:   reason,
			Guards:   []string{"preserve_no_cap"},
			Rejected: []Alternative{{
				Strategy: "set_max_threads",
				Reason:   "suppressed by no-thread-cap preference",
			}},
		}, true
	default:
		return Decision{}, false
	}
}
