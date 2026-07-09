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
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/native/physical"
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
		ctx.cse = newRenderCSEState(ctx.Analysis, node)
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
	effectiveParams := suppressThreadCapForPlan(ctx.Params, node)
	return withPhysicalSettings(rq, effectiveParams.Physical), nil
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
	// plainOnly holds CTE names that must render as plain (non-MATERIALIZED)
	// CTEs: ClickHouse rejects references to a MATERIALIZED CTE when the
	// reference sits behind a value-filtering layer inside a set-operator
	// join (issue #39). See materializedCTEBlockedNames.
	plainOnly map[string]struct{}
}

type renderCSEEntry struct {
	SQL    string
	Params map[string]string
}

func newRenderCSEState(analysis *logicalpkg.Analysis, root logicalpkg.Node) *renderCSEState {
	counts := map[string]int{}
	countCSESubtrees(root, counts)
	return &renderCSEState{ctes: map[string]renderCSEEntry{}, subtreeCounts: counts, plainOnly: materializedCTEBlockedNames(analysis, root)}
}

// materializedCTEBlockedNames walks the logical plan and returns the CTE
// names (selector-reuse and repeated-subtree CTEs) that must NOT be promoted
// to MATERIALIZED CTEs.
//
// Trigger boundary (issue #39, verified against ClickHouse): a CTE candidate
// referenced from BOTH sides of a set operator (and / or / unless, with or
// without on()/ignoring() matching) where at least one of those references
// sits behind a value-filtering layer (a non-bool comparison such as
// `up == 0`). ClickHouse rejects the filtered reference to a MATERIALIZED
// CTE at execution; the identical SQL with a plain CTE is valid and returns
// the correct result. Non-trigger shapes — `up unless up` (no filter),
// different selectors (no shared CTE), or bool comparisons (value transform,
// no row filter) — keep the MATERIALIZED promotion.
//
// The check is plan-shape only (version-agnostic) and errs toward plain
// CTEs: demotion only costs the materialization hint, never correctness.
func materializedCTEBlockedNames(analysis *logicalpkg.Analysis, root logicalpkg.Node) map[string]struct{} {
	blocked := map[string]struct{}{}
	var walk func(node logicalpkg.Node)
	walk = func(node logicalpkg.Node) {
		if node == nil {
			return
		}
		if bin, ok := node.(*logicalpkg.BinaryPlan); ok && bin.Op.IsSetOperator() {
			lhsRefs := collectCTEReferences(analysis, bin.LHS)
			rhsRefs := collectCTEReferences(analysis, bin.RHS)
			for name, lhsFiltered := range lhsRefs {
				rhsFiltered, shared := rhsRefs[name]
				if shared && (lhsFiltered || rhsFiltered) {
					blocked[name] = struct{}{}
				}
			}
		}
		for _, child := range logicalChildren(node) {
			walk(child)
		}
	}
	walk(root)
	return blocked
}

// collectCTEReferences returns the CTE candidate names referenced under
// node, mapped to whether ANY of those references sits behind a
// value-filtering layer within this subtree.
func collectCTEReferences(analysis *logicalpkg.Analysis, node logicalpkg.Node) map[string]bool {
	refs := map[string]bool{}
	var walk func(node logicalpkg.Node, filtered bool)
	walk = func(node logicalpkg.Node, filtered bool) {
		if node == nil {
			return
		}
		if key, ok := cseSubtreeKey(node); ok {
			name := subtreeReuseCTEName(key)
			refs[name] = refs[name] || filtered
		}
		if leaf, ok := node.(*logicalpkg.LeafExprPlan); ok && analysis != nil {
			if info := analysis.InfoFor(leaf); info != nil && info.SelectorReuseGroup != "" && info.SelectorReuseBlockedReason == "" {
				name := selectorReuseCTEName(info.SelectorReuseGroup)
				refs[name] = refs[name] || filtered
			}
		}
		childFiltered := filtered || isValueFilterLayer(node)
		for _, child := range logicalChildren(node) {
			walk(child, childFiltered)
		}
	}
	walk(node, false)
	return refs
}

// isValueFilterLayer reports whether lowering node wraps its children in a
// row-dropping value filter: a comparison operator without the bool
// modifier (vector-scalar renders a WHERE over the child source; the bool
// form only rewrites values and keeps every row, so it is not a filter).
func isValueFilterLayer(node logicalpkg.Node) bool {
	bin, ok := node.(*logicalpkg.BinaryPlan)
	if !ok || bin == nil {
		return false
	}
	return bin.Op.IsComparisonOperator() && !bin.ReturnBool
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
	return RenderedQuery{SQL: "SELECT " + columns + " FROM " + name + schema.QuerySuffix, QueryParams: map[string]string{}, QuerySettings: rq.QuerySettings, PhysicalDecisions: rq.PhysicalDecisions}, nil
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
	decisions := rq.PhysicalDecisions
	anyMaterialized := false
	for _, name := range s.order {
		entry := s.ctes[name]
		keyword := " AS MATERIALIZED (\n"
		if _, plain := s.plainOnly[name]; plain {
			keyword = " AS (\n"
			decisions = append(decisions, physical.Decision{
				Kind:     "cse_cte_materialization",
				Strategy: "plain_cte",
				Reason:   "shared subexpression " + name + " is referenced behind a value filter inside a set-operator join; ClickHouse rejects filtered references to MATERIALIZED CTEs",
				Guards:   []string{"set_operator_shared_reference", "value_filtered_reference"},
				Rejected: []physical.Alternative{{Strategy: "materialized_cte", Reason: "filtered MATERIALIZED CTE reference fails at ClickHouse execution"}},
			})
		} else {
			anyMaterialized = true
		}
		parts = append(parts, name+keyword+entry.SQL+"\n)")
		for key, value := range entry.Params {
			params[key] = value
		}
	}
	sql := "WITH " + strings.Join(parts, ",\n") + "\n" + rq.SQL
	replacement := "SETTINGS allow_experimental_time_series_table = 1, enable_global_with_statement = 1"
	if anyMaterialized {
		replacement += ", enable_materialized_cte = 1"
	}
	sql = strings.Replace(sql, "SETTINGS allow_experimental_time_series_table = 1", replacement, 1)
	return RenderedQuery{SQL: sql, QueryParams: params, QuerySettings: rq.QuerySettings, PhysicalDecisions: decisions}, nil
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
