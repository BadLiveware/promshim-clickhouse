package local

import (
	"github.com/BadLiveware/promshim-ch/internal/promshim/logical"
	promlabels "github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

// unwrapTransparentExpr is a package-local forwarding wrapper around
// logical.UnwrapTransparentExpr so that callers within local/ (e.g.
// delegated_support.go) continue to compile without modification.
func unwrapTransparentExpr(expr parser.Expr) parser.Expr {
	return logical.UnwrapTransparentExpr(expr)
}

// expressionContainsSubquery forwards to logical.ExpressionContainsSubquery.
// Kept here for any local/ callers; prefer the exported version in new code.
func expressionContainsSubquery(expr parser.Expr) bool {
	return logical.ExpressionContainsSubquery(expr)
}

// clonePromMatchers is retained for use by planner.go which builds local plan
// nodes that embed copied matcher slices. The canonical implementation lives
// in logical/build_helpers.go.
func clonePromMatchers(matchers []*promlabels.Matcher) []*promlabels.Matcher {
	if len(matchers) == 0 {
		return nil
	}
	out := make([]*promlabels.Matcher, 0, len(matchers))
	for _, matcher := range matchers {
		if matcher == nil {
			continue
		}
		out = append(out, promlabels.MustNewMatcher(matcher.Type, matcher.Name, matcher.Value))
	}
	return out
}

// cloneVectorMatching is retained for use by planner.go. The canonical
// implementation lives in logical/build_helpers.go.
func cloneVectorMatching(vectorMatching *parser.VectorMatching) *parser.VectorMatching {
	if vectorMatching == nil {
		return nil
	}
	cloned := &parser.VectorMatching{
		Card:           vectorMatching.Card,
		MatchingLabels: append([]string(nil), vectorMatching.MatchingLabels...),
		On:             vectorMatching.On,
		Include:        append([]string(nil), vectorMatching.Include...),
	}
	if vectorMatching.FillValues.LHS != nil {
		lhs := *vectorMatching.FillValues.LHS
		cloned.FillValues.LHS = &lhs
	}
	if vectorMatching.FillValues.RHS != nil {
		rhs := *vectorMatching.FillValues.RHS
		cloned.FillValues.RHS = &rhs
	}
	return cloned
}
