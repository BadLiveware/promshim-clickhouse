package exec

import (
	"math"
	"sort"
	"strings"

	"ch-observability/internal/promshim/model"
)

func ApplySortFunction(name string, input model.RuntimeValue, labels []string) (model.VectorValue, error) {
	vector, ok := input.(model.VectorValue)
	if !ok {
		return model.VectorValue{}, unsupportedf("%s currently requires vector input, got %T", name, input)
	}
	out := make([]model.InstantSample, len(vector.Samples))
	copy(out, vector.Samples)

	switch name {
	case "sort":
		sort.SliceStable(out, func(i, j int) bool {
			return compareByValue(out[i], out[j], false) < 0
		})
	case "sort_desc":
		sort.SliceStable(out, func(i, j int) bool {
			return compareByValue(out[i], out[j], true) < 0
		})
	case "sort_by_label":
		sort.SliceStable(out, func(i, j int) bool {
			return compareByLabels(out[i], out[j], labels, false) < 0
		})
	case "sort_by_label_desc":
		sort.SliceStable(out, func(i, j int) bool {
			return compareByLabels(out[i], out[j], labels, true) < 0
		})
	default:
		return model.VectorValue{}, unsupportedf("sort function %q is not implemented yet", name)
	}
	return model.VectorValue{Samples: out}, nil
}

func compareByValue(left, right model.InstantSample, descending bool) int {
	leftNaN := math.IsNaN(left.Value)
	rightNaN := math.IsNaN(right.Value)
	switch {
	case leftNaN && rightNaN:
		return compareSampleIdentity(left, right, false)
	case leftNaN:
		return 1
	case rightNaN:
		return -1
	case left.Value < right.Value:
		if descending {
			return 1
		}
		return -1
	case left.Value > right.Value:
		if descending {
			return -1
		}
		return 1
	default:
		return compareSampleIdentity(left, right, false)
	}
}

func compareByLabels(left, right model.InstantSample, labels []string, descending bool) int {
	for _, label := range labels {
		cmp := strings.Compare(left.Metric[label], right.Metric[label])
		if cmp == 0 {
			continue
		}
		if descending {
			return -cmp
		}
		return cmp
	}
	return compareSampleIdentity(left, right, descending)
}

func compareSampleIdentity(left, right model.InstantSample, descending bool) int {
	cmp := strings.Compare(model.LabelsKey(left.Metric), model.LabelsKey(right.Metric))
	if cmp == 0 {
		switch {
		case left.Timestamp < right.Timestamp:
			cmp = -1
		case left.Timestamp > right.Timestamp:
			cmp = 1
		default:
			cmp = 0
		}
	}
	if descending {
		return -cmp
	}
	return cmp
}
