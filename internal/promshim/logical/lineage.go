package logical

// Lineage state wire values used in NodeInfo.LabelLineage.Known and
// Wildcard. The enum LabelLineageState declared in nodeinfo.go is the
// canonical form for node-level propagation; the string values below
// are what ends up on the per-label map and are stable across the
// analysis boundary.
const (
	lineageStateOriginal  = "original"
	lineageStateMutated   = "mutated"
	lineageStateDropped   = "dropped"
	lineageStateUnknown   = "unknown"
	lineageStateSynthetic = "synthetic"
)

// LineageStateString returns the wire string for a LabelLineageState.
// The enum is lossy when converted to string via plain type
// conversion, so we map it explicitly here.
func LineageStateString(state LabelLineageState) string {
	switch state {
	case LabelLineagePassthrough:
		return lineageStateOriginal
	case LabelLineagePreserved:
		return lineageStateOriginal
	case LabelLineageDropped:
		return lineageStateDropped
	case LabelLineageReplaced:
		return lineageStateMutated
	}
	return lineageStateUnknown
}

// leafLineage is the default lineage for a selector-backed leaf:
// __name__ and all other labels come directly from storage.
func leafLineage() LabelLineage {
	return LabelLineage{
		Known:      map[string]string{"__name__": lineageStateOriginal},
		Wildcard:   lineageStateOriginal,
		MetricName: LabelLineagePassthrough,
	}
}

// unknownLineage marks the label set as unknowable — useful for
// vector-vector joins and nodes where the logical walk cannot model
// the result schema.
func unknownLineage() LabelLineage {
	return LabelLineage{
		Known:      map[string]string{"__name__": lineageStateUnknown},
		Wildcard:   lineageStateUnknown,
		MetricName: LabelLineagePassthrough,
	}
}

// syntheticLineage starts with every label dropped — used by
// synthesizers such as vector() and absent() that emit a fresh
// labelset unrelated to their input.
func syntheticLineage() LabelLineage {
	return LabelLineage{
		Known:      map[string]string{"__name__": lineageStateDropped},
		Wildcard:   lineageStateDropped,
		MetricName: LabelLineageDropped,
	}
}

// syntheticOutputLineage extends syntheticLineage with explicit
// synthesized labels (absent() output metric labels, for example).
func syntheticOutputLineage(labels map[string]string) LabelLineage {
	lineage := syntheticLineage()
	for label := range labels {
		lineage.Known[label] = lineageStateSynthetic
	}
	return lineage
}

// cloneLineage returns a deep copy so propagation never shares Known
// map state.
func cloneLineage(src LabelLineage) LabelLineage {
	out := LabelLineage{
		Known:      make(map[string]string, len(src.Known)),
		Wildcard:   src.Wildcard,
		MetricName: src.MetricName,
	}
	for key, value := range src.Known {
		out.Known[key] = value
	}
	return out
}

// passthroughLineage returns a clone of `child` suitable for a node
// that preserves the entire input labelset unchanged.
func passthroughLineage(child LabelLineage) LabelLineage {
	return cloneLineage(child)
}

// aggregationLineage mirrors native.aggregationLabelLineage but
// produces the logical-package lineage shape: by(...) keeps grouping
// labels, without(...) keeps everything else, __name__ is always
// dropped.
func aggregationLineage(child LabelLineage, grouping []string, without bool) LabelLineage {
	result := LabelLineage{
		Known:      map[string]string{"__name__": lineageStateDropped},
		MetricName: LabelLineageDropped,
	}
	if without {
		result.Wildcard = child.Wildcard
		for key, value := range child.Known {
			if key == "__name__" {
				continue
			}
			result.Known[key] = value
		}
		for _, label := range grouping {
			result.Known[label] = lineageStateDropped
		}
		return result
	}
	result.Wildcard = lineageStateDropped
	for _, label := range grouping {
		if state, ok := child.Known[label]; ok {
			result.Known[label] = state
			continue
		}
		if child.Wildcard != "" {
			result.Known[label] = child.Wildcard
			continue
		}
		result.Known[label] = lineageStateUnknown
	}
	return result
}
