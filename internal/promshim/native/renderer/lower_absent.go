package renderer

import (
	"fmt"
	"strconv"

	logicalpkg "github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/native"
)

// lowerAbsent dispatches to either the AbsentPlan or AbsentOverTimePlan
// direct lowerer. Both fully lower from the logical tree without reading
// ctx.NativeAnalysis — Lower on the child may return
// errUnsupportedLowerNode, which bubbles up so the whole query falls
// back to the next execution tier.
func lowerAbsent(ctx LoweringCtx, n logicalpkg.Node) (RenderedQuery, error) {
	if n == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: lowerAbsent called with nil")
	}
	if ctx.Analysis == nil || ctx.Analysis.InfoFor(n) == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: absent missing logical analysis")
	}
	switch p := n.(type) {
	case *logicalpkg.AbsentPlan:
		return lowerAbsentPlanDirect(ctx, p)
	case *logicalpkg.AbsentOverTimePlan:
		return lowerAbsentOverTimePlanDirect(ctx, p)
	default:
		return RenderedQuery{}, fmt.Errorf("renderer: lowerAbsent called with unexpected node type %T", n)
	}
}

// lowerAbsentPlanDirect lowers the child via Lower (bubbling the
// fallback sentinel), namespaces the result under "absent_child", and
// delegates to renderAbsentFromNamespacedChild.
func lowerAbsentPlanDirect(ctx LoweringCtx, n *logicalpkg.AbsentPlan) (RenderedQuery, error) {
	if n.Child == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: absent missing child")
	}
	childRQ, err := Lower(ctx, n.Child)
	if err != nil {
		return RenderedQuery{}, err
	}
	childSQL, childParams, err := namespaceRenderedQuery(trimRenderedQuerySQL(childRQ.SQL), childRQ.QueryParams, "absent_child")
	if err != nil {
		return RenderedQuery{}, err
	}
	tagsSQL := outputMetricTagsSQL(n.OutputMetric)
	rf, err := renderAbsentFromNamespacedChild(tagsSQL, childSQL, childParams, ctx.Params)
	if err != nil {
		return RenderedQuery{}, err
	}
	return finalizeRenderedFragment(rf)
}

// lowerAbsentOverTimePlanDirect renders AbsentOverTimePlan. In instant
// mode the child is lowered with current params and wrapped in a
// zero-sample probe. In range mode, rendering goes through
// renderAbsentOverTimeWindowedSourceLogical,
// which dispatches on the child's logical kind: LeafExprPlan with a range
// selector emits a windowed-arrays source over the raw selector samples;
// SubqueryPlan emits a windowed-arrays source over the subquery's rendered
// step-grid.
func lowerAbsentOverTimePlanDirect(ctx LoweringCtx, n *logicalpkg.AbsentOverTimePlan) (RenderedQuery, error) {
	if n.Child == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: absent_over_time missing child")
	}
	tagsSQL := outputMetricTagsSQL(n.OutputMetric)
	switch ctx.Params.Mode {
	case native.RenderModeInstant:
		childRQ, err := Lower(ctx, n.Child)
		if err != nil {
			return RenderedQuery{}, err
		}
		childSQL, childParams, err := namespaceRenderedQuery(trimRenderedQuerySQL(childRQ.SQL), childRQ.QueryParams, "absent_child")
		if err != nil {
			return RenderedQuery{}, err
		}
		queryParams := map[string]string{}
		mergeRenderedQueryParams(queryParams, childParams)
		queryParams["param_evaluation_ms"] = strconv.FormatInt(ctx.Params.EvaluationTimeMS, 10)
		sql := "SELECT " + tagsSQL + " AS tags, fromUnixTimestamp64Milli({evaluation_ms:Int64}) AS timestamp, toFloat64(1) AS value FROM (SELECT count() AS sample_count FROM (" + childSQL + ") AS absent_child ARRAY JOIN absent_child.time_series AS point) AS probe WHERE probe.sample_count = 0"
		return finalizeRenderedFragment(renderedFragment{RawSQL: sql, ExtraParams: queryParams})
	case native.RenderModeRange:
		windowedSQL, childParams, err := renderAbsentOverTimeWindowedSourceLogical(ctx, n.Child, ctx.Params)
		if err != nil {
			return RenderedQuery{}, err
		}
		queryParams := map[string]string{}
		queryParams["param_start_ms"] = strconv.FormatInt(ctx.Params.StartMS, 10)
		queryParams["param_end_ms"] = strconv.FormatInt(ctx.Params.EndMS, 10)
		queryParams["param_step_ms"] = strconv.FormatInt(ctx.Params.StepMS, 10)
		mergeRenderedQueryParams(queryParams, childParams)
		presenceSQL := "SELECT eval_ts AS timestamp, sum(length(window_series)) AS sample_count FROM (" + trimRenderedQuerySQL(windowedSQL) + ") AS absent_windows GROUP BY eval_ts"
		return finalizeRenderedFragment(renderedFragment{RawSQL: trimRenderedQuerySQL(buildRangeAbsentSeriesSQL(tagsSQL, presenceSQL)), ExtraParams: queryParams})
	default:
		return RenderedQuery{}, fmt.Errorf("unknown render mode %q", ctx.Params.Mode)
	}
}

