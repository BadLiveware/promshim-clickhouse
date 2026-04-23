package renderer

import (
	"fmt"

	logicalpkg "github.com/BadLiveware/promshim-ch/internal/promshim/logical"
	"github.com/BadLiveware/promshim-ch/internal/promshim/native"
	"github.com/BadLiveware/promshim-ch/internal/promshim/storage"
)

// renderLeafLogical renders a top-level LeafExprPlan directly to a
// renderedFragment without constructing an intermediate NativeFragment.
// It replicates the non-folded LeafSource branch of renderSourceFragment,
// reusing buildSelectorSource and the storage.Build*QuerySQL helpers to
// lock byte-identity with the Fragment path.
//
// Pure (non-folded) leaves always have ValueExpr="{value}" and
// TagsExpr="{tags}", which means sourceWrapperIsIdentity returns true
// and the wrap step is skipped. This function produces the same SQL as
// the "identity wrapper" branch of renderSourceFragment.
func renderLeafLogical(cfg storage.QueryConfig, leaf *logicalpkg.LeafExprPlan, params RenderParams) (renderedFragment, error) {
	if leaf == nil {
		return renderedFragment{}, fmt.Errorf("renderer: renderLeafLogical called with nil leaf")
	}

	selector, err := native.BuildSelectorSource(leaf.Expr)
	if err != nil {
		return renderedFragment{}, fmt.Errorf("renderer: leaf selector analysis failed: %w", err)
	}

	switch params.Mode {
	case native.RenderModeInstant:
		if selector != nil {
			storageSel := nativeSelectorToStorage(selector)
			sql, queryParams, err := storage.BuildInstantSelectorQuerySQL(cfg, storageSel, params.RequiredStartMS, params.RequiredEndMS)
			if err != nil {
				return renderedFragment{}, err
			}
			return renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: queryParams}, nil
		}
		// Delegated PromQL leaf — mirrors renderSourceFragment lines 33-45.
		// renderSourceFragment always calls wrapInstantSourceQuery for the
		// delegated path (no identity check), so we match that here.
		if params.ResolveSourcePromQL == nil {
			return renderedFragment{}, fmt.Errorf("native fragment render requires a source PromQL resolver")
		}
		promQL, err := params.ResolveSourcePromQL(leaf.Expr)
		if err != nil {
			return renderedFragment{}, err
		}
		sql, queryParams := storage.BuildInstantQuerySQL(cfg, promQL, params.EvaluationTimeMS)
		wrappedSQL, err := wrapInstantSourceQuery(sql, "{value}", "{tags}")
		if err != nil {
			return renderedFragment{}, err
		}
		return renderedFragment{RawSQL: trimRenderedQuerySQL(wrappedSQL), ExtraParams: queryParams}, nil

	case native.RenderModeRange:
		if selector != nil {
			storageSel := nativeSelectorToStorage(selector)
			sql, queryParams, err := storage.BuildRangeSelectorQuerySQL(cfg, storageSel, params.RequiredStartMS, params.RequiredEndMS, params.StartMS, params.EndMS, params.StepMS)
			if err != nil {
				return renderedFragment{}, err
			}
			return renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: queryParams}, nil
		}
		// Delegated PromQL leaf — mirrors renderSourceFragment lines 61-73.
		if params.ResolveSourcePromQL == nil {
			return renderedFragment{}, fmt.Errorf("native fragment render requires a source PromQL resolver")
		}
		promQL, err := params.ResolveSourcePromQL(leaf.Expr)
		if err != nil {
			return renderedFragment{}, err
		}
		sql, queryParams := storage.BuildRangeQuerySQL(cfg, promQL, params.StartMS, params.EndMS, params.StepMS)
		wrappedSQL, err := wrapRangeSourceQuery(sql, "{value}", "{tags}")
		if err != nil {
			return renderedFragment{}, err
		}
		return renderedFragment{RawSQL: trimRenderedQuerySQL(wrappedSQL), ExtraParams: queryParams}, nil

	default:
		return renderedFragment{}, fmt.Errorf("unknown render mode %q", params.Mode)
	}
}

// nativeSelectorToStorage converts a native.SelectorSource (from the
// native analysis package) into a storage.SelectorSource (used by the
// storage SQL builders). This mirrors what renderAggregationSource does
// in source.go, but operates directly on the logical leaf's selector.
func nativeSelectorToStorage(sel *native.SelectorSource) storage.SelectorSource {
	return storage.SelectorSource{
		Kind:              storage.SelectorKind(sel.Kind),
		MetricName:        sel.MetricName,
		Matchers:          selectorEffectiveMatchers(sel),
		NeedTags:          selectorNeedsTags(sel),
		RequireFullTags:   sel.RequireFullTags,
		RequiredTagLabels: append([]string(nil), sel.RequiredTagLabels...),
		LookbackMS:        sel.Lookback.Milliseconds(),
		OffsetMS:          sel.Offset.Milliseconds(),
	}
}
