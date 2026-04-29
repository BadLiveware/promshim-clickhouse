package renderer

import (
	"fmt"
	"os"

	logicalpkg "github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/native"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/native/physical"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/storage"
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

func nativeRepeatedSubexpressionReuseDisabled() bool {
	switch os.Getenv(DisableNativeRepeatedSubexpressionReuseEnv) {
	case "1", "true", "TRUE", "yes", "YES", "on", "ON":
		return true
	default:
		return false
	}
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
