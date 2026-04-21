package exec

import "ch-observability/internal/promshim/model"

type LocalRangeOperatorDescriptor struct {
	Name             string
	File             string
	Category         string
	PrometheusRef    string
	SemanticRules    []string
	KnownDivergences []string
}

type LocalRangeOracle func(model.RuntimeValue) (model.VectorValue, error)

var localRangeOperatorInventory = []LocalRangeOperatorDescriptor{
	{
		Name:          "last_over_time",
		File:          "internal/promshim/exec/rangefunc.go",
		Category:      "range_vector",
		PrometheusRef: "promql/functions.go:last_over_time",
		SemanticRules: []string{"returns the last sample in the range window", "drops metric name in the output"},
	},
	{
		Name:          "sum_over_time",
		File:          "internal/promshim/exec/rangefunc.go",
		Category:      "range_vector",
		PrometheusRef: "promql/functions.go:sum_over_time",
		SemanticRules: []string{"sums all non-NaN samples in the range window", "returns NaN if any sample in the range is NaN", "drops metric name in the output"},
	},
	{
		Name:          "avg_over_time",
		File:          "internal/promshim/exec/rangefunc.go",
		Category:      "range_vector",
		PrometheusRef: "promql/functions.go:avg_over_time",
		SemanticRules: []string{"averages non-NaN samples in the range window", "returns NaN if any sample in the range is NaN", "drops metric name in the output"},
	},
	{
		Name:          "max_over_time",
		File:          "internal/promshim/exec/rangefunc.go",
		Category:      "range_vector",
		PrometheusRef: "promql/functions.go:max_over_time",
		SemanticRules: []string{"returns the maximum finite sample in the range window", "returns NaN if any sample in the range is NaN", "drops metric name in the output"},
	},
	{
		Name:          "min_over_time",
		File:          "internal/promshim/exec/rangefunc.go",
		Category:      "range_vector",
		PrometheusRef: "promql/functions.go:min_over_time",
		SemanticRules: []string{"returns the minimum finite sample in the range window", "returns NaN if any sample in the range is NaN", "drops metric name in the output"},
	},
	{
		Name:          "count_over_time",
		File:          "internal/promshim/exec/rangefunc.go",
		Category:      "range_vector",
		PrometheusRef: "promql/functions.go:count_over_time",
		SemanticRules: []string{"counts raw samples in the range window", "drops metric name in the output"},
	},
	{
		Name:          "stddev_over_time",
		File:          "internal/promshim/exec/rangefunc.go",
		Category:      "range_vector",
		PrometheusRef: "promql/functions.go:stddev_over_time",
		SemanticRules: []string{"computes population standard deviation over samples in the range window", "drops metric name in the output"},
	},
	{
		Name:          "stdvar_over_time",
		File:          "internal/promshim/exec/rangefunc.go",
		Category:      "range_vector",
		PrometheusRef: "promql/functions.go:stdvar_over_time",
		SemanticRules: []string{"computes population variance over samples in the range window", "drops metric name in the output"},
	},
	{
		Name:          "present_over_time",
		File:          "internal/promshim/exec/rangefunc.go",
		Category:      "range_vector",
		PrometheusRef: "promql/functions.go:present_over_time",
		SemanticRules: []string{"returns 1 when the range window contains samples", "drops metric name in the output"},
	},
	{
		Name:             "quantile_over_time",
		File:             "internal/promshim/exec/rangefunc.go",
		Category:         "range_vector",
		PrometheusRef:    "promql/functions.go:quantile_over_time",
		SemanticRules:    []string{"computes quantile over raw samples in the range window", "drops metric name in the output"},
		KnownDivergences: []string{"explicit keep-local design note: see .pi/native-sql-lowering-plan/13-keep-local-quantile-over-time.md"},
	},
	{
		Name:             "rate",
		File:             "internal/promshim/exec/rate.go",
		Category:         "counter",
		PrometheusRef:    "promql/functions.go:rate",
		SemanticRules:    []string{"uses the full range window", "handles counter resets", "drops metric name in the output"},
		KnownDivergences: []string{"native-lowering tracking note: local semantics remain the correctness oracle until differential promotion is complete"},
	},
	{
		Name:             "irate",
		File:             "internal/promshim/exec/rate.go",
		Category:         "counter",
		PrometheusRef:    "promql/functions.go:irate",
		SemanticRules:    []string{"uses the last two non-NaN samples", "handles counter resets", "drops metric name in the output"},
		KnownDivergences: []string{"native-lowering tracking note: local semantics remain the correctness oracle until differential promotion is complete"},
	},
	{
		Name:             "increase",
		File:             "internal/promshim/exec/increase.go",
		Category:         "counter",
		PrometheusRef:    "promql/functions.go:increase",
		SemanticRules:    []string{"uses delta across the range window", "handles counter resets", "drops metric name in the output"},
		KnownDivergences: []string{"native-lowering tracking note: local semantics remain the correctness oracle until differential promotion is complete"},
	},
	{
		Name:             "delta",
		File:             "internal/promshim/exec/delta.go",
		Category:         "counter",
		PrometheusRef:    "promql/functions.go:delta",
		SemanticRules:    []string{"uses first-to-last sample difference", "drops metric name in the output"},
		KnownDivergences: []string{"left-edge inclusion compatibility is guarded by the delegated-range padding workaround from commit 0d44c2b"},
	},
	{
		Name:             "idelta",
		File:             "internal/promshim/exec/delta.go",
		Category:         "counter",
		PrometheusRef:    "promql/functions.go:idelta",
		SemanticRules:    []string{"uses the last two samples in the range window", "drops metric name in the output"},
		KnownDivergences: []string{"left-edge inclusion compatibility is guarded by the delegated-range padding workaround from commit 0d44c2b"},
	},
	{
		Name:             "changes",
		File:             "internal/promshim/exec/changes.go",
		Category:         "counter",
		PrometheusRef:    "promql/functions.go:changes",
		SemanticRules:    []string{"counts transitions across the range window", "propagates NaN presence", "drops metric name in the output"},
		KnownDivergences: []string{"left-edge inclusion compatibility is guarded by the delegated-range padding workaround from commit 0d44c2b"},
	},
	{
		Name:             "deriv",
		File:             "internal/promshim/exec/deriv.go",
		Category:         "counter",
		PrometheusRef:    "promql/functions.go:deriv",
		SemanticRules:    []string{"computes slope from timestamps and values in the range window", "propagates NaN presence", "drops metric name in the output"},
		KnownDivergences: []string{"left-edge inclusion compatibility is guarded by the delegated-range padding workaround from commit 0d44c2b"},
	},
}

