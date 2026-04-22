package native

type LabelLineageState string

const (
	LabelLineageUnknown   LabelLineageState = "unknown"
	LabelLineageOriginal  LabelLineageState = "original"
	LabelLineageCopied    LabelLineageState = "copied"
	LabelLineageMutated   LabelLineageState = "mutated"
	LabelLineageDropped   LabelLineageState = "dropped"
	LabelLineageSynthetic LabelLineageState = "synthetic"
)

type LabelLineage struct {
	Known      map[string]LabelLineageState
	Wildcard   LabelLineageState
	MetricName LabelLineageState
}

func leafLabelLineage() LabelLineage {
	return LabelLineage{
		Known:      map[string]LabelLineageState{"__name__": LabelLineageOriginal},
		Wildcard:   LabelLineageOriginal,
		MetricName: LabelLineageOriginal,
	}
}

func cloneLabelLineage(lineage LabelLineage) LabelLineage {
	cloned := LabelLineage{
		Known:      map[string]LabelLineageState{},
		Wildcard:   lineage.Wildcard,
		MetricName: lineage.MetricName,
	}
	for key, value := range lineage.Known {
		cloned.Known[key] = value
	}
	return cloned
}

func passthroughLabelLineage(child LabelLineage) LabelLineage {
	return cloneLabelLineage(child)
}

func withMetricNameState(child LabelLineage, state LabelLineageState) LabelLineage {
	updated := cloneLabelLineage(child)
	updated.MetricName = state
	updated.Known["__name__"] = state
	return updated
}

func mutateDestinationLabel(child LabelLineage, destination string, state LabelLineageState) LabelLineage {
	updated := cloneLabelLineage(child)
	if destination == "" {
		return updated
	}
	updated.Known[destination] = state
	if destination == "__name__" {
		updated.MetricName = state
	}
	return updated
}

func countValuesLabelLineage(child LabelLineage, grouping []string, without bool, valueLabel string) LabelLineage {
	mutated := mutateDestinationLabel(child, valueLabel, LabelLineageSynthetic)
	effectiveGrouping := append([]string(nil), grouping...)
	if !without {
		found := false
		for _, label := range effectiveGrouping {
			if label == valueLabel {
				found = true
				break
			}
		}
		if !found {
			effectiveGrouping = append(effectiveGrouping, valueLabel)
		}
	}
	return aggregationLabelLineage(mutated, effectiveGrouping, without)
}

func aggregationLabelLineage(child LabelLineage, grouping []string, without bool) LabelLineage {
	result := LabelLineage{
		Known:      map[string]LabelLineageState{"__name__": LabelLineageDropped},
		MetricName: LabelLineageDropped,
	}
	if without {
		result.Wildcard = child.Wildcard
		for key, state := range child.Known {
			if key == "__name__" {
				continue
			}
			result.Known[key] = state
		}
		for _, label := range grouping {
			result.Known[label] = LabelLineageDropped
		}
		return result
	}
	result.Wildcard = LabelLineageDropped
	for _, label := range grouping {
		if state, ok := child.Known[label]; ok {
			result.Known[label] = state
			continue
		}
		if child.Wildcard != "" {
			result.Known[label] = child.Wildcard
			continue
		}
		result.Known[label] = LabelLineageUnknown
	}
	return result
}

func syntheticOutputLineage(labels map[string]string) LabelLineage {
	result := LabelLineage{
		Known:      map[string]LabelLineageState{"__name__": LabelLineageDropped},
		Wildcard:   LabelLineageDropped,
		MetricName: LabelLineageDropped,
	}
	for label := range labels {
		result.Known[label] = LabelLineageSynthetic
	}
	return result
}

func (lineage LabelLineage) explain() *ExplainLabelLineage {
	if len(lineage.Known) == 0 && lineage.Wildcard == "" && lineage.MetricName == "" {
		return nil
	}
	explain := &ExplainLabelLineage{Known: map[string]string{}}
	for key, value := range lineage.Known {
		explain.Known[key] = string(value)
	}
	if len(explain.Known) == 0 {
		explain.Known = nil
	}
	if lineage.Wildcard != "" {
		explain.Wildcard = string(lineage.Wildcard)
	}
	if lineage.MetricName != "" {
		explain.MetricName = string(lineage.MetricName)
	}
	return explain
}
