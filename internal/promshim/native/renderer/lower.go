package renderer

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	logicalpkg "github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/native"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/storage"
)

// errUnsupportedLowerNode is returned by Lower when the node kind is
// not handled by the lowering dispatcher. Callers inspect this with
// IsUnsupportedByLower and fall back hierarchically to the next
// execution tier.
var errUnsupportedLowerNode = errors.New("renderer: node kind not supported by Lower")

// LoweringCtx bundles the per-query inputs Lower needs. It is
// immutable for the duration of a Lower call; per-kind lowerers treat
// it as read-only.
type LoweringCtx struct {
	Config         storage.QueryConfig
	Analysis       *logicalpkg.Analysis
	NativeAnalysis *native.Analysis
	Params         RenderParams

	cse *renderCSEState
}

// Lower translates a logical.Node into a RenderedQuery via a
// type-switch dispatch. Unsupported kinds return
// errUnsupportedLowerNode so callers can fall back hierarchically to
// the next execution tier.
func Lower(ctx LoweringCtx, node logicalpkg.Node) (RenderedQuery, error) {
	root := false
	if ctx.cse == nil {
		ctx.cse = newRenderCSEState()
		root = true
	}
	ctx.cse.depth++
	rq, err := lowerInner(ctx, node)
	ctx.cse.depth--
	if err != nil || !root {
		return rq, err
	}
	return ctx.cse.apply(rq)
}

func lowerInner(ctx LoweringCtx, node logicalpkg.Node) (RenderedQuery, error) {
	if node == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: Lower called with nil node")
	}
	if ctx.Analysis == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: Lower requires an Analysis")
	}
	switch n := node.(type) {
	case *logicalpkg.LeafExprPlan:
		return lowerLeaf(ctx, n)
	case *logicalpkg.ScalarLiteralPlan:
		return lowerScalarLiteral(ctx, n)
	case *logicalpkg.BinaryPlan:
		return lowerBinary(ctx, n)
	case *logicalpkg.PointwiseFunctionPlan:
		return lowerPointwiseFunction(ctx, n)
	case *logicalpkg.ScalarBuiltinPlan:
		return lowerScalarBuiltin(ctx, n)
	case *logicalpkg.ScalarConvertPlan:
		return lowerScalarConvert(ctx, n)
	case *logicalpkg.SubqueryPlan:
		return lowerSubquery(ctx, n)
	case *logicalpkg.SortPlan:
		return lowerSortTransform(ctx, n)
	case *logicalpkg.LabelReplacePlan:
		return lowerLabelTransform(ctx, n)
	case *logicalpkg.LabelJoinPlan:
		return lowerLabelTransform(ctx, n)
	case *logicalpkg.AggregationPlan:
		return lowerAggregation(ctx, n)
	case *logicalpkg.RangeFunctionPlan:
		return lowerRangeFunction(ctx, n)
	case *logicalpkg.RatePlan:
		return lowerRangeFunction(ctx, n)
	case *logicalpkg.IncreasePlan:
		return lowerRangeFunction(ctx, n)
	case *logicalpkg.DeltaPlan:
		return lowerRangeFunction(ctx, n)
	case *logicalpkg.ChangesPlan:
		return lowerRangeFunction(ctx, n)
	case *logicalpkg.DerivPlan:
		return lowerRangeFunction(ctx, n)
	case *logicalpkg.QuantileOverTimePlan:
		return lowerRangeFunction(ctx, n)
	case *logicalpkg.HistogramProjectionPlan:
		return lowerHistogramProjection(ctx, n)
	case *logicalpkg.HistogramQuantilePlan:
		return lowerHistogramFunction(ctx, n)
	case *logicalpkg.HistogramFractionPlan:
		return lowerHistogramFunction(ctx, n)
	case *logicalpkg.HistogramQuantilesPlan:
		return lowerHistogramFunction(ctx, n)
	case *logicalpkg.AbsentPlan:
		return lowerAbsent(ctx, n)
	case *logicalpkg.AbsentOverTimePlan:
		return lowerAbsent(ctx, n)
	case *logicalpkg.InfoPlan:
		return lowerInfoJoin(ctx, n)
	case *logicalpkg.UnaryPlan:
		return lowerUnary(ctx, n)
	case *logicalpkg.RoundPlan:
		return lowerRound(ctx, n)
	case *logicalpkg.VectorPlan:
		return lowerVector(ctx, n)
	default:
		return RenderedQuery{}, errUnsupportedLowerNode
	}
}

type renderCSEState struct {
	depth int
	ctes  map[string]renderCSEEntry
	order []string
}

type renderCSEEntry struct {
	SQL    string
	Params map[string]string
}

func newRenderCSEState() *renderCSEState {
	return &renderCSEState{ctes: map[string]renderCSEEntry{}}
}

var cseNameSanitizer = regexp.MustCompile(`[^A-Za-z0-9_]`)

func selectorReuseCTEName(group string) string {
	name := cseNameSanitizer.ReplaceAllString(group, "_")
	name = strings.Trim(name, "_")
	if name == "" {
		name = "selector"
	}
	return "cse_" + name
}

