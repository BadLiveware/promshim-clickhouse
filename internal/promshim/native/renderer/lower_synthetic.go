package renderer

import (
	"fmt"

	"github.com/BadLiveware/promshim-ch/internal/promshim/emit"
	logicalpkg "github.com/BadLiveware/promshim-ch/internal/promshim/logical"
	"github.com/BadLiveware/promshim-ch/internal/promshim/native"
	"github.com/BadLiveware/promshim-ch/internal/promshim/native/sqlb"
)

// lowerPointwiseFunction lowers a PointwiseFunctionPlan to a
// RenderedQuery. It dispatches first to clamp variants (clamp,
// clamp_min, clamp_max) handled by lowerClamp, then to zero-arg
// synthetic date/time functions (minute, hour, day_of_week,
// day_of_month, day_of_year, days_in_month, month, year) handled
// directly via renderSyntheticDateFragment. All other Funcs return
// errUnsupportedLowerNode so the caller falls back to Fragment
// rendering for the whole query.
//
// SQL output is byte-identical to the corresponding Fragment path for
// each kind — both paths call through to the shared syntheticSeriesValueSQL
// helper so whitespace, column order, and parameter naming stay in lockstep.
func lowerPointwiseFunction(ctx LoweringCtx, n *logicalpkg.PointwiseFunctionPlan) (RenderedQuery, error) {
	if n == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: lowerPointwiseFunction called with nil node")
	}
	// Clamp dispatch: clamp/clamp_min/clamp_max are lowered by lowerClamp
	// which reads the pre-computed Fragment from NativeAnalysis. Check this
	// before the synthetic-date path so a zero-arg hypothetical clamp never
	// accidentally falls through.
	if isClampFuncName(n.Func) {
		return lowerClamp(ctx, n)
	}
	// Unary source-expression shape (e.g. abs(foo), sqrt(foo)): analysis
	// produced a SourceExprView on LoweringInfo carrying Selector +
	// ValueExpr/TagsExpr. Render off that view directly via
	// renderSourceExprView (the pure-logical analog of renderSourceFragment)
	// so Lower no longer dereferences info.Fragment. Keeps Lower off the
	// sentinel path so tier-3 callers no longer need a Fragment fallback.
	//
	// Anchored range handling mirrors renderFragment's top-level gate (see
	// renderer.go:54): when the outer mode is Range and the subtree pins
	// evaluation to a fixed temporal anchor (@/start/end), the instant
	// render at the anchor is broadcast across the range step grid via
	// renderAnchoredRangeSourceExprView. This replaces renderFragment's
	// dispatch through renderAnchoredRangeFragment for this shape.
	if n.Child != nil {
		if ctx.NativeAnalysis == nil {
			return RenderedQuery{}, errUnsupportedLowerNode
		}
		info := ctx.NativeAnalysis.InfoFor(n)
		if info == nil || info.SourceExpr == nil {
			return RenderedQuery{}, errUnsupportedLowerNode
		}
		var rf renderedFragment
		var err error
		if ctx.Params.Mode == native.RenderModeRange && info.Shape.HasFixedTemporalAnchor {
			anchorMS, ok := logicalResolvedAnchorTimeMS(n, native.OptimizationContext{
				Mode:             native.RenderModeRange,
				EvaluationTimeMS: ctx.Params.EvaluationTimeMS,
				StartMS:          ctx.Params.StartMS,
				EndMS:            ctx.Params.EndMS,
				StepMS:           ctx.Params.StepMS,
			})
			if !ok {
				return RenderedQuery{}, fmt.Errorf("anchored native range rendering requires a resolved anchor")
			}
			rf, err = renderAnchoredRangeSourceExprView(ctx.Config, info.SourceExpr, info.OutputKind, ctx.Params, anchorMS)
		} else {
			rf, err = renderSourceExprView(ctx.Config, info.SourceExpr, ctx.Params)
		}
		if err != nil {
			return RenderedQuery{}, err
		}
		return finalizeRenderedFragment(rf)
	}
	// Zero-arg: only synthetic date functions lower here.
	if !isSupportedNativeSyntheticDateFuncName(n.Func) {
		return RenderedQuery{}, errUnsupportedLowerNode
	}
	if len(n.ParamChildren) != 0 {
		return RenderedQuery{}, fmt.Errorf("renderer: synthetic pointwise %q must not carry param children", n.Func)
	}
	rf, err := renderSyntheticDateFragment(n.Func, ctx.Params)
	if err != nil {
		return RenderedQuery{}, err
	}
	return finalizeRenderedFragment(rf)
}

// renderSyntheticDateFragment renders a synthetic date function directly
// without constructing an intermediate NativeFragment. It produces
// byte-identical SQL to what renderSyntheticFragment produces for
// FragmentKindSyntheticSeries / OutputKindInstantVector with the same
// funcName, because both paths call through to the shared
// syntheticSeriesValueSQL helper.
func renderSyntheticDateFragment(funcName string, params RenderParams) (renderedFragment, error) {
	if !isSupportedNativeSyntheticDateFuncName(funcName) {
		return renderedFragment{}, fmt.Errorf("renderer: %q is not a supported synthetic date function", funcName)
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

// isSupportedNativeSyntheticDateFuncName mirrors
// native.isSupportedNativeSyntheticDateFunction; duplicated here so the
// renderer package does not depend on the unexported helper. Keep in
// sync with analysis_support.go until the native package is retired.
func isSupportedNativeSyntheticDateFuncName(name string) bool {
	switch name {
	case "minute", "hour", "day_of_week", "day_of_month", "day_of_year", "days_in_month", "month", "year":
		return true
	default:
		return false
	}
}