var localRangeOracles = map[string]LocalRangeOracle{
	"last_over_time": ApplyLastOverTimeInstant,
	"sum_over_time": func(input model.RuntimeValue) (model.VectorValue, error) {
		return ApplyRangeFunctionInstant("sum_over_time", input)
	},
	"avg_over_time": func(input model.RuntimeValue) (model.VectorValue, error) {
		return ApplyRangeFunctionInstant("avg_over_time", input)
	},
	"max_over_time": func(input model.RuntimeValue) (model.VectorValue, error) {
		return ApplyRangeFunctionInstant("max_over_time", input)
	},
	"min_over_time": func(input model.RuntimeValue) (model.VectorValue, error) {
		return ApplyRangeFunctionInstant("min_over_time", input)
	},
	"count_over_time": func(input model.RuntimeValue) (model.VectorValue, error) {
		return ApplyRangeFunctionInstant("count_over_time", input)
	},
	"stddev_over_time": func(input model.RuntimeValue) (model.VectorValue, error) {
		return ApplyRangeFunctionInstant("stddev_over_time", input)
	},
	"stdvar_over_time": func(input model.RuntimeValue) (model.VectorValue, error) {
		return ApplyRangeFunctionInstant("stdvar_over_time", input)
	},
	"present_over_time": func(input model.RuntimeValue) (model.VectorValue, error) {
		return ApplyRangeFunctionInstant("present_over_time", input)
	},
	"rate":     ApplyRate,
	"irate":    ApplyIRate,
	"increase": ApplyIncreaseInstant,
	"delta":    ApplyDelta,
	"idelta":   ApplyIDelta,
	"changes":  ApplyChanges,
	"deriv":    ApplyDeriv,
}

func LocalRangeOperatorInventory() []LocalRangeOperatorDescriptor {
	out := make([]LocalRangeOperatorDescriptor, len(localRangeOperatorInventory))
	copy(out, localRangeOperatorInventory)
	for i := range out {
		out[i].SemanticRules = append([]string(nil), out[i].SemanticRules...)
		out[i].KnownDivergences = append([]string(nil), out[i].KnownDivergences...)
	}
	return out
}

func LocalRangeOracleForTest(name string) (LocalRangeOracle, bool) {
	oracle, ok := localRangeOracles[name]
	return oracle, ok
}

func ApplyLocalQuantileOverTimeOracleForTest(quantile float64, input model.RuntimeValue) (model.VectorValue, error) {
	return ApplyQuantileOverTime(quantile, input)
}