func (s *renderCSEState) leafReference(ctx LoweringCtx, leaf *logicalpkg.LeafExprPlan, rf renderedFragment) (renderedFragment, bool, error) {
	if s == nil || ctx.Analysis == nil || leaf == nil || rf.RawSQL == "" {
		return rf, false, nil
	}
	info := ctx.Analysis.InfoFor(leaf)
	if info == nil || info.SelectorReuseGroup == "" || info.SelectorReuseBlockedReason != "" {
		return rf, false, nil
	}
	name := selectorReuseCTEName(info.SelectorReuseGroup)
	if _, ok := s.ctes[name]; !ok {
		sql, params, err := namespaceRenderedQuery(rf.RawSQL, rf.ExtraParams, name)
		if err != nil {
			return renderedFragment{}, false, err
		}
		s.ctes[name] = renderCSEEntry{SQL: sql, Params: params}
		s.order = append(s.order, name)
	}
	columns := "tags AS tags, timestamp AS timestamp, value AS value"
	if ctx.Params.Mode == native.RenderModeRange {
		columns = "tags AS tags, time_series AS time_series"
	}
	return renderedFragment{RawSQL: "SELECT " + columns + " FROM " + name}, true, nil
}

func (s *renderCSEState) apply(rq RenderedQuery) (RenderedQuery, error) {
	if s == nil || len(s.order) == 0 {
		return rq, nil
	}
	params := map[string]string{}
	for key, value := range rq.QueryParams {
		params[key] = value
	}
	parts := make([]string, 0, len(s.order))
	for _, name := range s.order {
		entry := s.ctes[name]
		parts = append(parts, name+" AS MATERIALIZED (\n"+entry.SQL+"\n)")
		for key, value := range entry.Params {
			params[key] = value
		}
	}
	sort.Strings(parts)
	sql := "WITH " + strings.Join(parts, ",\n") + "\n" + rq.SQL
	sql = strings.Replace(sql, "SETTINGS allow_experimental_time_series_table = 1", "SETTINGS allow_experimental_time_series_table = 1, enable_global_with_statement = 1, enable_materialized_cte = 1", 1)
	return RenderedQuery{SQL: sql, QueryParams: params}, nil
}

// IsUnsupportedByLower reports whether err is the Lower fallback
// sentinel — i.e. the caller should fall back to the next execution
// tier.
func IsUnsupportedByLower(err error) bool { return errors.Is(err, errUnsupportedLowerNode) }

// lowerLeaf handles a top-level LeafExprPlan by rendering SQL directly
// from the logical plan via renderLeafLogical, which uses
// buildSelectorSource and the storage.Build*QuerySQL helpers.
func lowerLeaf(ctx LoweringCtx, n *logicalpkg.LeafExprPlan) (RenderedQuery, error) {
	if n == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: lowerLeaf called with nil")
	}
	// Prefer the cached leaf selector from NativeAnalysis when
	// available: the cached SelectorSource is immutable across the
	// render pass, so reusing the pointer avoids re-running
	// BuildSelectorSource. Tag-narrowing is carried on RenderParams and
	// merged onto the storage selector inside renderLeafLogical via
	// applyRenderParamsNarrowing.
	var cachedSelector *native.SelectorSource
	if ctx.NativeAnalysis != nil {
		if info := ctx.NativeAnalysis.InfoFor(n); info != nil {
			cachedSelector = info.LeafSelector
		}
	}
	rf, err := renderLeafLogical(ctx.Config, n, ctx.Params, cachedSelector)
	if err != nil {
		return RenderedQuery{}, err
	}
	if cseRF, ok, err := ctx.cse.leafReference(ctx, n, rf); err != nil {
		return RenderedQuery{}, err
	} else if ok {
		rf = cseRF
	}
	return finalizeRenderedFragment(rf)
}

// lowerBinary handles BinaryPlan in two branches:
//   - Scalar-involving (at least one side is DomainScalar): delegates
//     to lowerBinaryScalarInvolving.
//   - Vector-vector (both sides are non-scalar): delegates to
//     lowerBinaryVectorJoin, which direct-renders each side via Lower
//     and assembles the join via
//     storage.Build{Instant,Range}BinaryVectorJoinSQL.
func lowerBinary(ctx LoweringCtx, n *logicalpkg.BinaryPlan) (RenderedQuery, error) {
	if n == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: lowerBinary called with nil")
	}
	if ctx.Analysis == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: binary missing logical analysis")
	}
	lhsInfo := ctx.Analysis.InfoFor(n.LHS)
	rhsInfo := ctx.Analysis.InfoFor(n.RHS)
	if lhsInfo == nil || rhsInfo == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: binary node missing analysis")
	}
	if lhsInfo.TimeDomain == logicalpkg.DomainScalar || rhsInfo.TimeDomain == logicalpkg.DomainScalar {
		return lowerBinaryScalarInvolving(ctx, n)
	}
	// Vector-vector path: Surface 13 (Approach A).
	return lowerBinaryVectorJoin(ctx, n)
}
