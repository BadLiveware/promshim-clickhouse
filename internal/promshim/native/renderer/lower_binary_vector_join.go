package renderer

import (
	"fmt"
	"os"
	"sort"
	"strings"

	logicalpkg "github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/native"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/native/physical"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/storage"
	promlabels "github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

const DisableNativeRepeatedSubexpressionReuseEnv = "PROM_SHIM_DISABLE_NATIVE_REPEATED_SUBEXPRESSION_REUSE"

// lowerBinaryVectorJoin renders a vector-vector BinaryPlan directly from the
// logical tree. LHS/RHS are lowered via Lower (bubbling errUnsupportedLowerNode
// if either side's kind isn't yet directly renderable), namespaced under "lhs"
// / "rhs", and handed to storage.Build{Instant,Range}BinaryVectorJoinSQL.
//
// Precondition: both sides have TimeDomain != DomainScalar; the scalar-
// involving path is handled in lowerBinary before this is ever called.
//
// Covered ops: arithmetic (+, -, *, /, %, ^), comparison (==, !=, >, <,
// >=, <= with or without bool), set ops (and, or, unless), with any of the
// supported matching shapes: one-to-one (no modifier), on(...), ignoring(...),
// group_left(...), group_right(...).
//
// Per-side range bounds come from logicalRequiredInputBounds, which mirrors
// native.RequiredInputBounds by walking the logical subtree for
// lookback/offset + @/start()/end() anchor resolution.
//
// Hierarchical fallback: if either child is missing logical analysis or the
// operator/matching shape isn't natively supported, we return
// errUnsupportedLowerNode so the caller falls back to the next execution tier.
func lowerBinaryVectorJoin(ctx LoweringCtx, n *logicalpkg.BinaryPlan) (RenderedQuery, error) {
	if n == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: lowerBinaryVectorJoin called with nil")
	}
	if n.LHS == nil || n.RHS == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: binary vector join missing child node")
	}
	if !native.IsSupportedNativeVectorJoinOp(n.Op, n.VectorMatching) {
		return RenderedQuery{}, errUnsupportedLowerNode
	}
	joinShape, ok := native.SupportedNativeVectorJoinShape(n.VectorMatching)
	if !ok {
		return RenderedQuery{}, errUnsupportedLowerNode
	}
	joinCfg := storage.BinaryJoinConfig{
		Op:             n.Op,
		ReturnBool:     n.ReturnBool,
		VectorMatching: native.CloneVectorMatching(n.VectorMatching),
		JoinShape:      joinShape,
	}
	metadataDecision, annotateMetadataDecision := metadataLookupJoinDecision(n)
	metadataPushdownDecision, annotateMetadataPushdownDecision := metadataLookupFilterPushdownDecision(n, metadataDecision)
	resourceStatusDecision, annotateResourceStatusDecision := resourceStatusJoinDecision(n)
	pushdownFilters, pushdownApplied := metadataLookupPushdownFilters(n, metadataDecision)
	if pushdownApplied {
		metadataPushdownDecision.Strategy = "applied"
		metadataPushdownDecision.Reason = "applied missing on(...) key exact matchers to metadata selector"
		metadataPushdownDecision.Guards = []string{"recognized_metadata_lookup_join", "rhs_key_filter_missing", "safe_exact_matcher_candidate", "exact_matcher_injection"}
		metadataPushdownDecision.Rejected = []physical.Alternative{{Strategy: "eligible_but_not_rewritten", Reason: "pushdown rewrite was skipped"}}
	}
	switch ctx.Params.Mode {
	case native.RenderModeInstant:
		reuseDecision, annotateReuse := buildSelfReuseDecision(n, joinShape, native.RenderModeInstant)
		if reuseDecision.Strategy == "instant_self_join" {
			childSQL, childParams, err := lowerBinaryVectorJoinSide(ctx, n.LHS, "lhs")
			if err != nil {
				return RenderedQuery{}, err
			}
			sql, queryParams, err := storage.BuildInstantBinaryVectorSelfJoinSQL(childSQL, childParams, joinCfg)
			if err != nil {
				return RenderedQuery{}, err
			}
			rq, err := finalizeRenderedFragment(renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: queryParams})
			if err != nil {
				return RenderedQuery{}, err
			}
			if annotateReuse {
				rq.PhysicalDecisions = appendRenderedQueryPhysicalDecisions(rq.PhysicalDecisions, reuseDecision)
			}
			if annotateMetadataDecision {
				rq.PhysicalDecisions = appendRenderedQueryPhysicalDecisions(rq.PhysicalDecisions, metadataDecision)
			}
			if annotateMetadataPushdownDecision {
				rq.PhysicalDecisions = appendRenderedQueryPhysicalDecisions(rq.PhysicalDecisions, metadataPushdownDecision)
			}
			if annotateResourceStatusDecision {
				rq.PhysicalDecisions = appendRenderedQueryPhysicalDecisions(rq.PhysicalDecisions, resourceStatusDecision)
			}
			return rq, nil
		}
		lhsSQL, lhsParams, err := lowerBinaryVectorJoinSide(ctx, n.LHS, "lhs")
		if err != nil {
			return RenderedQuery{}, err
		}
		rhsSQL, rhsParams, err := lowerBinaryVectorJoinSide(ctx, n.RHS, "rhs")
		if err != nil {
			return RenderedQuery{}, err
		}
		if pushdownApplied {
			rhsSQL = applyExactTagFiltersToRenderedSQL(rhsSQL, pushdownFilters, "metadata_rhs", native.RenderModeInstant)
		}
		sql, queryParams, err := storage.BuildInstantBinaryVectorJoinSQL(lhsSQL, lhsParams, rhsSQL, rhsParams, joinCfg)
		if err != nil {
			return RenderedQuery{}, err
		}
		rq, err := finalizeRenderedFragment(renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: queryParams})
		if err != nil {
			return RenderedQuery{}, err
		}
		if annotateReuse {
			rq.PhysicalDecisions = appendRenderedQueryPhysicalDecisions(rq.PhysicalDecisions, reuseDecision)
		}
		if annotateMetadataDecision {
			rq.PhysicalDecisions = appendRenderedQueryPhysicalDecisions(rq.PhysicalDecisions, metadataDecision)
		}
		if annotateMetadataPushdownDecision {
			rq.PhysicalDecisions = appendRenderedQueryPhysicalDecisions(rq.PhysicalDecisions, metadataPushdownDecision)
		}
		if annotateResourceStatusDecision {
			rq.PhysicalDecisions = appendRenderedQueryPhysicalDecisions(rq.PhysicalDecisions, resourceStatusDecision)
		}
		return rq, nil
	case native.RenderModeRange:
		reuseDecision, annotateReuse := buildSelfReuseDecision(n, joinShape, native.RenderModeRange)
		if reuseDecision.Strategy == "range_self_join" {
			childCtx := ctx
			childCtx.Params = rangeSideParams(ctx.Params, n.LHS)
			childSQL, childParams, err := lowerBinaryVectorJoinSide(childCtx, n.LHS, "lhs")
			if err != nil {
				return RenderedQuery{}, err
			}
			sql, queryParams, err := storage.BuildRangeBinaryVectorSelfJoinSQL(childSQL, childParams, joinCfg)
			if err != nil {
				return RenderedQuery{}, err
			}
			rq, err := finalizeRenderedFragment(renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: queryParams})
			if err != nil {
				return RenderedQuery{}, err
			}
			if annotateReuse {
				rq.PhysicalDecisions = appendRenderedQueryPhysicalDecisions(rq.PhysicalDecisions, reuseDecision)
			}
			if annotateMetadataDecision {
				rq.PhysicalDecisions = appendRenderedQueryPhysicalDecisions(rq.PhysicalDecisions, metadataDecision)
			}
			if annotateMetadataPushdownDecision {
				rq.PhysicalDecisions = appendRenderedQueryPhysicalDecisions(rq.PhysicalDecisions, metadataPushdownDecision)
			}
			if annotateResourceStatusDecision {
				rq.PhysicalDecisions = appendRenderedQueryPhysicalDecisions(rq.PhysicalDecisions, resourceStatusDecision)
			}
			return rq, nil
		}
		lhsBoundsCtx := ctx
		lhsBoundsCtx.Params = rangeSideParams(ctx.Params, n.LHS)
		rhsBoundsCtx := ctx
		rhsBoundsCtx.Params = rangeSideParams(ctx.Params, n.RHS)
		lhsSQL, lhsParams, err := lowerBinaryVectorJoinSide(lhsBoundsCtx, n.LHS, "lhs")
		if err != nil {
			return RenderedQuery{}, err
		}
		rhsSQL, rhsParams, err := lowerBinaryVectorJoinSide(rhsBoundsCtx, n.RHS, "rhs")
		if err != nil {
			return RenderedQuery{}, err
		}
		if pushdownApplied {
			rhsSQL = applyExactTagFiltersToRenderedSQL(rhsSQL, pushdownFilters, "metadata_rhs", native.RenderModeRange)
		}
		sql, queryParams, err := storage.BuildRangeBinaryVectorJoinSQL(lhsSQL, lhsParams, rhsSQL, rhsParams, joinCfg)
		if err != nil {
			return RenderedQuery{}, err
		}
		rq, err := finalizeRenderedFragment(renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: queryParams})
		if err != nil {
			return RenderedQuery{}, err
		}
		if annotateReuse {
			rq.PhysicalDecisions = appendRenderedQueryPhysicalDecisions(rq.PhysicalDecisions, reuseDecision)
		}
		if annotateMetadataDecision {
			rq.PhysicalDecisions = appendRenderedQueryPhysicalDecisions(rq.PhysicalDecisions, metadataDecision)
		}
		if annotateMetadataPushdownDecision {
			rq.PhysicalDecisions = appendRenderedQueryPhysicalDecisions(rq.PhysicalDecisions, metadataPushdownDecision)
		}
		if annotateResourceStatusDecision {
			rq.PhysicalDecisions = appendRenderedQueryPhysicalDecisions(rq.PhysicalDecisions, resourceStatusDecision)
		}
		return rq, nil
	default:
		return RenderedQuery{}, fmt.Errorf("unknown render mode %q", ctx.Params.Mode)
	}
}

