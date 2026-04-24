package renderer

import (
	"fmt"

	"ch-observability/internal/promshim/emit"
	logicalpkg "ch-observability/internal/promshim/logical"
	"ch-observability/internal/promshim/native"
	"ch-observability/internal/promshim/native/sqlb"
)

// lowerScalarBuiltin lowers a ScalarBuiltinPlan (time(), pi()) to a
// RenderedQuery. ScalarBuiltinPlan carries no children, so this surface
// is complete: there is no sub-tree that could require falling back to
// another execution tier. The per-sample value expression is produced
// by the shared syntheticSeriesValueSQL helper.
func lowerScalarBuiltin(ctx LoweringCtx, n *logicalpkg.ScalarBuiltinPlan) (RenderedQuery, error) {
	if n == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: lowerScalarBuiltin called with nil")
	}
	rf, err := renderScalarBuiltinLogical(n.Func, ctx.Params)
	if err != nil {
		return RenderedQuery{}, err
	}
	return finalizeRenderedFragment(rf)
}

// renderScalarBuiltinLogical renders a scalar-builtin synthetic series
// (time(), pi()) directly. The per-sample value is dispatched through
// syntheticSeriesValueSQL, which is the canonical SQL source for these
// builtins.
func renderScalarBuiltinLogical(funcName string, params RenderParams) (renderedFragment, error) {
	if !isSupportedNativeScalarBuiltinFuncName(funcName) {
		return renderedFragment{}, fmt.Errorf("renderer: %q is not a supported synthetic scalar builtin", funcName)
	}
	emptyTags := emit.EmptyTagsArray()
	switch params.Mode {
	case native.RenderModeInstant:
		valueSQL, err := syntheticSeriesValueSQL(funcName, "{evaluation_ms:Int64}")
		if err != nil {
			return renderedFragment{}, err
		}
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
		valueSQL, err := syntheticSeriesValueSQL(funcName, "ts_ms")
		if err != nil {
			return renderedFragment{}, err
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

// isSupportedNativeScalarBuiltinFuncName mirrors
// native.isSupportedNativeSyntheticScalarBuiltin; duplicated here so
// the renderer package does not depend on the unexported helper. Keep
// in sync with analysis_support.go until the native package is retired.
func isSupportedNativeScalarBuiltinFuncName(name string) bool {
	switch name {
	case "time", "pi":
		return true
	default:
		return false
	}
}
