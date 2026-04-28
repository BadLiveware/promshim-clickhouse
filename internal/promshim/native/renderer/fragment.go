package renderer

import (
	"fmt"

	logicalpkg "github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/native/sqlb"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/storage/schema"
)

// renderedFragment is the typed form of a rendered native fragment. A fragment
// exposes either Select (preferred, sqlb AST body without SETTINGS / FORMAT) or
// RawSQL (already-stringified body without the SETTINGS / FORMAT suffix — used
// as a bridge for renderers that still call into string-returning storage
// helpers). ExtraParams carries parameter bindings that are not part of the
// Select AST (e.g. bindings namespaced by a legacy child bridge).
type renderedFragment struct {
	Select        *sqlb.Select
	RawSQL        string
	ExtraParams   map[string]string
	ExtraSettings map[string]any
}

func finalizeRenderedFragment(rf renderedFragment) (RenderedQuery, error) {
	var sql string
	var params map[string]string
	switch {
	case rf.Select != nil:
		s, p, err := rf.Select.Build()
		if err != nil {
			return RenderedQuery{}, err
		}
		sql, params = s, p
	case rf.RawSQL != "":
		sql = rf.RawSQL
		params = map[string]string{}
	default:
		return RenderedQuery{}, fmt.Errorf("renderedFragment has neither Select nor RawSQL")
	}
	if params == nil {
		params = map[string]string{}
	}
	for key, value := range rf.ExtraParams {
		params[key] = value
	}
	settings := map[string]any{}
	mergeRenderedQuerySettings(settings, rf.ExtraSettings)
	return RenderedQuery{SQL: sql + schema.QuerySuffix, QueryParams: params, QuerySettings: settings}, nil
}

// renderLoweredChildAsSource renders a logical.Node child via Lower, then
// namespaces the result's params under alias so the parent can embed it as
// an sqlb.RawSource. Intended for direct-render lowerers that previously
// went through renderChildAsSource — Lower returns errUnsupportedLowerNode
// when the child kind is not yet directly renderable, which bubbles up so
// the caller falls back hierarchically.
func renderLoweredChildAsSource(ctx LoweringCtx, child logicalpkg.Node, alias string) (sqlb.Source, map[string]string, error) {
	rq, err := Lower(ctx, child)
	if err != nil {
		return nil, nil, err
	}
	body, namespaced, err := namespaceRenderedQuery(trimRenderedQuerySQL(rq.SQL), rq.QueryParams, alias)
	if err != nil {
		return nil, nil, err
	}
	return rawRenderedSubquerySourceWithAlias(body, alias), namespaced, nil
}