func buildSelfReuseDecision(n *logicalpkg.BinaryPlan, joinShape string, mode native.RenderMode) (physical.Decision, bool) {
	lhsExpr := nodeExprString(n.LHS)
	rhsExpr := nodeExprString(n.RHS)
	if lhsExpr == "" || rhsExpr == "" {
		return physical.Decision{}, false
	}
	decision := physical.Decision{Kind: "row_source_reuse", Strategy: "not_reused"}
	selectedStrategy := "range_self_join"
	modeGuard := "range_mode"
	reuseReason := "identical one-to-one repeated range-function operands share one flattened range source"
	if mode == native.RenderModeInstant {
		selectedStrategy = "instant_self_join"
		modeGuard = "instant_mode"
		reuseReason = "identical one-to-one repeated range-function operands share one instant source"
	}
	lhsKey, lhsOK := cseSubtreeKey(n.LHS)
	rhsKey, rhsOK := cseSubtreeKey(n.RHS)
	if lhsExpr != rhsExpr {
		if lhsOK && rhsOK {
			decision.Reason = "operands are different repeated subtree candidates"
			decision.Guards = []string{"repeated_subtree_candidate_mismatch"}
			decision.Rejected = []physical.Alternative{{Strategy: selectedStrategy, Reason: decision.Reason}}
			return decision, true
		}
		return physical.Decision{}, false
	}
	switch {
	case nativeRepeatedSubexpressionReuseDisabled():
		decision.Reason = "native repeated subexpression reuse is disabled by environment"
		decision.Guards = []string{"disabled_by_env"}
	case joinShape != "one_to_one":
		decision.Reason = "range self-reuse requires one-to-one matching"
		decision.Guards = []string{"matching_not_one_to_one"}
	case n.VectorMatching != nil && (n.VectorMatching.On || len(n.VectorMatching.MatchingLabels) > 0 || len(n.VectorMatching.Include) > 0):
		decision.Reason = "range self-reuse currently requires default one-to-one matching labels"
		decision.Guards = []string{"matching_labels_not_default"}
	case !isSelfReuseSupportedOp(n.Op):
		decision.Reason = "operator is not supported for range self-reuse"
		decision.Guards = []string{"unsupported_operator"}
	case n.ReturnBool && !isSelfReuseComparisonOp(n.Op):
		decision.Reason = "bool modifier is only supported for comparison self-reuse"
		decision.Guards = []string{"bool_with_non_comparison"}
	default:
		if !lhsOK || !rhsOK || lhsKey != rhsKey {
			decision.Reason = "operands are not identical repeated subtree candidates"
			decision.Guards = []string{"repeated_subtree_candidate_mismatch"}
			break
		}
		decision.Strategy = selectedStrategy
		decision.Reason = reuseReason
		decision.Guards = []string{"identical_operands", "one_to_one_matching", "supported_operator", modeGuard, "repeated_subtree_candidate"}
	}
	decision.Rejected = []physical.Alternative{{Strategy: selectedStrategy, Reason: decision.Reason}}
	if decision.Strategy == selectedStrategy {
		decision.Rejected = nil
	}
	return decision, true
}

