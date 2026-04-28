package storage

import (
	"strings"
	"testing"

	"github.com/prometheus/prometheus/promql/parser"
)

func TestBuildInstantBinaryVectorJoinSQLIncludesJoinKeyAndDuplicateChecks(t *testing.T) {
	lhsSQL := "SELECT tags, timestamp, value FROM lhs_source"
	rhsSQL := "SELECT tags, timestamp, value FROM rhs_source"
	sql, _, err := BuildInstantBinaryVectorJoinSQL(lhsSQL, nil, rhsSQL, nil, BinaryJoinConfig{
		Op: parser.MUL,
		VectorMatching: &parser.VectorMatching{
			Card:           parser.CardManyToOne,
			On:             true,
			MatchingLabels: []string{"job"},
			Include:        []string{"team"},
		},
		JoinShape: "many_to_one",
	})
	if err != nil {
		t.Fatalf("expected join SQL, got error: %v", err)
	}
	for _, expected := range []string{
		"arraySort(tag -> tag.1, arrayFilter(tag -> has(['job'], tag.1), tags)) AS join_group",
		"throwIf(count() > 1, 'found duplicate series for the match group on the rhs hand-side of the operation') = 0",
		"lhs.join_group = rhs.join_group",
		"arrayFilter(tag -> tag.1 != '__name__'",
		"has(['team'], tag.1)",
		"tags AS original_group",
	} {
		if !strings.Contains(sql, expected) {
			t.Fatalf("expected %q in SQL, got %q", expected, sql)
		}
	}
	if strings.Contains(sql, "GROUP BY original_group") {
		t.Fatalf("did not expect aggregate alias grouping in SQL, got %q", sql)
	}
}

func TestBuildInstantBinaryVectorSelfJoinSQLUsesOneSource(t *testing.T) {
	sourceSQL := "SELECT tags, timestamp, value FROM source"
	sql, _, err := BuildInstantBinaryVectorSelfJoinSQL(sourceSQL, map[string]string{"a": "b"}, BinaryJoinConfig{
		Op: parser.ADD,
		VectorMatching: &parser.VectorMatching{
			Card: parser.CardOneToOne,
		},
		JoinShape: "one_to_one",
	})
	if err != nil {
		t.Fatalf("expected self-join SQL, got error: %v", err)
	}
	for _, expected := range []string{
		"(lhs.value + lhs.value) AS value",
		"SELECT tags, timestamp, value FROM source",
		"throwIf(count() > 1, 'found duplicate series for the match group on the lhs hand-side of the operation') = 0",
		"arrayFilter(tag -> tag.1 != '__name__'",
	} {
		if !strings.Contains(sql, expected) {
			t.Fatalf("expected %q in SQL, got %q", expected, sql)
		}
	}
	if strings.Contains(sql, " rhs") || strings.Contains(sql, " JOIN ") {
		t.Fatalf("did not expect second source or join in self-join SQL, got %q", sql)
	}
}

func TestBuildRangeBinaryVectorSelfJoinSQLUsesOneFlattenedSource(t *testing.T) {
	sourceSQL := "SELECT tags, time_series FROM source"
	sql, _, err := BuildRangeBinaryVectorSelfJoinSQL(sourceSQL, map[string]string{"a": "b"}, BinaryJoinConfig{
		Op: parser.ADD,
		VectorMatching: &parser.VectorMatching{
			Card: parser.CardOneToOne,
		},
		JoinShape: "one_to_one",
	})
	if err != nil {
		t.Fatalf("expected range self-join SQL, got error: %v", err)
	}
	for _, expected := range []string{
		"ARRAY JOIN time_series AS point",
		"(lhs.value + lhs.value) AS value",
		"arraySort(item -> item.1, groupArray((timestamp, value))) AS time_series",
		"throwIf(count() > 1, 'found duplicate series for the match group on the lhs hand-side of the operation') = 0",
	} {
		if !strings.Contains(sql, expected) {
			t.Fatalf("expected %q in SQL, got %q", expected, sql)
		}
	}
	if strings.Contains(sql, " rhs") || strings.Contains(sql, " INNER JOIN ") || strings.Contains(sql, " ALL INNER JOIN ") {
		t.Fatalf("did not expect second source or binary join in range self-join SQL, got %q", sql)
	}
}

func TestBuildRangeBinaryVectorJoinSQLJoinsOnTimestampAndJoinGroup(t *testing.T) {
	lhsSQL := "SELECT tags, time_series FROM lhs_source"
	rhsSQL := "SELECT tags, time_series FROM rhs_source"
	sql, _, err := BuildRangeBinaryVectorJoinSQL(lhsSQL, nil, rhsSQL, nil, BinaryJoinConfig{
		Op: parser.ADD,
		VectorMatching: &parser.VectorMatching{
			Card: parser.CardOneToOne,
		},
		JoinShape: "one_to_one",
	})
	if err != nil {
		t.Fatalf("expected range join SQL, got error: %v", err)
	}
	for _, expected := range []string{
		"ARRAY JOIN time_series AS point",
		"lhs.join_group = rhs.join_group AND lhs.timestamp = rhs.timestamp",
		"arraySort(item -> item.1, groupArray((timestamp, value))) AS time_series",
		"timestamp AS timestamp",
	} {
		if !strings.Contains(sql, expected) {
			t.Fatalf("expected %q in SQL, got %q", expected, sql)
		}
	}
	if strings.Contains(sql, "any(timestamp) AS timestamp") {
		t.Fatalf("did not expect range join SQL to aggregate grouped timestamps, got %q", sql)
	}
}

func TestBuildInstantBinaryVectorJoinSQLSupportsSetOperators(t *testing.T) {
	lhsSQL := "SELECT tags, timestamp, value FROM lhs_source"
	rhsSQL := "SELECT tags, timestamp, value FROM rhs_source"
	for _, tc := range []struct {
		op    parser.ItemType
		check string
	}{
		{op: parser.LAND, check: "INNER JOIN"},
		{op: parser.LOR, check: "UNION ALL"},
		{op: parser.LUNLESS, check: "LEFT JOIN"},
	} {
		sql, _, err := BuildInstantBinaryVectorJoinSQL(lhsSQL, nil, rhsSQL, nil, BinaryJoinConfig{Op: tc.op, VectorMatching: &parser.VectorMatching{Card: parser.CardManyToMany, On: true, MatchingLabels: []string{"job"}}, JoinShape: "many_to_many"})
		if err != nil {
			t.Fatalf("expected set-operator SQL for %v, got error: %v", tc.op, err)
		}
		for _, expected := range []string{"toUInt8(1) AS present_marker", tc.check, "lhs.join_group = rhs.join_group", "result_tags AS tags"} {
			if !strings.Contains(sql, expected) {
				t.Fatalf("expected %q in %v SQL, got %q", expected, tc.op, sql)
			}
		}
	}
}
