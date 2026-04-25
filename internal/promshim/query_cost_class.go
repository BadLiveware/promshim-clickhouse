package promshim

import (
	"strings"
	"time"

	httpapi "github.com/BadLiveware/promshim-clickhouse/internal/promshim/httpapi"
	"github.com/prometheus/prometheus/promql/parser"
)

type queryCostTiming struct {
	Endpoint string
	Start    time.Time
	End      time.Time
	Step     time.Duration
}

func classifyQueryCost(expr parser.Expr, timing queryCostTiming, strictStrategy string) httpapi.QueryCostClass {
	class := httpapi.QueryCostClass{
		Endpoint:           timing.Endpoint,
		Family:             queryFamily(expr, timing.Endpoint),
		RootStrategyStrict: strictStrategy,
		OutputKind:         outputKind(expr, timing.Endpoint),
		StepMS:             timing.Step.Milliseconds(),
	}
	walkQueryCost(expr, &class)
	if timing.Endpoint == "query_range" && timing.Step > 0 && !timing.End.Before(timing.Start) {
		class.RangePointsPerSeries = int64(timing.End.Sub(timing.Start)/timing.Step) + 1
		if class.SelectorCount > 0 {
			class.EstimatedOutputPoints = int64(class.SelectorCount) * class.RangePointsPerSeries
		} else {
			class.EstimatedOutputPoints = class.RangePointsPerSeries
		}
	}
	if class.HasRangeFunction && class.LookbackMS > 0 && timing.Step > 0 {
		class.OverlapSlots = float64(time.Duration(class.LookbackMS)*time.Millisecond) / float64(timing.Step)
	}
	if class.SelectorCount > 0 {
		class.LocalRoundTrips = class.SelectorCount
	}
	switch strictStrategy {
	case "native_sql", "delegated_promql":
		class.NativeRoundTrips = 1
	}
	return class
}

func walkQueryCost(expr parser.Expr, class *httpapi.QueryCostClass) {
	if expr == nil || class == nil {
		return
	}
	switch node := expr.(type) {
	case *parser.VectorSelector:
		class.SelectorCount++
		lookback := node.OriginalOffset
		if lookback > 0 && lookback.Milliseconds() > class.LookbackMS {
			class.LookbackMS = lookback.Milliseconds()
		}
	case *parser.MatrixSelector:
		class.HasRangeFunction = true
		if node.Range.Milliseconds() > class.LookbackMS {
			class.LookbackMS = node.Range.Milliseconds()
		}
		walkQueryCost(node.VectorSelector, class)
	case *parser.Call:
		name := strings.ToLower(node.Func.Name)
		if strings.Contains(name, "histogram") {
			class.HasHistogram = true
		}
		if name == "label_replace" || name == "label_join" || name == "info" {
			class.HasLabelMutation = true
		}
		if isSelectionAggregation(name) {
			class.HasSelectionAgg = true
		}
		for _, arg := range node.Args {
			walkQueryCost(arg, class)
		}
	case *parser.AggregateExpr:
		class.HasAggregation = true
		class.HasSelectionAgg = class.HasSelectionAgg || isSelectionAggregation(strings.ToLower(node.Op.String()))
		class.DropsAllLabels = len(node.Grouping) == 0 && !node.Without
		walkQueryCost(node.Expr, class)
	case *parser.BinaryExpr:
		if node.VectorMatching != nil {
			class.HasVectorJoin = true
		}
		walkQueryCost(node.LHS, class)
		walkQueryCost(node.RHS, class)
	case *parser.SubqueryExpr:
		class.HasSubquery = true
		if node.Range.Milliseconds() > class.LookbackMS {
			class.LookbackMS = node.Range.Milliseconds()
		}
		walkQueryCost(node.Expr, class)
	case *parser.ParenExpr:
		walkQueryCost(node.Expr, class)
	case *parser.UnaryExpr:
		walkQueryCost(node.Expr, class)
	}
}

func queryFamily(expr parser.Expr, endpoint string) string {
	base := queryFamilyBase(expr)
	if endpoint == "query_range" {
		switch base {
		case "selector":
			return "range_selector"
		case "rate", "increase", "range_function":
			return "range_" + base
		}
	}
	return base
}

func queryFamilyBase(expr parser.Expr) string {
	switch node := expr.(type) {
	case *parser.VectorSelector:
		return "selector"
	case *parser.MatrixSelector:
		return "range_selector"
	case *parser.Call:
		name := strings.ToLower(node.Func.Name)
		switch {
		case name == "histogram_quantile":
			return "histogram_quantile"
		case name == "label_replace" || name == "label_join" || name == "info":
			return "label_mutation"
		case name == "rate" || name == "irate":
			return "rate"
		case name == "increase":
			return "increase"
		case isRangeFunction(name):
			return "range_function"
		default:
			return name
		}
	case *parser.AggregateExpr:
		if isSelectionAggregation(strings.ToLower(node.Op.String())) {
			return "selection_aggregation"
		}
		return "aggregation"
	case *parser.BinaryExpr:
		if node.VectorMatching != nil {
			return "vector_match"
		}
		return "binary"
	case *parser.SubqueryExpr:
		return "subquery"
	case *parser.ParenExpr:
		return queryFamilyBase(node.Expr)
	case *parser.UnaryExpr:
		return queryFamilyBase(node.Expr)
	case *parser.NumberLiteral:
		return "scalar"
	case *parser.StringLiteral:
		return "string"
	default:
		return "unknown"
	}
}

func outputKind(expr parser.Expr, endpoint string) string {
	if endpoint == "query_range" {
		return "matrix"
	}
	if expr == nil {
		return "unknown"
	}
	return string(expr.Type())
}

func isSelectionAggregation(name string) bool {
	switch name {
	case "topk", "bottomk", "limitk", "limit_ratio":
		return true
	default:
		return false
	}
}

func isRangeFunction(name string) bool {
	switch name {
	case "avg_over_time", "min_over_time", "max_over_time", "sum_over_time", "count_over_time", "last_over_time", "present_over_time", "quantile_over_time", "stddev_over_time", "stdvar_over_time", "delta", "idelta", "deriv", "predict_linear", "changes", "resets":
		return true
	default:
		return false
	}
}
