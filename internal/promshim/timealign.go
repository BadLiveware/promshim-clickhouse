package promshim

import (
	"sort"
	"time"
)

func buildConstantRangePoints(value float64, params evalParams) ([]rangePoint, error) {
	if params.Step <= 0 {
		return nil, newBadDataErrorf("step must be greater than zero for range evaluation")
	}
	values := make([]rangePoint, 0)
	for current := params.Start; !current.After(params.End); current = current.Add(params.Step) {
		timestamp := float64(current.UnixNano()) / float64(time.Second)
		values = append(values, rangePoint{Timestamp: timestamp, Value: value})
	}
	return values, nil
}

func sortAndValidateRangePoints(points []rangePoint) ([]rangePoint, error) {
	result := cloneRangePoints(points)
	sort.Slice(result, func(i, j int) bool {
		return result[i].Timestamp < result[j].Timestamp
	})
	for i := 1; i < len(result); i++ {
		if result[i].Timestamp == result[i-1].Timestamp {
			return nil, newExecutionErrorf("vector cannot contain metrics with the same labelset")
		}
	}
	return result, nil
}

func appendRangePointsStrict(existing, incoming []rangePoint) ([]rangePoint, error) {
	if len(existing) == 0 {
		return cloneRangePoints(incoming), nil
	}
	if len(incoming) == 0 {
		return cloneRangePoints(existing), nil
	}
	if incoming[0].Timestamp <= existing[len(existing)-1].Timestamp {
		return nil, newExecutionErrorf("chunked range merge encountered non-increasing timestamps")
	}
	result := make([]rangePoint, 0, len(existing)+len(incoming))
	result = append(result, cloneRangePoints(existing)...)
	result = append(result, cloneRangePoints(incoming)...)
	return result, nil
}
