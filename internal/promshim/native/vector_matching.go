package native

import "github.com/prometheus/prometheus/promql/parser"

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

func normalizeVectorMatching(vectorMatching *parser.VectorMatching) *parser.VectorMatching {
	if vectorMatching == nil {
		return &parser.VectorMatching{Card: parser.CardOneToOne}
	}
	return cloneVectorMatching(vectorMatching)
}
