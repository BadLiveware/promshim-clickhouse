package renderer

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"

	logicalpkg "github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/native"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/storage"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/storage/schema"
	"github.com/prometheus/prometheus/promql/parser"
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
		ctx.cse = newRenderCSEState(node)
		root = true
	}
	ctx.cse.depth++
	rq, err := lowerInner(ctx, node)
	ctx.cse.depth--
	if err == nil && !root {
		rq, err = ctx.cse.subtreeReference(ctx, node, rq)
	}
	if err != nil || !root {
		return rq, err
	}
	rq, err = ctx.cse.apply(rq)
	if err != nil {
		return RenderedQuery{}, err
	}
	return withPhysicalSettings(rq, ctx.Params.Physical), nil
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
	depth         int
	ctes          map[string]renderCSEEntry
	order         []string
	subtreeCounts map[string]int
}

type renderCSEEntry struct {
	SQL    string
	Params map[string]string
}

func newRenderCSEState(root logicalpkg.Node) *renderCSEState {
	counts := map[string]int{}
	countCSESubtrees(root, counts)
	return &renderCSEState{ctes: map[string]renderCSEEntry{}, subtreeCounts: counts}
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

func subtreeReuseCTEName(key string) string {
	sum := sha1.Sum([]byte(key))
	return "cse_subtree_" + hex.EncodeToString(sum[:])[:12]
}

func cseSubtreeKey(node logicalpkg.Node) (string, bool) {
	if node == nil || !isCSESubtreeCandidate(node) {
		return "", false
	}
	described, ok := node.(interface {
		ExprString() string
		ValueType() parser.ValueType
	})
	if !ok || described.ExprString() == "" {
		return "", false
	}
	return "subtree|" + described.ExprString() + "|" + string(described.ValueType()), true
}

func isCSESubtreeCandidate(node logicalpkg.Node) bool {
	if nativeRepeatedSubexpressionReuseDisabled() {
		return false
	}
	switch node.(type) {
	case *logicalpkg.RangeFunctionPlan, *logicalpkg.RatePlan, *logicalpkg.IncreasePlan, *logicalpkg.DeltaPlan, *logicalpkg.ChangesPlan, *logicalpkg.DerivPlan, *logicalpkg.QuantileOverTimePlan:
		return true
	default:
		return false
	}
}

func countCSESubtrees(node logicalpkg.Node, counts map[string]int) {
	if key, ok := cseSubtreeKey(node); ok {
		counts[key]++
	}
	for _, child := range logicalChildren(node) {
		countCSESubtrees(child, counts)
	}
}

func logicalChildren(node logicalpkg.Node) []logicalpkg.Node {
	switch n := node.(type) {
	case *logicalpkg.UnaryPlan:
		return []logicalpkg.Node{n.Child}
	case *logicalpkg.BinaryPlan:
		return []logicalpkg.Node{n.LHS, n.RHS}
	case *logicalpkg.AggregationPlan:
		return []logicalpkg.Node{n.Child}
	case *logicalpkg.HistogramQuantilePlan:
		return []logicalpkg.Node{n.Child}
	case *logicalpkg.HistogramFractionPlan:
		return []logicalpkg.Node{n.Child}
	case *logicalpkg.HistogramProjectionPlan:
		return []logicalpkg.Node{n.Child}
	case *logicalpkg.HistogramQuantilesPlan:
		children := append([]logicalpkg.Node{}, n.ParamChildren...)
		return append(children, n.Child)
	case *logicalpkg.RangeFunctionPlan:
		return []logicalpkg.Node{n.Child}
	case *logicalpkg.VectorPlan:
		return []logicalpkg.Node{n.Child}
	case *logicalpkg.RoundPlan:
		return []logicalpkg.Node{n.Child}
	case *logicalpkg.SortPlan:
		return []logicalpkg.Node{n.Child}
	case *logicalpkg.ScalarConvertPlan:
		return []logicalpkg.Node{n.Child}
	case *logicalpkg.InfoPlan:
		return []logicalpkg.Node{n.Child}
	case *logicalpkg.PointwiseFunctionPlan:
		children := append([]logicalpkg.Node{}, n.ParamChildren...)
		return append(children, n.Child)
	case *logicalpkg.RatePlan:
		return []logicalpkg.Node{n.Child}
	case *logicalpkg.IncreasePlan:
		return []logicalpkg.Node{n.Child}
	case *logicalpkg.DeltaPlan:
		return []logicalpkg.Node{n.Child}
	case *logicalpkg.ChangesPlan:
		return []logicalpkg.Node{n.Child}
	case *logicalpkg.DerivPlan:
		return []logicalpkg.Node{n.Child}
	case *logicalpkg.QuantileOverTimePlan:
		return []logicalpkg.Node{n.Child}
	case *logicalpkg.AbsentPlan:
		return []logicalpkg.Node{n.Child}
	case *logicalpkg.AbsentOverTimePlan:
		return []logicalpkg.Node{n.Child}
	case *logicalpkg.SubqueryPlan:
		return []logicalpkg.Node{n.Child}
	case *logicalpkg.LabelReplacePlan:
		return []logicalpkg.Node{n.Child}
	case *logicalpkg.LabelJoinPlan:
		return []logicalpkg.Node{n.Child}
	default:
		return nil
	}
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

func (s *renderCSEState) subtreeReference(ctx LoweringCtx, node logicalpkg.Node, rq RenderedQuery) (RenderedQuery, error) {
	if s == nil || rq.SQL == "" {
		return rq, nil
	}
	key, ok := cseSubtreeKey(node)
	if !ok || s.subtreeCounts[key] <= 1 {
		return rq, nil
	}
	name := subtreeReuseCTEName(key)
	if _, ok := s.ctes[name]; !ok {
		sql, params, err := namespaceRenderedQuery(trimRenderedQuerySQL(rq.SQL), rq.QueryParams, name)
		if err != nil {
			return RenderedQuery{}, err
		}
		s.ctes[name] = renderCSEEntry{SQL: sql, Params: params}
		s.order = append(s.order, name)
	}
	columns := "tags AS tags, timestamp AS timestamp, value AS value"
	if ctx.Params.Mode == native.RenderModeRange {
		columns = "tags AS tags, time_series AS time_series"
	}
	return RenderedQuery{SQL: "SELECT " + columns + " FROM " + name + schema.QuerySuffix, QueryParams: map[string]string{}, QuerySettings: rq.QuerySettings}, nil
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
	sql := "WITH " + strings.Join(parts, ",\n") + "\n" + rq.SQL
	sql = strings.Replace(sql, "SETTINGS allow_experimental_time_series_table = 1", "SETTINGS allow_experimental_time_series_table = 1, enable_global_with_statement = 1, enable_materialized_cte = 1", 1)
	return RenderedQuery{SQL: sql, QueryParams: params, QuerySettings: rq.QuerySettings}, nil
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
