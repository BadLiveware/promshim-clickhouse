package renderer

import (
	"ch-observability/internal/promshim/emit"
	"ch-observability/internal/promshim/native"
	"fmt"

	"ch-observability/internal/promshim/native/sqlb"
)

// syntheticSeriesValueSQL returns the SQL expression that computes the
// per-sample value for a named synthetic function. tsMSExpr is the SQL
// expression that evaluates to the sample timestamp in milliseconds (e.g.
// "ts_ms" for range mode or "{evaluation_ms:Int64}" for instant mode).
//
// The "literal" case is only reachable as an error guard — vector(1) and
// other scalar literals flow through lowerScalarLiteral /
// renderScalarLiteralFragment directly, never via this helper.
func syntheticSeriesValueSQL(funcName string, tsMSExpr string) (string, error) {
	utcTs := "toTimeZone(fromUnixTimestamp64Milli(" + tsMSExpr + "), 'UTC')"
	switch funcName {
	case "literal":
		// Literals carry a float value that funcName alone cannot encode;
		// callers that reach this branch have a bug.
		return "", fmt.Errorf("synthetic series function %q must be handled via renderScalarLiteralFragment, not syntheticSeriesValueSQL", funcName)
	case "pi":
		return "toFloat64(3.141592653589793)", nil
	case "time":
		return "toFloat64(" + tsMSExpr + ") / 1000.0", nil
	case "minute":
		return "toFloat64(toMinute(" + utcTs + "))", nil
	case "hour":
		return "toFloat64(toHour(" + utcTs + "))", nil
	case "day_of_week":
		return "toFloat64(modulo(toDayOfWeek(" + utcTs + "), 7))", nil
	case "day_of_month":
		return "toFloat64(toDayOfMonth(" + utcTs + "))", nil
	case "day_of_year":
		return "toFloat64(toDayOfYear(" + utcTs + "))", nil
	case "days_in_month":
		return "toFloat64(toDaysInMonth(" + utcTs + "))", nil
	case "month":
		return "toFloat64(toMonth(" + utcTs + "))", nil
	case "year":
		return "toFloat64(toYear(" + utcTs + "))", nil
	default:
		return "", fmt.Errorf("synthetic series function %q is not implemented yet", funcName)
	}
}

// renderScalarConvertFromSource builds the scalar-convert outer SELECT over a
// pre-rendered child source. The direct Lower path (lowerScalarConvert) is
// the sole caller.
func renderScalarConvertFromSource(childSource sqlb.Source, childParams map[string]string, params RenderParams) (renderedFragment, error) {
	emptyTags := emit.EmptyTagsArray()
	switch params.Mode {
	case native.RenderModeInstant:
		evalParam := sqlb.Param{Name: "evaluation_ms", Type: "Int64", V: params.EvaluationTimeMS}
		valueExpr := sqlb.Call{Name: "if", Args: []sqlb.Expr{
			sqlb.RawLit{V: "count() = 1"},
			sqlb.Call{Name: "any", Args: []sqlb.Expr{sqlb.Ident("value")}},
			sqlb.RawLit{V: "nan"},
		}}
		return renderedFragment{
			Select: &sqlb.Select{
				Columns: []sqlb.ColExpr{
					{Expr: emptyTags, Alias: "tags"},
					{Expr: sqlb.Call{Name: "fromUnixTimestamp64Milli", Args: []sqlb.Expr{evalParam}}, Alias: "timestamp"},
					{Expr: valueExpr, Alias: "value"},
				},
				From: childSource,
			},
			ExtraParams: childParams,
		}, nil
	case native.RenderModeRange:
		startParam := sqlb.Param{Name: "start_ms", Type: "Int64", V: params.StartMS}
		endParam := sqlb.Param{Name: "end_ms", Type: "Int64", V: params.EndMS}
		stepParam := sqlb.Param{Name: "step_ms", Type: "Int64", V: params.StepMS}

		gridTimestampExpr := sqlb.Call{Name: "arrayJoin", Args: []sqlb.Expr{
			sqlb.Call{Name: "arrayMap", Args: []sqlb.Expr{
				sqlb.Lambda{Params: []sqlb.Ident{"ts_ms"}, Body: sqlb.Call{Name: "fromUnixTimestamp64Milli", Args: []sqlb.Expr{sqlb.Ident("ts_ms")}}},
				sqlb.Call{Name: "range", Args: []sqlb.Expr{startParam, sqlb.Binary{Op: "+", L: endParam, R: stepParam}, stepParam}},
			}},
		}}
		grid := &sqlb.Select{Columns: []sqlb.ColExpr{{Expr: gridTimestampExpr, Alias: "timestamp"}}}

		scalarValues := &sqlb.Select{
			Columns: []sqlb.ColExpr{
				{Expr: sqlb.RawLit{V: "point.1"}, Alias: "timestamp"},
				{Expr: sqlb.Call{Name: "count"}, Alias: "sample_count"},
				{Expr: sqlb.Call{Name: "any", Args: []sqlb.Expr{sqlb.RawLit{V: "point.2"}}}, Alias: "any_value"},
			},
			From: sqlb.ArrayJoin{
				Base:  childSource,
				Expr:  sqlb.RawLit{V: "scalar_child.time_series"},
				Alias: "point",
			},
			GroupBy: []sqlb.Expr{sqlb.RawLit{V: "point.1"}},
		}

		middleValue := sqlb.Call{Name: "if", Args: []sqlb.Expr{
			sqlb.RawLit{V: "ifNull(scalar_values.sample_count, 0) = 1"},
			sqlb.RawLit{V: "scalar_values.any_value"},
			sqlb.RawLit{V: "nan"},
		}}
		middle := &sqlb.Select{
			Columns: []sqlb.ColExpr{
				{Expr: sqlb.RawLit{V: "grid.timestamp"}, Alias: "timestamp"},
				{Expr: middleValue, Alias: "value"},
			},
			From: sqlb.Join{
				Kind:  "LEFT",
				Left:  sqlb.SubSelect{S: grid, Alias: "grid"},
				Right: sqlb.SubSelect{S: scalarValues, Alias: "scalar_values"},
				On:    sqlb.RawLit{V: "scalar_values.timestamp = grid.timestamp"},
			},
			OrderBy: []sqlb.OrderExpr{{Expr: sqlb.Ident("timestamp")}},
		}

		timeSeriesExpr := sqlb.Call{Name: "arraySort", Args: []sqlb.Expr{
			sqlb.RawLit{V: "item -> item.1"},
			sqlb.Call{Name: "groupArray", Args: []sqlb.Expr{sqlb.Tuple{Elems: []sqlb.Expr{sqlb.Ident("timestamp"), sqlb.Ident("value")}}}},
		}}
		return renderedFragment{
			Select: &sqlb.Select{
				Columns: []sqlb.ColExpr{
					{Expr: emptyTags, Alias: "tags"},
					{Expr: timeSeriesExpr, Alias: "time_series"},
				},
				From: sqlb.SubSelect{S: middle},
			},
			ExtraParams: childParams,
		}, nil
	default:
		return renderedFragment{}, fmt.Errorf("unknown render mode %q", params.Mode)
	}
}

