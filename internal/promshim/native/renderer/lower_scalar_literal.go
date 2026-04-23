package renderer

import (
	"fmt"

	"ch-observability/internal/promshim/emit"
	logicalpkg "ch-observability/internal/promshim/logical"
	"ch-observability/internal/promshim/native"
	"ch-observability/internal/promshim/native/sqlb"
	"ch-observability/internal/promshim/storage"
)

func lowerScalarLiteral(ctx LoweringCtx, n *logicalpkg.ScalarLiteralPlan) (RenderedQuery, error) {
	if n == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: lowerScalarLiteral called with nil")
	}
	rf, err := renderScalarLiteralFragment(n.Value, ctx.Params)
	if err != nil {
		return RenderedQuery{}, err
	}
	return finalizeRenderedFragment(rf)
}

func renderScalarLiteralFragment(value float64, params RenderParams) (renderedFragment, error) {
	valueSQL := storage.NativeFloatLiteral(value)
	emptyTags := emit.EmptyTagsArray()

	switch params.Mode {
	case native.RenderModeInstant:
		evalParam := sqlb.Param{Name: "evaluation_ms", Type: "Int64", V: params.EvaluationTimeMS}
		return renderedFragment{Select: &sqlb.Select{
			Columns: []sqlb.ColExpr{
				{Expr: emptyTags, Alias: "tags"},
				{Expr: sqlb.Call{Name: "fromUnixTimestamp64Milli", Args: []sqlb.Expr{evalParam}}, Alias: "timestamp"},
				{Expr: sqlb.RawLit{V: valueSQL}, Alias: "value"},
			},
		}}, nil

	case native.RenderModeRange:
		if params.StepMS <= 0 {
			return renderedFragment{}, fmt.Errorf("synthetic range render requires a positive step")
		}
		startParam := sqlb.Param{Name: "start_ms", Type: "Int64", V: params.StartMS}
		endParam := sqlb.Param{Name: "end_ms", Type: "Int64", V: params.EndMS}
		stepParam := sqlb.Param{Name: "step_ms", Type: "Int64", V: params.StepMS}
		timeSeriesExpr := sqlb.Call{Name: "arrayMap", Args: []sqlb.Expr{
			sqlb.Lambda{
				Params: []sqlb.Ident{"ts_ms"},
				Body: sqlb.Tuple{Elems: []sqlb.Expr{
					sqlb.Call{Name: "fromUnixTimestamp64Milli", Args: []sqlb.Expr{sqlb.Ident("ts_ms")}},
					sqlb.RawLit{V: valueSQL},
				}},
			},
			sqlb.Call{Name: "range", Args: []sqlb.Expr{
				startParam,
				sqlb.Binary{Op: "+", L: endParam, R: stepParam},
				stepParam,
			}},
		}}
		return renderedFragment{Select: &sqlb.Select{
			Columns: []sqlb.ColExpr{
				{Expr: emptyTags, Alias: "tags"},
				{Expr: timeSeriesExpr, Alias: "time_series"},
			},
		}}, nil

	default:
		return renderedFragment{}, fmt.Errorf("unknown render mode %q", params.Mode)
	}
}