func nodeExprString(n logicalpkg.Node) string {
	if n == nil {
		return ""
	}
	exprNode, ok := n.(interface{ ExprString() string })
	if !ok {
		return ""
	}
	return exprNode.ExprString()
}

func isSelfReuseSupportedOp(op parser.ItemType) bool {
	switch op {
	case parser.ADD, parser.SUB, parser.MUL, parser.DIV, parser.MOD, parser.POW,
		parser.EQLC, parser.NEQ, parser.GTR, parser.LSS, parser.GTE, parser.LTE:
		return true
	default:
		return false
	}
}

func isSelfReuseComparisonOp(op parser.ItemType) bool {
	switch op {
	case parser.EQLC, parser.NEQ, parser.GTR, parser.LSS, parser.GTE, parser.LTE:
		return true
	default:
		return false
	}
}

func metadataLookupJoinDecision(n *logicalpkg.BinaryPlan) (physical.Decision, bool) {
	decision := physical.Decision{Kind: "metadata_lookup_join", Strategy: "not_recognized"}
	if n == nil {
		decision.Reason = "binary node is nil"
		return decision, true
	}
	if n.Op != parser.MUL || n.ReturnBool {
		decision.Reason = "requires non-bool multiplication"
		return decision, true
	}
	matching := native.CloneVectorMatching(n.VectorMatching)
	if matching == nil {
		matching = &parser.VectorMatching{Card: parser.CardOneToOne}
	}
	if matching.Card != parser.CardManyToOne || !matching.On {
		decision.Reason = "requires on(...) group_left(...) matching"
		return decision, true
	}
	topk, ok := n.RHS.(*logicalpkg.AggregationPlan)
	if !ok || topk.Op != parser.TOPK || topk.ParamNumber == nil || *topk.ParamNumber != 1 {
		decision.Reason = "rhs must be topk by (K) (1, ...)"
		return decision, true
	}
	maxAgg, ok := topk.Child.(*logicalpkg.AggregationPlan)
	if !ok || maxAgg.Op != parser.MAX {
		decision.Reason = "rhs topk child must be max by (K+M)"
		return decision, true
	}
	if !sameStringSet(topk.Grouping, matching.MatchingLabels) {
		decision.Reason = "topk grouping labels must match on(...) labels"
		return decision, true
	}
	combined := append([]string{}, matching.MatchingLabels...)
	combined = append(combined, matching.Include...)
	if !sameStringSet(maxAgg.Grouping, combined) {
		decision.Reason = "max grouping must be key labels plus included metadata labels"
		return decision, true
	}
	if _, ok := maxAgg.Child.(*logicalpkg.LeafExprPlan); !ok {
		decision.Reason = "rhs metadata source must be a plain selector"
		return decision, true
	}
	decision.Strategy = "recognized"
	decision.Reason = "recognized metadata-enrichment join shape"
	decision.Guards = []string{"mul_join", "on_group_left", "topk_1", "max_by_key_plus_metadata", "selector_rhs"}
	return decision, true
}