// renderAbsentOverTimeWindowedSourceLogical is the logical-node counterpart
// of renderAbsentOverTimeWindowedSource. It dispatches on the child's kind,
// lowers the child in range mode with bounds derived from that kind's
// lookback/offset, namespaces the result, and wraps it in
// buildWindowedArraysSourceSQL. Kinds beyond LeafExprPlan/SubqueryPlan bubble
// errUnsupportedLowerNode so the whole-query fallback takes over.
func renderAbsentOverTimeWindowedSourceLogical(ctx LoweringCtx, child logicalpkg.Node, params RenderParams) (string, map[string]string, error) {
	switch c := child.(type) {
	case *logicalpkg.LeafExprPlan:
		selector, err := native.BuildSelectorSource(c.Expr)
		if err != nil {
			return "", nil, fmt.Errorf("renderer: absent_over_time leaf selector analysis failed: %w", err)
		}
		if selector == nil || selector.Kind != native.SelectorKindRangeVector {
			return "", nil, errUnsupportedLowerNode
		}
		lookbackMS := selector.Lookback.Milliseconds()
		offsetMS := selector.Offset.Milliseconds()
		childCtx := LoweringCtx{
			Config:             ctx.Config,
			Analysis:           ctx.Analysis,
			NativeAnalysis:     ctx.NativeAnalysis,
			OptimizationReport: ctx.OptimizationReport,
			Params: RenderParams{
				Mode:                native.RenderModeRange,
				StartMS:             params.StartMS,
				EndMS:               params.EndMS,
				StepMS:              params.StepMS,
				RequiredStartMS:     params.StartMS - offsetMS - lookbackMS,
				RequiredEndMS:       params.EndMS - offsetMS,
				ResolveSourcePromQL: params.ResolveSourcePromQL,
			},
		}
		rendered, err := Lower(childCtx, c)
		if err != nil {
			return "", nil, err
		}
		namespacedSQL, namespacedParams, err := namespaceRenderedQuery(trimRenderedQuerySQL(rendered.SQL), rendered.QueryParams, "absent_window_child")
		if err != nil {
			return "", nil, err
		}
		windowedSQL, err := buildWindowedArraysSourceSQL(namespacedSQL, "absent_over_time", params.StartMS, params.EndMS, params.StepMS, lookbackMS, offsetMS)
		if err != nil {
			return "", nil, err
		}
		return windowedSQL, namespacedParams, nil
	case *logicalpkg.SubqueryPlan:
		childCtx := LoweringCtx{
			Config:             ctx.Config,
			Analysis:           ctx.Analysis,
			NativeAnalysis:     ctx.NativeAnalysis,
			OptimizationReport: ctx.OptimizationReport,
			Params: RenderParams{
				Mode:                native.RenderModeRange,
				StartMS:             params.StartMS,
				EndMS:               params.EndMS,
				StepMS:              params.StepMS,
				RequiredStartMS:     params.RequiredStartMS,
				RequiredEndMS:       params.RequiredEndMS,
				ResolveSourcePromQL: params.ResolveSourcePromQL,
			},
		}
		rendered, err := Lower(childCtx, c)
		if err != nil {
			return "", nil, err
		}
		namespacedSQL, namespacedParams, err := namespaceRenderedQuery(trimRenderedQuerySQL(rendered.SQL), rendered.QueryParams, "absent_window_child")
		if err != nil {
			return "", nil, err
		}
		windowedSQL, err := buildWindowedArraysSourceSQL(namespacedSQL, "absent_over_time", params.StartMS, params.EndMS, params.StepMS, c.Range.Milliseconds(), c.Offset.Milliseconds())
		if err != nil {
			return "", nil, err
		}
		return windowedSQL, namespacedParams, nil
	default:
		return "", nil, errUnsupportedLowerNode
	}
}
