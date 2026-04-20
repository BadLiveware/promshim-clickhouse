package model

type RuntimeValue interface{ runtimeValue() }

type ScalarValue struct {
	Timestamp float64
	Value     float64
}

func (ScalarValue) runtimeValue() {}

type InstantSample struct {
	Metric    map[string]string
	Timestamp float64
	Value     float64
}

type VectorValue struct {
	Samples []InstantSample
}

func (VectorValue) runtimeValue() {}

type RangePoint struct {
	Timestamp float64
	Value     float64
}

type RangeSeries struct {
	Metric map[string]string
	Values []RangePoint
}

type MatrixValue struct {
	Series []RangeSeries
}

func (MatrixValue) runtimeValue() {}

type NormalizedLabel struct {
	Name  string
	Value string
}
