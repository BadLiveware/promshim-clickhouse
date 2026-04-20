package exec

import (
	"testing"
	"time"

	"ch-observability/internal/promshim/model"
	"github.com/prometheus/prometheus/promql/parser"
)

func TestApplyBinaryRuntimeValueVectorVectorOneToOneArithmetic(t *testing.T) {
	lhs := model.VectorValue{Samples: []model.InstantSample{{
		Metric:    map[string]string{"__name__": "metric_a", "job": "api", "instance": "a"},
		Timestamp: 1,
		Value:     2,
	}}}
	rhs := model.VectorValue{Samples: []model.InstantSample{{
		Metric:    map[string]string{"__name__": "metric_b", "job": "api", "instance": "a"},
		Timestamp: 1,
		Value:     3,
	}}}

	value, err := ApplyBinaryRuntimeValue(parser.ADD, lhs, rhs, nil, false, EvalParams{Mode: EvalModeInstant, EvaluationTime: time.Unix(1, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	vector := value.(model.VectorValue)
	if len(vector.Samples) != 1 {
		t.Fatalf("expected one output sample, got %#v", vector.Samples)
	}
	if vector.Samples[0].Value != 5 {
		t.Fatalf("expected arithmetic value 5, got %#v", vector.Samples[0])
	}
	if _, ok := vector.Samples[0].Metric["__name__"]; ok {
		t.Fatalf("did not expect __name__ in arithmetic vector-vector result: %#v", vector.Samples[0].Metric)
	}
}

func TestApplyBinaryRuntimeValueVectorVectorOnMatching(t *testing.T) {
	lhs := model.VectorValue{Samples: []model.InstantSample{{
		Metric:    map[string]string{"__name__": "metric_a", "job": "api", "instance": "a"},
		Timestamp: 1,
		Value:     2,
	}}}
	rhs := model.VectorValue{Samples: []model.InstantSample{{
		Metric:    map[string]string{"__name__": "metric_b", "job": "api", "instance": "b"},
		Timestamp: 1,
		Value:     3,
	}}}
	matching := &parser.VectorMatching{Card: parser.CardOneToOne, On: true, MatchingLabels: []string{"job"}}

	value, err := ApplyBinaryRuntimeValue(parser.MUL, lhs, rhs, matching, false, EvalParams{Mode: EvalModeInstant, EvaluationTime: time.Unix(1, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	vector := value.(model.VectorValue)
	if len(vector.Samples) != 1 || vector.Samples[0].Value != 6 {
		t.Fatalf("unexpected vector-vector on() result: %#v", vector.Samples)
	}
	if len(vector.Samples[0].Metric) != 1 || vector.Samples[0].Metric["job"] != "api" {
		t.Fatalf("expected only matching label in one-to-one on() result, got %#v", vector.Samples[0].Metric)
	}
}

func TestApplyBinaryRuntimeValueVectorVectorGroupLeftInclude(t *testing.T) {
	lhs := model.VectorValue{Samples: []model.InstantSample{
		{Metric: map[string]string{"__name__": "metric_a", "job": "api", "instance": "a"}, Timestamp: 1, Value: 1},
		{Metric: map[string]string{"__name__": "metric_a", "job": "api", "instance": "b"}, Timestamp: 1, Value: 2},
	}}
	rhs := model.VectorValue{Samples: []model.InstantSample{{
		Metric:    map[string]string{"__name__": "metric_b", "job": "api", "team": "core"},
		Timestamp: 1,
		Value:     10,
	}}}
	matching := &parser.VectorMatching{Card: parser.CardManyToOne, On: true, MatchingLabels: []string{"job"}, Include: []string{"team"}}

	value, err := ApplyBinaryRuntimeValue(parser.MUL, lhs, rhs, matching, false, EvalParams{Mode: EvalModeInstant, EvaluationTime: time.Unix(1, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	vector := value.(model.VectorValue)
	if len(vector.Samples) != 2 {
		t.Fatalf("expected two group_left output samples, got %#v", vector.Samples)
	}
	if vector.Samples[0].Metric["instance"] != "a" || vector.Samples[0].Metric["team"] != "core" || vector.Samples[0].Value != 10 {
		t.Fatalf("unexpected first group_left sample: %#v", vector.Samples[0])
	}
	if vector.Samples[1].Metric["instance"] != "b" || vector.Samples[1].Metric["team"] != "core" || vector.Samples[1].Value != 20 {
		t.Fatalf("unexpected second group_left sample: %#v", vector.Samples[1])
	}
}

func TestApplyBinaryRuntimeValueVectorVectorGroupRightInclude(t *testing.T) {
	lhs := model.VectorValue{Samples: []model.InstantSample{{
		Metric:    map[string]string{"__name__": "metric_a", "job": "api", "team": "core"},
		Timestamp: 1,
		Value:     10,
	}}}
	rhs := model.VectorValue{Samples: []model.InstantSample{
		{Metric: map[string]string{"__name__": "metric_b", "job": "api", "instance": "a"}, Timestamp: 1, Value: 1},
		{Metric: map[string]string{"__name__": "metric_b", "job": "api", "instance": "b"}, Timestamp: 1, Value: 2},
	}}
	matching := &parser.VectorMatching{Card: parser.CardOneToMany, On: true, MatchingLabels: []string{"job"}, Include: []string{"team"}}

	value, err := ApplyBinaryRuntimeValue(parser.MUL, lhs, rhs, matching, false, EvalParams{Mode: EvalModeInstant, EvaluationTime: time.Unix(1, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	vector := value.(model.VectorValue)
	if len(vector.Samples) != 2 {
		t.Fatalf("expected two group_right output samples, got %#v", vector.Samples)
	}
	if vector.Samples[0].Metric["instance"] != "a" || vector.Samples[0].Metric["team"] != "core" || vector.Samples[0].Value != 10 {
		t.Fatalf("unexpected first group_right sample: %#v", vector.Samples[0])
	}
	if vector.Samples[1].Metric["instance"] != "b" || vector.Samples[1].Metric["team"] != "core" || vector.Samples[1].Value != 20 {
		t.Fatalf("unexpected second group_right sample: %#v", vector.Samples[1])
	}
}

func TestApplyBinaryRuntimeValueVectorVectorComparisonBoolDropsMetricName(t *testing.T) {
	lhs := model.VectorValue{Samples: []model.InstantSample{{
		Metric:    map[string]string{"__name__": "metric_a", "job": "api", "instance": "a"},
		Timestamp: 1,
		Value:     1,
	}}}
	rhs := model.VectorValue{Samples: []model.InstantSample{{
		Metric:    map[string]string{"__name__": "metric_b", "job": "api", "instance": "a"},
		Timestamp: 1,
		Value:     2,
	}}}

	value, err := ApplyBinaryRuntimeValue(parser.GTR, lhs, rhs, nil, true, EvalParams{Mode: EvalModeInstant, EvaluationTime: time.Unix(1, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	vector := value.(model.VectorValue)
	if len(vector.Samples) != 1 || vector.Samples[0].Value != 0 {
		t.Fatalf("unexpected bool vector-vector result: %#v", vector.Samples)
	}
	if _, ok := vector.Samples[0].Metric["__name__"]; ok {
		t.Fatalf("did not expect __name__ for bool vector-vector result: %#v", vector.Samples[0].Metric)
	}
}

func TestApplyBinaryRuntimeValueVectorVectorRangeMatching(t *testing.T) {
	lhs := model.MatrixValue{Series: []model.RangeSeries{
		{Metric: map[string]string{"__name__": "metric_a", "job": "api", "instance": "a"}, Values: []model.RangePoint{{Timestamp: 10, Value: 1}, {Timestamp: 20, Value: 2}}},
		{Metric: map[string]string{"__name__": "metric_a", "job": "api", "instance": "b"}, Values: []model.RangePoint{{Timestamp: 10, Value: 3}, {Timestamp: 20, Value: 4}}},
	}}
	rhs := model.MatrixValue{Series: []model.RangeSeries{
		{Metric: map[string]string{"__name__": "metric_b", "job": "api"}, Values: []model.RangePoint{{Timestamp: 10, Value: 10}, {Timestamp: 20, Value: 100}}},
	}}
	matching := &parser.VectorMatching{Card: parser.CardManyToOne, On: true, MatchingLabels: []string{"job"}}

	value, err := ApplyBinaryRuntimeValue(parser.MUL, lhs, rhs, matching, false, EvalParams{Mode: EvalModeRange})
	if err != nil {
		t.Fatal(err)
	}
	matrix := value.(model.MatrixValue)
	if len(matrix.Series) != 2 {
		t.Fatalf("expected two range result series, got %#v", matrix.Series)
	}
	if matrix.Series[0].Metric["instance"] != "a" || len(matrix.Series[0].Values) != 2 || matrix.Series[0].Values[0].Value != 10 || matrix.Series[0].Values[1].Value != 200 {
		t.Fatalf("unexpected first range series: %#v", matrix.Series[0])
	}
	if matrix.Series[1].Metric["instance"] != "b" || len(matrix.Series[1].Values) != 2 || matrix.Series[1].Values[0].Value != 30 || matrix.Series[1].Values[1].Value != 400 {
		t.Fatalf("unexpected second range series: %#v", matrix.Series[1])
	}
}

func TestApplyBinaryRuntimeValueVectorVectorFillRightOnMissingRHS(t *testing.T) {
	lhs := model.VectorValue{Samples: []model.InstantSample{{Metric: map[string]string{"job": "api", "instance": "a"}, Timestamp: 1, Value: 2}}}
	rhs := model.VectorValue{Samples: []model.InstantSample{}}
	fillRight := 10.0
	matching := &parser.VectorMatching{Card: parser.CardOneToOne, On: true, MatchingLabels: []string{"job", "instance"}, FillValues: parser.VectorMatchFillValues{RHS: &fillRight}}

	value, err := ApplyBinaryRuntimeValue(parser.ADD, lhs, rhs, matching, false, EvalParams{Mode: EvalModeInstant, EvaluationTime: time.Unix(1, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	vector := value.(model.VectorValue)
	if len(vector.Samples) != 1 || vector.Samples[0].Value != 12 {
		t.Fatalf("unexpected fill_right result: %#v", vector.Samples)
	}
}

func TestApplyBinaryRuntimeValueVectorVectorFillLeftOnMissingLHS(t *testing.T) {
	lhs := model.VectorValue{Samples: []model.InstantSample{}}
	rhs := model.VectorValue{Samples: []model.InstantSample{{Metric: map[string]string{"job": "api", "instance": "a"}, Timestamp: 1, Value: 3}}}
	fillLeft := 5.0
	matching := &parser.VectorMatching{Card: parser.CardOneToOne, On: true, MatchingLabels: []string{"job", "instance"}, FillValues: parser.VectorMatchFillValues{LHS: &fillLeft}}

	value, err := ApplyBinaryRuntimeValue(parser.MUL, lhs, rhs, matching, false, EvalParams{Mode: EvalModeInstant, EvaluationTime: time.Unix(1, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	vector := value.(model.VectorValue)
	if len(vector.Samples) != 1 || vector.Samples[0].Value != 15 {
		t.Fatalf("unexpected fill_left result: %#v", vector.Samples)
	}
}

func TestApplyBinaryRuntimeValueVectorVectorFillBothSides(t *testing.T) {
	lhs := model.VectorValue{Samples: []model.InstantSample{{Metric: map[string]string{"job": "api", "instance": "a"}, Timestamp: 1, Value: 2}}}
	rhs := model.VectorValue{Samples: []model.InstantSample{{Metric: map[string]string{"job": "worker", "instance": "b"}, Timestamp: 1, Value: 4}}}
	fill := 1.0
	matching := &parser.VectorMatching{Card: parser.CardOneToOne, On: true, MatchingLabels: []string{"job", "instance"}, FillValues: parser.VectorMatchFillValues{LHS: &fill, RHS: &fill}}

	value, err := ApplyBinaryRuntimeValue(parser.ADD, lhs, rhs, matching, false, EvalParams{Mode: EvalModeInstant, EvaluationTime: time.Unix(1, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	vector := value.(model.VectorValue)
	if len(vector.Samples) != 2 {
		t.Fatalf("expected two fill() result samples, got %#v", vector.Samples)
	}
	if vector.Samples[0].Value != 3 || vector.Samples[1].Value != 5 {
		t.Fatalf("unexpected fill() values: %#v", vector.Samples)
	}
}

func TestApplyBinaryRuntimeValueVectorVectorAndSetOperator(t *testing.T) {
	lhs := model.VectorValue{Samples: []model.InstantSample{
		{Metric: map[string]string{"job": "api", "instance": "a"}, Timestamp: 1, Value: 1},
		{Metric: map[string]string{"job": "worker", "instance": "b"}, Timestamp: 1, Value: 2},
	}}
	rhs := model.VectorValue{Samples: []model.InstantSample{{Metric: map[string]string{"job": "api", "instance": "x"}, Timestamp: 1, Value: 9}}}
	matching := &parser.VectorMatching{Card: parser.CardManyToMany, On: true, MatchingLabels: []string{"job"}}

	value, err := ApplyBinaryRuntimeValue(parser.LAND, lhs, rhs, matching, false, EvalParams{Mode: EvalModeInstant})
	if err != nil {
		t.Fatal(err)
	}
	vector := value.(model.VectorValue)
	if len(vector.Samples) != 1 || vector.Samples[0].Metric["job"] != "api" {
		t.Fatalf("unexpected and-set result: %#v", vector.Samples)
	}
}

func TestApplyBinaryRuntimeValueVectorVectorOrSetOperator(t *testing.T) {
	lhs := model.VectorValue{Samples: []model.InstantSample{{Metric: map[string]string{"job": "api"}, Timestamp: 1, Value: 1}}}
	rhs := model.VectorValue{Samples: []model.InstantSample{{Metric: map[string]string{"job": "worker"}, Timestamp: 1, Value: 2}}}
	matching := &parser.VectorMatching{Card: parser.CardManyToMany, On: true, MatchingLabels: []string{"job"}}

	value, err := ApplyBinaryRuntimeValue(parser.LOR, lhs, rhs, matching, false, EvalParams{Mode: EvalModeInstant})
	if err != nil {
		t.Fatal(err)
	}
	vector := value.(model.VectorValue)
	if len(vector.Samples) != 2 {
		t.Fatalf("unexpected or-set result: %#v", vector.Samples)
	}
}

func TestApplyBinaryRuntimeValueVectorVectorUnlessSetOperator(t *testing.T) {
	lhs := model.VectorValue{Samples: []model.InstantSample{
		{Metric: map[string]string{"job": "api"}, Timestamp: 1, Value: 1},
		{Metric: map[string]string{"job": "worker"}, Timestamp: 1, Value: 2},
	}}
	rhs := model.VectorValue{Samples: []model.InstantSample{{Metric: map[string]string{"job": "api"}, Timestamp: 1, Value: 3}}}
	matching := &parser.VectorMatching{Card: parser.CardManyToMany, On: true, MatchingLabels: []string{"job"}}

	value, err := ApplyBinaryRuntimeValue(parser.LUNLESS, lhs, rhs, matching, false, EvalParams{Mode: EvalModeInstant})
	if err != nil {
		t.Fatal(err)
	}
	vector := value.(model.VectorValue)
	if len(vector.Samples) != 1 || vector.Samples[0].Metric["job"] != "worker" {
		t.Fatalf("unexpected unless-set result: %#v", vector.Samples)
	}
}

func TestApplyBinaryRuntimeValueVectorVectorSetOperatorRejectsNonManyToMany(t *testing.T) {
	lhs := model.VectorValue{Samples: []model.InstantSample{{Metric: map[string]string{"job": "api"}, Timestamp: 1, Value: 1}}}
	rhs := model.VectorValue{Samples: []model.InstantSample{{Metric: map[string]string{"job": "api"}, Timestamp: 1, Value: 2}}}
	matching := &parser.VectorMatching{Card: parser.CardOneToOne, On: true, MatchingLabels: []string{"job"}}

	_, err := ApplyBinaryRuntimeValue(parser.LAND, lhs, rhs, matching, false, EvalParams{Mode: EvalModeInstant})
	if err == nil {
		t.Fatal("expected non-many-to-many set operator error")
	}
}

func TestApplyBinaryRuntimeValueVectorVectorRejectsImplicitManyToOne(t *testing.T) {
	lhs := model.VectorValue{Samples: []model.InstantSample{
		{Metric: map[string]string{"job": "api", "instance": "a"}, Timestamp: 1, Value: 1},
		{Metric: map[string]string{"job": "api", "instance": "b"}, Timestamp: 1, Value: 2},
	}}
	rhs := model.VectorValue{Samples: []model.InstantSample{{Metric: map[string]string{"job": "api"}, Timestamp: 1, Value: 10}}}
	matching := &parser.VectorMatching{Card: parser.CardOneToOne, On: true, MatchingLabels: []string{"job"}}

	_, err := ApplyBinaryRuntimeValue(parser.MUL, lhs, rhs, matching, false, EvalParams{Mode: EvalModeInstant, EvaluationTime: time.Unix(1, 0).UTC()})
	if err == nil {
		t.Fatal("expected many-to-one explicit cardinality error")
	}
	execErr, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected exec.Error, got %T (%v)", err, err)
	}
	if execErr.Kind != ErrorKindBadData {
		t.Fatalf("expected bad_data kind, got %v (%v)", execErr.Kind, err)
	}
}

func TestApplyBinaryRuntimeValueVectorVectorRejectsDuplicateOneSide(t *testing.T) {
	lhs := model.VectorValue{Samples: []model.InstantSample{{Metric: map[string]string{"job": "api", "instance": "a"}, Timestamp: 1, Value: 1}}}
	rhs := model.VectorValue{Samples: []model.InstantSample{
		{Metric: map[string]string{"job": "api", "instance": "x"}, Timestamp: 1, Value: 10},
		{Metric: map[string]string{"job": "api", "instance": "y"}, Timestamp: 1, Value: 20},
	}}
	matching := &parser.VectorMatching{Card: parser.CardManyToOne, On: true, MatchingLabels: []string{"job"}}

	_, err := ApplyBinaryRuntimeValue(parser.MUL, lhs, rhs, matching, false, EvalParams{Mode: EvalModeInstant, EvaluationTime: time.Unix(1, 0).UTC()})
	if err == nil {
		t.Fatal("expected duplicate one-side vector matching error")
	}
	execErr, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected exec.Error, got %T (%v)", err, err)
	}
	if execErr.Kind != ErrorKindBadData {
		t.Fatalf("expected bad_data kind, got %v (%v)", execErr.Kind, err)
	}
}

func TestApplyBinaryRuntimeValueVectorVectorRejectsNonUniqueGroupingLabels(t *testing.T) {
	lhs := model.VectorValue{Samples: []model.InstantSample{
		{Metric: map[string]string{"job": "api", "instance": "a"}, Timestamp: 1, Value: 1},
		{Metric: map[string]string{"job": "api", "instance": "a"}, Timestamp: 1, Value: 2},
	}}
	rhs := model.VectorValue{Samples: []model.InstantSample{{Metric: map[string]string{"job": "api"}, Timestamp: 1, Value: 10}}}
	matching := &parser.VectorMatching{Card: parser.CardManyToOne, On: true, MatchingLabels: []string{"job"}}

	_, err := ApplyBinaryRuntimeValue(parser.MUL, lhs, rhs, matching, false, EvalParams{Mode: EvalModeInstant, EvaluationTime: time.Unix(1, 0).UTC()})
	if err == nil {
		t.Fatal("expected grouping-label uniqueness error")
	}
	execErr, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected exec.Error, got %T (%v)", err, err)
	}
	if execErr.Kind != ErrorKindBadData {
		t.Fatalf("expected bad_data kind, got %v (%v)", execErr.Kind, err)
	}
}
