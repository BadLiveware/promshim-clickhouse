package promshim

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

func parsePrometheusTimestamp(raw string) (time.Time, error) {
	if raw == "" {
		return time.Time{}, fmt.Errorf("empty timestamp")
	}

	if seconds, err := strconv.ParseFloat(raw, 64); err == nil {
		sec := int64(seconds)
		nsec := int64((seconds - float64(sec)) * float64(time.Second))
		return time.Unix(sec, nsec).UTC(), nil
	}

	layouts := []string{time.RFC3339Nano, time.RFC3339}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed.UTC(), nil
		}
	}

	return time.Time{}, fmt.Errorf("invalid timestamp %q", raw)
}

func parsePrometheusDuration(raw string) (time.Duration, error) {
	if raw == "" {
		return 0, fmt.Errorf("empty duration")
	}
	if seconds, err := strconv.ParseFloat(raw, 64); err == nil {
		return time.Duration(seconds * float64(time.Second)), nil
	}

	units := map[string]time.Duration{
		"ms": time.Millisecond,
		"s":  time.Second,
		"m":  time.Minute,
		"h":  time.Hour,
		"d":  24 * time.Hour,
		"w":  7 * 24 * time.Hour,
		"y":  365 * 24 * time.Hour,
	}

	remaining := raw
	var total time.Duration
	for remaining != "" {
		valueEnd := 0
		for valueEnd < len(remaining) && remaining[valueEnd] >= '0' && remaining[valueEnd] <= '9' {
			valueEnd++
		}
		if valueEnd == 0 {
			return 0, fmt.Errorf("invalid duration %q", raw)
		}

		value, err := strconv.Atoi(remaining[:valueEnd])
		if err != nil {
			return 0, err
		}

		remaining = remaining[valueEnd:]
		unit := ""
		if strings.HasPrefix(remaining, "ms") {
			unit = "ms"
			remaining = remaining[2:]
		} else if remaining != "" {
			unit = remaining[:1]
			remaining = remaining[1:]
		}

		multiplier, ok := units[unit]
		if !ok {
			return 0, fmt.Errorf("invalid duration unit in %q", raw)
		}
		total += time.Duration(value) * multiplier
	}

	return total, nil
}

func parseClickHouseTimestamp(raw string) (float64, error) {
	layouts := []string{
		"2006-01-02 15:04:05.000",
		"2006-01-02 15:04:05.999999",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		if parsed, err := time.ParseInLocation(layout, raw, time.UTC); err == nil {
			return float64(parsed.UnixNano()) / float64(time.Second), nil
		}
	}
	return 0, fmt.Errorf("invalid ClickHouse timestamp %q", raw)
}
