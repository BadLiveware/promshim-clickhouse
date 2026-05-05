package rules

import (
	"strings"
	"testing"
	"time"
)

func TestBuildMaterializationInsertUsesUnixMilliseconds(t *testing.T) {
	rows := []struct {
		Tags      [][2]string `json:"tags"`
		Timestamp float64     `json:"timestamp"`
		Value     float64     `json:"value"`
	}{
		{Tags: [][2]string{{"job", "kubelet"}}, Timestamp: 1778004750907, Value: 3},
	}

	sql, err := buildMaterializationInsert("promshim_rules", rows, RecordingRule{Name: "count:up0"}, time.UnixMilli(1778004750907))
	if err != nil {
		t.Fatalf("buildMaterializationInsert: %v", err)
	}
	if !strings.Contains(sql, "fromUnixTimestamp64Milli(1778004750907)") {
		t.Fatalf("insert SQL did not use Unix millisecond timestamp: %s", sql)
	}
	if strings.Contains(sql, "toDateTime64(1778004750907") || strings.Contains(sql, "toUnixTimestamp64Milli(toDateTime64") {
		t.Fatalf("insert SQL still treats milliseconds as DateTime seconds: %s", sql)
	}
	if !strings.Contains(sql, "tuple('__name__', 'count:up0')") {
		t.Fatalf("insert SQL did not add recording rule metric name tag: %s", sql)
	}
}