func metadataLookupFilterPushdownDecision(n *logicalpkg.BinaryPlan, metadataDecision physical.Decision) (physical.Decision, bool) {
	decision := physical.Decision{Kind: "metadata_lookup_filter_pushdown", Strategy: "not_applied"}
	if metadataDecision.Strategy != "recognized" {
		decision.Reason = "metadata lookup join shape not recognized"
		decision.Rejected = []physical.Alternative{{Strategy: "already_scoped", Reason: decision.Reason}, {Strategy: "eligible_but_not_rewritten", Reason: decision.Reason}}
		return decision, true
	}
	matching := native.CloneVectorMatching(n.VectorMatching)
	if matching == nil {
		decision.Reason = "vector matching is missing"
		return decision, true
	}
	lhsMatchers, ok := selectorExactMatchersFromNode(n.LHS)
	if !ok {
		decision.Reason = "left side has no analyzable selector exact matchers"
		return decision, true
	}
	rhsTopk, ok := n.RHS.(*logicalpkg.AggregationPlan)
	if !ok {
		decision.Reason = "right side is not topk aggregation"
		return decision, true
	}
	rhsMax, ok := rhsTopk.Child.(*logicalpkg.AggregationPlan)
	if !ok {
		decision.Reason = "right topk child is not max aggregation"
		return decision, true
	}
	rhsMatchers, ok := selectorExactMatchersFromNode(rhsMax.Child)
	if !ok {
		decision.Reason = "metadata selector has no analyzable exact matchers"
		return decision, true
	}
	pushable := 0
	for _, label := range matching.MatchingLabels {
		lv, lok := lhsMatchers[label]
		rv, rok := rhsMatchers[label]
		if !lok {
			decision.Reason = "left side is missing exact matcher for an on(...) key label"
			decision.Guards = []string{"recognized_metadata_lookup_join", "lhs_key_filter_missing"}
			decision.Rejected = []physical.Alternative{{Strategy: "eligible_but_not_rewritten", Reason: decision.Reason}}
			return decision, true
		}
		if !rok {
			pushable++
			continue
		}
		if lv != rv {
			decision.Reason = "key-label filters conflict across join sides"
			decision.Guards = []string{"recognized_metadata_lookup_join", "on_key_label_mismatch"}
			decision.Rejected = []physical.Alternative{{Strategy: "eligible_but_not_rewritten", Reason: decision.Reason}, {Strategy: "already_scoped", Reason: decision.Reason}}
			return decision, true
		}
	}
	if pushable > 0 {
		decision.Strategy = "eligible_but_not_rewritten"
		decision.Reason = "metadata selector is missing one or more on(...) key exact filters"
		decision.Guards = []string{"recognized_metadata_lookup_join", "rhs_key_filter_missing", "safe_exact_matcher_candidate"}
		decision.Rejected = []physical.Alternative{{Strategy: "already_scoped", Reason: "metadata selector is not yet fully scoped"}}
		return decision, true
	}
	decision.Strategy = "already_scoped"
	decision.Reason = "metadata selector already carries join-key exact filters"
	decision.Guards = []string{"recognized_metadata_lookup_join", "on_key_filters_shared"}
	return decision, true
}

