package model

import (
	"fmt"
	"sort"
	"time"
)

func BuildConstantRangePoints(start, end time.Time, step time.Duration, value float64) ([]RangePoint, error) {
	if step <= 0 {
		return nil, fmt.Errorf("step must be greater than zero for range evaluation")
	}
	values := make([]RangePoint, 0)
	for current := start; !current.After(end); current = current.Add(step) {
		timestamp := float64(current.UnixNano()) / float64(time.Second)
		values = append(values, RangePoint{Timestamp: timestamp, Value: value})
	}
	return values, nil
}

func SortAndValidateRangePoints(points []RangePoint) ([]RangePoint, error) {
	result := CloneRangePoints(points)
	sort.Slice(result, func(i, j int) bool {
		return result[i].Timestamp < result[j].Timestamp
	})
	for i := 1; i < len(result); i++ {
		if result[i].Timestamp == result[i-1].Timestamp {
			return nil, ErrDuplicateLabelsetTimestamps
		}
	}
	return result, nil
}

func AppendRangePointsStrict(existing, incoming []RangePoint) ([]RangePoint, error) {
	if len(existing) == 0 {
		return CloneRangePoints(incoming), nil
	}
	if len(incoming) == 0 {
		return CloneRangePoints(existing), nil
	}
	if incoming[0].Timestamp <= existing[len(existing)-1].Timestamp {
		return nil, ErrNonIncreasingChunkMerge
	}
	result := make([]RangePoint, 0, len(existing)+len(incoming))
	result = append(result, CloneRangePoints(existing)...)
	result = append(result, CloneRangePoints(incoming)...)
	return result, nil
}
