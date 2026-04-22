package renderer

import (
	"fmt"
	"strconv"

	"github.com/BadLiveware/promshim-ch/internal/promshim/native"
	"github.com/BadLiveware/promshim-ch/internal/promshim/storage"
)

func renderAnchoredRangeFragment(cfg storage.QueryConfig, fragment *native.NativeFragment, params RenderParams) (renderedFragment, error) {
	anchorMS, ok := native.ResolvedAnchorTimeMS(fragment, native.OptimizationContext{
		Mode:             native.RenderModeRange,
		EvaluationTimeMS: params.EvaluationTimeMS,
		StartMS:          params.StartMS,
		EndMS:            params.EndMS,
		StepMS:           params.StepMS,
	})
	if !ok {
		return renderedFragment{}, fmt.Errorf("anchored native range rendering requires a resolved anchor")
	}
	instantRendered, err := renderFragment(cfg, fragment, RenderParams{
		Mode:                native.RenderModeInstant,
		EvaluationTimeMS:    anchorMS,
		RequiredStartMS:     params.RequiredStartMS,
		RequiredEndMS:       params.RequiredEndMS,
		ResolveSourcePromQL: params.ResolveSourcePromQL,
	})
	if err != nil {
		return renderedFragment{}, err
	}
	if fragment.OutputKind == native.OutputKindRangeMatrix {
		return instantRendered, nil
	}
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
