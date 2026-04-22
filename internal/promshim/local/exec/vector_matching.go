package exec

import (
	"sort"

	"github.com/BadLiveware/promshim-ch/internal/promshim/model"
	"github.com/prometheus/prometheus/promql/parser"
)

func applyVectorVectorBinaryInstant(op parser.ItemType, lhsSamples, rhsSamples []model.InstantSample, vectorMatching *parser.VectorMatching, returnBool bool) ([]model.InstantSample, error) {
	matching := normalizedVectorMatching(vectorMatching)
	if isSetOperator(op) {
		return applyVectorVectorSetInstant(op, lhsSamples, rhsSamples, matching)
	}
	if matching.Card == parser.CardManyToMany {
		return nil, badDataf("many-to-many matching is only allowed for set operators")
	}
	if (len(lhsSamples) == 0 && len(rhsSamples) == 0) ||
		((len(lhsSamples) == 0 || len(rhsSamples) == 0) && matching.FillValues.LHS == nil && matching.FillValues.RHS == nil) {
		return nil, nil
	}

	lhs := lhsSamples
	rhs := rhsSamples
	swappedForOneToMany := false
	if matching.Card == parser.CardOneToMany {
		lhs, rhs = rhs, lhs
		swappedForOneToMany = true
	}

	rightBySignature := make(map[string]model.InstantSample, len(rhs))
	rightSignaturesPresent := make(map[string]struct{}, len(rhs))
	for _, sample := range rhs {
		signature := vectorMatchSignature(sample.Metric, matching)
		if _, exists := rightSignaturesPresent[signature]; exists {
			oneSide := "right"
			if swappedForOneToMany {
				oneSide = "left"
			}
			return nil, badDataf("found duplicate series for the match group %v on the %s hand-side of the operation;many-to-many matching not allowed: matching labels must be unique on one side", vectorMatchGroupLabels(sample.Metric, matching), oneSide)
		}
		rightBySignature[signature] = sample
		rightSignaturesPresent[signature] = struct{}{}
	}

	matchedOneToOne := make(map[string]struct{}, len(lhs))
	matchedManyToOne := make(map[string]map[string]struct{}, len(lhs))

	result := make([]model.InstantSample, 0, len(lhs)+len(rhs))
	appendResult := func(leftSample, rightSample model.InstantSample, signature string) error {
		exprLHS, exprRHS := leftSample, rightSample
		if swappedForOneToMany {
			exprLHS, exprRHS = rightSample, leftSample
		}

		binaryValue := applyScalarBinary(op, exprLHS.Value, exprRHS.Value)
		keep := true
		outputValue := binaryValue
		if isComparisonBinaryOperator(op) {
			comparisonKept := binaryValue != 0
			if returnBool {
				outputValue = boolToFloat(comparisonKept)
			} else {
				outputValue = exprLHS.Value
				keep = comparisonKept
			}
		}
		if !keep {
			return nil
		}

		resultMetric := buildVectorVectorResultMetric(leftSample.Metric, rightSample.Metric, op, matching, returnBool)
		if matching.Card == parser.CardOneToOne {
			if _, exists := matchedOneToOne[signature]; exists {
				return badDataf("multiple matches for labels: many-to-one matching must be explicit (group_left/group_right)")
			}
			matchedOneToOne[signature] = struct{}{}
		} else {
			resultKey := model.LabelsKey(resultMetric)
			matchedResultKeys := matchedManyToOne[signature]
			if matchedResultKeys == nil {
				matchedResultKeys = map[string]struct{}{}
				matchedManyToOne[signature] = matchedResultKeys
			}
			if _, duplicate := matchedResultKeys[resultKey]; duplicate {
				return badDataf("multiple matches for labels: grouping labels must ensure unique matches")
			}
			matchedResultKeys[resultKey] = struct{}{}
		}

		result = append(result, model.InstantSample{Metric: resultMetric, Timestamp: exprLHS.Timestamp, Value: outputValue})
		return nil
	}

	for _, leftSample := range lhs {
		signature := vectorMatchSignature(leftSample.Metric, matching)
		rightSample, matched := rightBySignature[signature]
		if !matched {
			if matching.FillValues.RHS == nil {
				continue
			}
			rightSample = model.InstantSample{Metric: vectorMatchGroupLabels(leftSample.Metric, matching), Timestamp: leftSample.Timestamp, Value: *matching.FillValues.RHS}
		}
		if err := appendResult(leftSample, rightSample, signature); err != nil {
			return nil, err
		}
	}

	if matching.FillValues.LHS != nil {
		for _, rightSample := range rhs {
			signature := vectorMatchSignature(rightSample.Metric, matching)
			if (matching.Card == parser.CardOneToOne && hasSignature(matchedOneToOne, signature)) ||
				(matching.Card != parser.CardOneToOne && hasManyToOneSignature(matchedManyToOne, signature)) {
				continue
			}
			leftSample := model.InstantSample{Metric: vectorMatchGroupLabels(rightSample.Metric, matching), Timestamp: rightSample.Timestamp, Value: *matching.FillValues.LHS}
			if err := appendResult(leftSample, rightSample, signature); err != nil {
				return nil, err
			}
		}
	}

	sort.SliceStable(result, func(i, j int) bool {
		leftKey := model.LabelsKey(result[i].Metric)
		rightKey := model.LabelsKey(result[j].Metric)
		if leftKey == rightKey {
			return result[i].Timestamp < result[j].Timestamp
		}
		return leftKey < rightKey
	})

	return result, nil
}

