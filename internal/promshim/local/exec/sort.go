package exec

import (
	"math"
	"sort"
	"strings"

	"github.com/BadLiveware/promshim-ch/internal/promshim/model"
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
		lv := left.Metric[label]
		rv := right.Metric[label]
		if lv == rv {
			continue
		}
		cmp := naturalCompareStrings(lv, rv)
		if descending {
			return -cmp
		}
		return cmp
	}
	return compareSampleIdentity(left, right, descending)
}

func naturalCompareStrings(left, right string) int {
	for len(left) > 0 && len(right) > 0 {
		leftDigit := left[0] >= '0' && left[0] <= '9'
		rightDigit := right[0] >= '0' && right[0] <= '9'
		if leftDigit && rightDigit {
			li, lj := 0, 0
			for li < len(left) && left[li] == '0' {
				li++
			}
			for lj < len(right) && right[lj] == '0' {
				lj++
			}
			le, re := li, lj
			for le < len(left) && left[le] >= '0' && left[le] <= '9' {
				le++
			}
			for re < len(right) && right[re] >= '0' && right[re] <= '9' {
				re++
			}
			leftDigits := left[li:le]
			rightDigits := right[lj:re]
			switch {
			case len(leftDigits) < len(rightDigits):
				return -1
			case len(leftDigits) > len(rightDigits):
				return 1
			case leftDigits < rightDigits:
				return -1
			case leftDigits > rightDigits:
				return 1
			}
			left = left[le:]
			right = right[re:]
			continue
		}
		if left[0] < right[0] {
			return -1
		}
		if left[0] > right[0] {
			return 1
		}
		left = left[1:]
		right = right[1:]
	}
	switch {
	case len(left) < len(right):
		return -1
	case len(left) > len(right):
		return 1
	default:
		return 0
	}
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
