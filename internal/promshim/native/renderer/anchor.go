package renderer

import (
	"fmt"
	"strconv"

	"ch-observability/internal/promshim/native"
	"ch-observability/internal/promshim/storage"
)

// wrapInstantAsAnchoredRange wraps a pre-rendered instant child into the
// anchored-range broadcast SQL. Used by the pure-logical Lower-side anchor
// path (see lowerAnchoredRangeFromSourceExpr).
//
// Caller guarantees: the child fragment's OutputKind is InstantVector
// (OutputKindRangeMatrix short-circuits to returning the instant render
// unchanged, before this helper is reached).
func wrapInstantAsAnchoredRange(instantRendered renderedFragment, params RenderParams) (renderedFragment, error) {
	if params.StepMS <= 0 {
		return renderedFragment{}, fmt.Errorf("anchored native range rendering requires a positive outer step")
	}
	finalized, err := finalizeRenderedFragment(instantRendered)
	if err != nil {
		return renderedFragment{}, err
	}
	childSQL, childParams, err := namespaceRenderedQuery(trimRenderedQuerySQL(finalized.SQL), finalized.QueryParams, "anchored_child")
	if err != nil {
		return renderedFragment{}, err
	}
	paramsOut := map[string]string{
		"param_anchored_range_start_ms": strconv.FormatInt(params.StartMS, 10),
		"param_anchored_range_end_ms":   strconv.FormatInt(params.EndMS, 10),
		"param_anchored_range_step_ms":  strconv.FormatInt(params.StepMS, 10),
	}
	mergeRenderedQueryParams(paramsOut, childParams)
	gridSQL := "SELECT arrayJoin(arrayMap(ts_ms -> fromUnixTimestamp64Milli(ts_ms), range({anchored_range_start_ms:Int64}, {anchored_range_end_ms:Int64} + {anchored_range_step_ms:Int64}, {anchored_range_step_ms:Int64}))) AS timestamp"
	broadcastSQL := "SELECT tags, arraySort(item -> item.1, groupArray((timestamp, value))) AS time_series FROM (" +
		"SELECT anchored_child.tags AS tags, grid.timestamp AS timestamp, anchored_child.value AS value FROM (" + gridSQL + ") AS grid CROSS JOIN (" + childSQL + ") AS anchored_child" +
		") AS anchored_steps GROUP BY tags ORDER BY tags"
	return renderedFragment{RawSQL: broadcastSQL, ExtraParams: paramsOut}, nil
}

// renderAnchoredRangeSourceExprView is the pure-logical anchored-range
// entry for the UnarySourceExpr/LeafSource shapes rendered by
// lowerPointwiseFunction via renderSourceExprView. It resolves the anchor
// time from the logical plan tree (via logicalResolvedAnchorTimeMS),
// re-renders the inner SourceExprView in instant mode at that anchor, and
// wraps the result with wrapInstantAsAnchoredRange.
//
// OutputKind is captured on the analysis-side LoweringInfo; callers pass
// it in so OutputKindRangeMatrix short-circuits to returning the instant
// render unchanged. For UnarySourceExpr/LeafSource the output kind is
// always an instant-vector (matrix selectors flow through the range-
// function path instead), but threading it keeps the helper generic for
// future callers.
func renderAnchoredRangeSourceExprView(cfg storage.QueryConfig, view *native.SourceExprView, outputKind native.OutputKind, params RenderParams, anchorMS int64) (renderedFragment, error) {
	instantRendered, err := renderSourceExprView(cfg, view, RenderParams{
		Mode:                native.RenderModeInstant,
		EvaluationTimeMS:    anchorMS,
		RequiredStartMS:     params.RequiredStartMS,
		RequiredEndMS:       params.RequiredEndMS,
		ResolveSourcePromQL: params.ResolveSourcePromQL,
	})
	if err != nil {
		return renderedFragment{}, err
	}
	if outputKind == native.OutputKindRangeMatrix {
		return instantRendered, nil
	}
	return wrapInstantAsAnchoredRange(instantRendered, params)
}