func metadataLookupPushdownFilters(n *logicalpkg.BinaryPlan, metadataDecision physical.Decision) (map[string]string, bool) {
	if n == nil || metadataDecision.Strategy != "recognized" {
		return nil, false
	}
	matching := native.CloneVectorMatching(n.VectorMatching)
	if matching == nil {
		return nil, false
	}
	lhsMatchers, ok := selectorExactMatchersFromNode(n.LHS)
	if !ok {
		return nil, false
	}
	rhsTopk, ok := n.RHS.(*logicalpkg.AggregationPlan)
	if !ok {
		return nil, false
	}
	rhsMax, ok := rhsTopk.Child.(*logicalpkg.AggregationPlan)
	if !ok {
		return nil, false
	}
	rhsMatchers, ok := selectorExactMatchersFromNode(rhsMax.Child)
	if !ok {
		return nil, false
	}
	filters := map[string]string{}
	for _, label := range matching.MatchingLabels {
		lv, lok := lhsMatchers[label]
		rv, rok := rhsMatchers[label]
		if !lok {
			return nil, false
		}
		if rok && rv != lv {
			return nil, false
		}
		if !rok {
			filters[label] = lv
		}
	}
	if len(filters) == 0 {
		return nil, false
	}
	return filters, true
}

func applyExactTagFiltersToRenderedSQL(sql string, filters map[string]string, alias string, mode native.RenderMode) string {
	if len(filters) == 0 {
		return sql
	}
	keys := make([]string, 0, len(filters))
	for key := range filters {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	predicates := make([]string, 0, len(keys))
	for _, key := range keys {
		predicates = append(predicates, labelValueExpr(alias+".tags", key)+" = "+sqlStringLiteral(filters[key]))
	}
	columns := "tags, timestamp, value"
	if mode == native.RenderModeRange {
		columns = "tags, time_series"
	}
	return "SELECT " + columns + " FROM (" + trimRenderedQuerySQL(sql) + ") AS " + alias + " WHERE " + strings.Join(predicates, " AND ")
}

func selectorExactMatchersFromNode(node logicalpkg.Node) (map[string]string, bool) {
	leaf := firstLeafSelectorNode(node)
	if leaf == nil || leaf.Expr == nil {
		return nil, false
	}
	selector := firstVectorSelectorExpr(leaf.Expr)
	if selector == nil {
		return nil, false
	}
	matchers := map[string]string{}
	for _, m := range selector.LabelMatchers {
		if m == nil || m.Type != promlabels.MatchEqual {
			continue
		}
		matchers[m.Name] = m.Value
	}
	if len(matchers) == 0 {
		return nil, false
	}
	return matchers, true
}

func firstLeafSelectorNode(node logicalpkg.Node) *logicalpkg.LeafExprPlan {
	switch n := node.(type) {
	case *logicalpkg.LeafExprPlan:
		return n
	case *logicalpkg.UnaryPlan:
		return firstLeafSelectorNode(n.Child)
	case *logicalpkg.AggregationPlan:
		return firstLeafSelectorNode(n.Child)
	case *logicalpkg.RangeFunctionPlan:
		return firstLeafSelectorNode(n.Child)
	case *logicalpkg.RatePlan:
		return firstLeafSelectorNode(n.Child)
	case *logicalpkg.IncreasePlan:
		return firstLeafSelectorNode(n.Child)
	case *logicalpkg.DeltaPlan:
		return firstLeafSelectorNode(n.Child)
	case *logicalpkg.ChangesPlan:
		return firstLeafSelectorNode(n.Child)
	case *logicalpkg.DerivPlan:
		return firstLeafSelectorNode(n.Child)
	case *logicalpkg.QuantileOverTimePlan:
		return firstLeafSelectorNode(n.Child)
	case *logicalpkg.LabelReplacePlan:
		return firstLeafSelectorNode(n.Child)
	case *logicalpkg.LabelJoinPlan:
		return firstLeafSelectorNode(n.Child)
	default:
		return nil
	}
}

func firstVectorSelectorExpr(expr parser.Expr) *parser.VectorSelector {
	switch e := expr.(type) {
	case *parser.VectorSelector:
		return e
	case *parser.MatrixSelector:
		if vs, ok := e.VectorSelector.(*parser.VectorSelector); ok {
			return vs
		}
		return nil
	case *parser.StepInvariantExpr:
		return firstVectorSelectorExpr(e.Expr)
	case *parser.ParenExpr:
		return firstVectorSelectorExpr(e.Expr)
	default:
		return nil
	}
}

func sameStringSet(lhs, rhs []string) bool {
	if len(lhs) != len(rhs) {
		return false
	}
	l := append([]string{}, lhs...)
	r := append([]string{}, rhs...)
	sort.Strings(l)
	sort.Strings(r)
	for i := range l {
		if l[i] != r[i] {
			return false
		}
	}
	return true
}

func nativeRepeatedSubexpressionReuseDisabled() bool {
	switch os.Getenv(DisableNativeRepeatedSubexpressionReuseEnv) {
	case "1", "true", "TRUE", "yes", "YES", "on", "ON":
		return true
	default:
		return false
	}
}

func vectorZeroDefaultingDecision(n *logicalpkg.BinaryPlan) (physical.Decision, bool) {
	decision := physical.Decision{Kind: "vector_zero_defaulting", Strategy: "not_recognized"}
	if n == nil || n.Op != parser.LOR {
		return decision, false
	}
	// Check if either side is vector(0)
	lhsVec, lhsIsVec := n.LHS.(*logicalpkg.VectorPlan)
	rhsVec, rhsIsVec := n.RHS.(*logicalpkg.VectorPlan)
	if !lhsIsVec && !rhsIsVec {
		return decision, false
	}
	vecSide := lhsVec
	if rhsIsVec {
		vecSide = rhsVec
	}
	if vecSide == nil || vecSide.Expr == nil {
		return decision, false
	}
	// verify it's vector(0)
	if vecSide.Expr.String() != "vector(0)" {
		decision.Reason = "or branch contains vector(N) but not vector(0)"
		return decision, true
	}
	decision.Strategy = "recognized"
	decision.Reason = "recognized or vector(0) defaulting pattern"
	decision.Guards = []string{"lor_operator", "vector_zero_default"}
	return decision, true
}

func resourceStatusJoinDecision(n *logicalpkg.BinaryPlan) (physical.Decision, bool) {
	decision := physical.Decision{Kind: "resource_status_join", Strategy: "not_recognized"}
	if n == nil || n.Op != parser.MUL || n.ReturnBool {
		return decision, false
	}
	matching := native.CloneVectorMatching(n.VectorMatching)
	if matching == nil || matching.Card != parser.CardManyToOne || !matching.On || len(matching.Include) > 0 {
		return decision, false
	}
	// RHS should be: max by (K) (<status_selector> == 1)
	rhsAgg, ok := n.RHS.(*logicalpkg.AggregationPlan)
	if !ok || rhsAgg.Op != parser.MAX {
		return decision, false
	}
	// RHS child: binary comparison == 1 with a leaf selector on the other side
	rhsBin, ok := rhsAgg.Child.(*logicalpkg.BinaryPlan)
	if !ok || rhsBin.Op != parser.EQLC || rhsBin.ReturnBool {
		return decision, false
	}
	// Check for leaf selector == 1 on RHS binary
	rhsLeaf, lhsOK := rhsBin.LHS.(*logicalpkg.LeafExprPlan)
	scalarLit, rhsOK := rhsBin.RHS.(*logicalpkg.ScalarLiteralPlan)
	if !lhsOK || !rhsOK {
		rhsLeaf, lhsOK = rhsBin.RHS.(*logicalpkg.LeafExprPlan)
		scalarLit, rhsOK = rhsBin.LHS.(*logicalpkg.ScalarLiteralPlan)
	}
	if !lhsOK || !rhsOK || rhsLeaf == nil {
		decision.Reason = "resource-status join rhs is not <selector> == <scalar> shape"
		return decision, true
	}
	if scalarLit == nil || scalarLit.Value != 1 {
		decision.Reason = "resource-status join rhs scalar must be == 1"
		return decision, true
	}
	// Verify on(...) labels match max grouping
	if !sameStringSet(rhsAgg.Grouping, matching.MatchingLabels) {
		decision.Reason = "resource-status join on labels do not match max grouping"
		return decision, true
	}
	decision.Strategy = "recognized"
	decision.Reason = "recognized resource-status join shape"
	decision.Guards = []string{"mul_join", "on_group_left", "max_status_selector", "scalar_comparison"}
	return decision, true
}

// lowerBinaryVectorJoinSide lowers one side of a vector-vector binary
// join and namespaces the result under the given alias ("lhs" or "rhs")
// so the rendered SQL is embeddable as a FROM source inside the join
// body.
func lowerBinaryVectorJoinSide(ctx LoweringCtx, child logicalpkg.Node, prefix string) (string, map[string]string, error) {
	rendered, err := Lower(ctx, child)
	if err != nil {
		return "", nil, err
	}
	return namespaceRenderedQuery(trimRenderedQuerySQL(rendered.SQL), rendered.QueryParams, prefix)
}

// rangeSideParams computes the per-side RenderParams for range-mode binary
// vector join lowering, deriving RequiredStartMS/EndMS via
// logicalRequiredInputBounds — the logical-tree mirror of
// native.RequiredInputBounds.
func rangeSideParams(outer RenderParams, child logicalpkg.Node) RenderParams {
	requiredStartMS, requiredEndMS, _ := logicalRequiredInputBounds(child, native.OptimizationContext{Mode: native.RenderModeRange, StartMS: outer.StartMS, EndMS: outer.EndMS, StepMS: outer.StepMS})
	return RenderParams{
		Mode:                native.RenderModeRange,
		StartMS:             outer.StartMS,
		EndMS:               outer.EndMS,
		StepMS:              outer.StepMS,
		RequiredStartMS:     requiredStartMS,
		RequiredEndMS:       requiredEndMS,
		ResolveSourcePromQL: outer.ResolveSourcePromQL,
	}
}