func applyVectorVectorBinaryRange(op parser.ItemType, lhsSeries, rhsSeries []model.RangeSeries, vectorMatching *parser.VectorMatching, returnBool bool) ([]model.RangeSeries, error) {
	lhsSamplesByTimestamp, _ := rangeSeriesSamplesByTimestamp(lhsSeries)
	rhsSamplesByTimestamp, _ := rangeSeriesSamplesByTimestamp(rhsSeries)

	timestampSet := make(map[float64]struct{}, len(lhsSamplesByTimestamp)+len(rhsSamplesByTimestamp))
	for timestamp := range lhsSamplesByTimestamp {
		timestampSet[timestamp] = struct{}{}
	}
	for timestamp := range rhsSamplesByTimestamp {
		timestampSet[timestamp] = struct{}{}
	}
	timestamps := make([]float64, 0, len(timestampSet))
	for timestamp := range timestampSet {
		timestamps = append(timestamps, timestamp)
	}
	sort.Float64s(timestamps)

	grouped := make(map[string]model.RangeSeries)
	for _, timestamp := range timestamps {
		instant, err := applyVectorVectorBinaryInstant(op, lhsSamplesByTimestamp[timestamp], rhsSamplesByTimestamp[timestamp], vectorMatching, returnBool)
		if err != nil {
			return nil, err
		}
		for _, sample := range instant {
			key := model.LabelsKey(sample.Metric)
			item := grouped[key]
			if item.Metric == nil {
				item.Metric = model.CloneMetric(sample.Metric)
			}
			item.Values = append(item.Values, model.RangePoint{Timestamp: sample.Timestamp, Value: sample.Value})
			grouped[key] = item
		}
	}

	keys := sortedMapKeys(grouped)
	result := make([]model.RangeSeries, 0, len(keys))
	for _, key := range keys {
		result = append(result, grouped[key])
	}
	return result, nil
}

func normalizedVectorMatching(vectorMatching *parser.VectorMatching) *parser.VectorMatching {
	if vectorMatching == nil {
		return &parser.VectorMatching{Card: parser.CardOneToOne}
	}
	cloned := &parser.VectorMatching{
		Card:           vectorMatching.Card,
		MatchingLabels: append([]string(nil), vectorMatching.MatchingLabels...),
		On:             vectorMatching.On,
		Include:        append([]string(nil), vectorMatching.Include...),
	}
	if vectorMatching.FillValues.LHS != nil {
		lhs := *vectorMatching.FillValues.LHS
		cloned.FillValues.LHS = &lhs
	}
	if vectorMatching.FillValues.RHS != nil {
		rhs := *vectorMatching.FillValues.RHS
		cloned.FillValues.RHS = &rhs
	}
	return cloned
}

func vectorMatchSignature(metric map[string]string, matching *parser.VectorMatching) string {
	return model.LabelsKey(vectorMatchGroupLabels(metric, matching))
}

func vectorMatchGroupLabels(metric map[string]string, matching *parser.VectorMatching) map[string]string {
	if matching.On {
		result := make(map[string]string, len(matching.MatchingLabels))
		for _, label := range matching.MatchingLabels {
			if value, ok := metric[label]; ok {
				result[label] = value
			}
		}
		return result
	}

	ignored := make(map[string]struct{}, len(matching.MatchingLabels)+1)
	ignored["__name__"] = struct{}{}
	for _, label := range matching.MatchingLabels {
		ignored[label] = struct{}{}
	}
	result := make(map[string]string, len(metric))
	for label, value := range metric {
		if _, skip := ignored[label]; skip {
			continue
		}
		result[label] = value
	}
	return result
}

