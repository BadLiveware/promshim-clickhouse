package rules

import (
	"fmt"
	"sort"
	"strings"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

type Expansion struct {
	Record string            `json:"record"`
	Expr   string            `json:"expr"`
	Source string            `json:"source"`
	Labels map[string]string `json:"labels,omitempty"`
	Mode   string            `json:"mode"`
}

type ExpandResult struct {
	Expr       parser.Expr
	Expanded   bool
	Expansions []Expansion
}

func ExpandExpr(expr parser.Expr, registry *Registry) (ExpandResult, error) {
	if registry == nil || registry.Len() == 0 {
		return ExpandResult{Expr: expr}, nil
	}
	expanded, expansions, changed, err := expand(expr, registry)
	if err != nil {
		return ExpandResult{}, err
	}
	return ExpandResult{Expr: expanded, Expanded: changed, Expansions: expansions}, nil
}

func expand(expr parser.Expr, registry *Registry) (parser.Expr, []Expansion, bool, error) {
	switch n := expr.(type) {
	case *parser.VectorSelector:
		return expandVectorSelector(n, registry)
	case *parser.AggregateExpr:
		child, exps, changed, err := expand(n.Expr, registry)
		if err != nil {
			return nil, nil, false, err
		}
		if changed {
			clone := *n
			clone.Expr = child
			return &clone, exps, true, nil
		}
		return n, nil, false, nil
	case *parser.BinaryExpr:
		lhs, lhsExps, lhsChanged, err := expand(n.LHS, registry)
		if err != nil {
			return nil, nil, false, err
		}
		rhs, rhsExps, rhsChanged, err := expand(n.RHS, registry)
		if err != nil {
			return nil, nil, false, err
		}
		if lhsChanged || rhsChanged {
			clone := *n
			clone.LHS = lhs
			clone.RHS = rhs
			return &clone, append(lhsExps, rhsExps...), true, nil
		}
		return n, nil, false, nil
	case *parser.Call:
		args := make(parser.Expressions, len(n.Args))
		copy(args, n.Args)
		var all []Expansion
		changed := false
		for i, arg := range args {
			newArg, exps, argChanged, err := expand(arg, registry)
			if err != nil {
				return nil, nil, false, err
			}
			if argChanged {
				args[i] = newArg
				all = append(all, exps...)
				changed = true
			}
		}
		if changed {
			clone := *n
			clone.Args = args
			return &clone, all, true, nil
		}
		return n, nil, false, nil
	case *parser.UnaryExpr:
		child, exps, changed, err := expand(n.Expr, registry)
		if err != nil {
			return nil, nil, false, err
		}
		if changed {
			clone := *n
			clone.Expr = child
			return &clone, exps, true, nil
		}
		return n, nil, false, nil
	case *parser.ParenExpr:
		child, exps, changed, err := expand(n.Expr, registry)
		if err != nil {
			return nil, nil, false, err
		}
		if changed {
			clone := *n
			clone.Expr = child
			return &clone, exps, true, nil
		}
		return n, nil, false, nil
	case *parser.SubqueryExpr:
		_, _, changed, err := expand(n.Expr, registry)
		if err != nil {
			return nil, nil, false, err
		}
		if changed {
			return nil, nil, false, fmt.Errorf("recording rule subqueries require materialization or bounded virtual-history support")
		}
		return n, nil, false, nil
	case *parser.MatrixSelector:
		if sel, ok := n.VectorSelector.(*parser.VectorSelector); ok && selectorMetricName(sel) != "" {
			if _, ok := registry.Lookup(selectorMetricName(sel)); ok {
				return nil, nil, false, fmt.Errorf("recording rule range selectors require materialization or bounded virtual-history support")
			}
			if conflicts, ok := registry.Conflict(selectorMetricName(sel)); ok {
				return nil, nil, false, fmt.Errorf("recording rule %q is ambiguous across %d definitions", selectorMetricName(sel), len(conflicts))
			}
		}
		return n, nil, false, nil
	default:
		return n, nil, false, nil
	}
}

func expandVectorSelector(sel *parser.VectorSelector, registry *Registry) (parser.Expr, []Expansion, bool, error) {
	name := selectorMetricName(sel)
	if name == "" {
		return sel, nil, false, nil
	}
	if conflicts, ok := registry.Conflict(name); ok {
		return nil, nil, false, fmt.Errorf("recording rule %q is ambiguous across %d definitions", name, len(conflicts))
	}
	rule, ok := registry.Lookup(name)
	if !ok {
		return sel, nil, false, nil
	}
	staticLabels := mergedLabels(rule)
	staticLabels["__name__"] = rule.Name
	if empty, err := selectorStaticMismatch(sel.LabelMatchers, staticLabels); err != nil {
		return nil, nil, false, err
	} else if empty {
		return emptyVectorExpr(), []Expansion{{Record: rule.Name, Expr: rule.ExprString, Source: rule.Source, Labels: mergedLabels(rule), Mode: "instant_virtual"}}, true, nil
	}
	child, err := parser.NewParser(parser.Options{EnableBinopFillModifiers: true, EnableExperimentalFunctions: true}).ParseExpr(rule.ExprString)
	if err != nil {
		return nil, nil, false, fmt.Errorf("parsing recording rule %q expression: %w", name, err)
	}
	wrapped := wrapRuleExpression(child, rule)
	wrapped, err = applySelectorMatchers(wrapped, sel.LabelMatchers, staticLabels)
	if err != nil {
		return nil, nil, false, err
	}
	return wrapped, []Expansion{{Record: rule.Name, Expr: rule.ExprString, Source: rule.Source, Labels: mergedLabels(rule), Mode: "instant_virtual"}}, true, nil
}

func selectorStaticMismatch(matchers []*labels.Matcher, staticLabels map[string]string) (bool, error) {
	for _, m := range matchers {
		if m == nil {
			continue
		}
		staticValue, ok := staticLabels[m.Name]
		if !ok {
			continue
		}
		if !m.Matches(staticValue) {
			return true, nil
		}
	}
	return false, nil
}

func selectorMetricName(sel *parser.VectorSelector) string {
	if sel == nil {
		return ""
	}
	if sel.Name != "" {
		return sel.Name
	}
	for _, m := range sel.LabelMatchers {
		if m != nil && m.Name == "__name__" && m.Type == labels.MatchEqual {
			return m.Value
		}
	}
	return ""
}

func wrapRuleExpression(expr parser.Expr, rule RecordingRule) parser.Expr {
	wrapped := expr
	labelsToSet := mergedLabels(rule)
	labelsToSet["__name__"] = rule.Name
	keys := make([]string, 0, len(labelsToSet))
	for k := range labelsToSet {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	// Deterministic wrapping keeps tests and explain output stable.
	for _, name := range keys {
		value := labelsToSet[name]
		wrapped = labelReplace(wrapped, name, value)
	}
	return wrapped
}

func applySelectorMatchers(expr parser.Expr, matchers []*labels.Matcher, staticLabels map[string]string) (parser.Expr, error) {
	wrapped := expr
	for _, m := range matchers {
		if m == nil || m.Name == "__name__" {
			continue
		}
		if _, ok := staticLabels[m.Name]; ok {
			continue
		}
		if m.Type != labels.MatchEqual {
			return nil, fmt.Errorf("recording rule selector matcher %s on dynamic label %q is not supported", m.Type, m.Name)
		}
		wrapped = &parser.BinaryExpr{Op: parser.LAND, LHS: wrapped, RHS: labelReplace(vectorLiteral(1), m.Name, m.Value), VectorMatching: &parser.VectorMatching{Card: parser.CardManyToMany, MatchingLabels: []string{m.Name}, On: true}}
	}
	return wrapped, nil
}

func vectorLiteral(value float64) parser.Expr {
	expr, err := parser.NewParser(parser.Options{EnableBinopFillModifiers: true, EnableExperimentalFunctions: true}).ParseExpr(fmt.Sprintf("vector(%g)", value))
	if err != nil {
		return &parser.NumberLiteral{Val: value}
	}
	return expr
}

func emptyVectorExpr() parser.Expr {
	expr, err := parser.NewParser(parser.Options{EnableBinopFillModifiers: true, EnableExperimentalFunctions: true}).ParseExpr(`vector(0) unless vector(0)`)
	if err != nil {
		return &parser.NumberLiteral{Val: 0}
	}
	return expr
}

func labelReplace(expr parser.Expr, dst, value string) parser.Expr {
	call, err := parser.NewParser(parser.Options{EnableBinopFillModifiers: true, EnableExperimentalFunctions: true}).ParseExpr(fmt.Sprintf(`label_replace(vector(0), %q, %q, "", ".*")`, dst, value))
	if err != nil {
		return expr
	}
	parsed := call.(*parser.Call)
	parsed.Args[0] = expr
	return parsed
}

func mergedLabels(rule RecordingRule) map[string]string {
	out := map[string]string{}
	for k, v := range rule.GroupLabels {
		out[k] = v
	}
	for k, v := range rule.Labels {
		out[k] = v
	}
	return out
}

func ParseMode(value string) (Mode, error) {
	switch Mode(strings.ToLower(strings.TrimSpace(value))) {
	case "", ModeOff:
		return ModeOff, nil
	case ModeVirtual:
		return ModeVirtual, nil
	default:
		return "", fmt.Errorf("invalid recording rule mode %q", value)
	}
}