func hasSignature(items map[string]struct{}, signature string) bool {
	_, ok := items[signature]
	return ok
}

func hasManyToOneSignature(items map[string]map[string]struct{}, signature string) bool {
	_, ok := items[signature]
	return ok
}

func applyVectorVectorSetInstant(op parser.ItemType, lhsSamples, rhsSamples []model.InstantSample, matching *parser.VectorMatching) ([]model.InstantSample, error) {
	if matching.Card != parser.CardManyToMany {
		return nil, badDataf("set operations require many-to-many vector matching")
	}
	if matching.FillValues.LHS != nil || matching.FillValues.RHS != nil {
		return nil, badDataf("filling in missing series not allowed for set operators")
	}

	signatureFor := func(metric map[string]string) string {
		return vectorMatchSignature(metric, matching)
	}

	switch op {
	case parser.LAND:
		if len(lhsSamples) == 0 || len(rhsSamples) == 0 {
			return nil, nil
		}
		rightPresent := make(map[string]struct{}, len(rhsSamples))
		for _, sample := range rhsSamples {
			rightPresent[signatureFor(sample.Metric)] = struct{}{}
		}
		result := make([]model.InstantSample, 0, len(lhsSamples))
		for _, sample := range lhsSamples {
			if _, ok := rightPresent[signatureFor(sample.Metric)]; ok {
				result = append(result, cloneInstantSample(sample))
			}
		}
		return result, nil
	case parser.LOR:
		switch {
		case len(lhsSamples) == 0:
			return cloneInstantSamples(rhsSamples), nil
		case len(rhsSamples) == 0:
			return cloneInstantSamples(lhsSamples), nil
		}
		leftPresent := make(map[string]struct{}, len(lhsSamples))
		result := make([]model.InstantSample, 0, len(lhsSamples)+len(rhsSamples))
		for _, sample := range lhsSamples {
			leftPresent[signatureFor(sample.Metric)] = struct{}{}
			result = append(result, cloneInstantSample(sample))
		}
		for _, sample := range rhsSamples {
			if _, ok := leftPresent[signatureFor(sample.Metric)]; ok {
				continue
			}
			result = append(result, cloneInstantSample(sample))
		}
		return result, nil
	case parser.LUNLESS:
		if len(lhsSamples) == 0 || len(rhsSamples) == 0 {
			return cloneInstantSamples(lhsSamples), nil
		}
		rightPresent := make(map[string]struct{}, len(rhsSamples))
		for _, sample := range rhsSamples {
			rightPresent[signatureFor(sample.Metric)] = struct{}{}
		}
		result := make([]model.InstantSample, 0, len(lhsSamples))
		for _, sample := range lhsSamples {
			if _, ok := rightPresent[signatureFor(sample.Metric)]; ok {
				continue
			}
			result = append(result, cloneInstantSample(sample))
		}
		return result, nil
	default:
		return nil, executionf("set operator %q is not implemented yet", op.String())
	}
}

func cloneInstantSample(sample model.InstantSample) model.InstantSample {
	return model.InstantSample{Metric: model.CloneMetric(sample.Metric), Timestamp: sample.Timestamp, Value: sample.Value}
}

func cloneInstantSamples(samples []model.InstantSample) []model.InstantSample {
	result := make([]model.InstantSample, 0, len(samples))
	for _, sample := range samples {
		result = append(result, cloneInstantSample(sample))
	}
	return result
}

func isSetOperator(op parser.ItemType) bool {
	switch op {
	case parser.LAND, parser.LOR, parser.LUNLESS:
		return true
	default:
		return false
	}
}

func buildVectorVectorResultMetric(lhsMetric, rhsMetric map[string]string, op parser.ItemType, matching *parser.VectorMatching, returnBool bool) map[string]string {
	result := model.CloneMetric(lhsMetric)
	if !isComparisonBinaryOperator(op) || returnBool {
		result = model.DropMetricName(result)
	}

	if matching.Card == parser.CardOneToOne {
		if matching.On {
			kept := make(map[string]string, len(matching.MatchingLabels))
			for _, label := range matching.MatchingLabels {
				if value, ok := result[label]; ok {
					kept[label] = value
				}
			}
			result = kept
		} else {
			for _, label := range matching.MatchingLabels {
				delete(result, label)
			}
		}
	}

	for _, label := range matching.Include {
		if value, ok := rhsMetric[label]; ok {
			result[label] = value
		} else {
			delete(result, label)
		}
	}
	return result
}
