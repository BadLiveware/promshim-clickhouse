package rules

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

const (
	maxExpansionDepth = 16
	matcherLabelName  = "__promshim_rule_match__"
)

type Expansion struct {
	Record       string            `json:"record"`
	Expr         string            `json:"expr"`
	Source       string            `json:"source"`
	Labels       map[string]string `json:"labels,omitempty"`
	Mode         string            `json:"mode"`
	DependsOn    []string          `json:"dependsOn,omitempty"`
	VirtualRange string            `json:"virtualRange,omitempty"`
	VirtualStep  string            `json:"virtualStep,omitempty"`
}

type ExpandResult struct {
	Expr       parser.Expr
	Expanded   bool
	Expansions []Expansion
}

type expandState struct {
	registry *Registry
	visiting map[string]struct{}
}

type ruleExpansion struct {
	expr       parser.Expr
	expansions []Expansion
	rule       RecordingRule
}

func ExpandExpr(expr parser.Expr, registry *Registry) (ExpandResult, error) {
	if registry.Empty() {
		return ExpandResult{Expr: expr}, nil
	}
	state := expandState{registry: registry, visiting: map[string]struct{}{}}
	expanded, expansions, changed, err := state.expand(expr)
	if err != nil {
		return ExpandResult{}, err
	}
	return ExpandResult{Expr: expanded, Expanded: changed, Expansions: expansions}, nil
}

func (s *expandState) expand(expr parser.Expr) (parser.Expr, []Expansion, bool, error) {
	switch n := expr.(type) {
	case *parser.VectorSelector:
		expanded, ok, err := s.expandVectorSelector(n)
		if err != nil || !ok {
			return n, nil, false, err
		}
		return expanded.expr, expanded.expansions, true, nil
	case *parser.AggregateExpr:
		child, exps, changed, err := s.expand(n.Expr)
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
		lhs, lhsExps, lhsChanged, err := s.expand(n.LHS)
		if err != nil {
			return nil, nil, false, err
		}
		rhs, rhsExps, rhsChanged, err := s.expand(n.RHS)
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
			newArg, exps, argChanged, err := s.expand(arg)
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
		child, exps, changed, err := s.expand(n.Expr)
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
		child, exps, changed, err := s.expand(n.Expr)
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
		child, exps, changed, err := s.expand(n.Expr)
		if err != nil {
			return nil, nil, false, err
		}
		if changed {
			clone := *n
			clone.Expr = child
			return &clone, markHistoricalExpansions(exps, "subquery_virtual", n.Range, n.Step), true, nil
		}
		return n, nil, false, nil
	case *parser.MatrixSelector:
		sel, ok := n.VectorSelector.(*parser.VectorSelector)
		if !ok {
			return n, nil, false, nil
		}
		expanded, ok, err := s.expandVectorSelector(sel)
		if err != nil || !ok {
			return n, nil, false, err
		}
		subquery := matrixSelectorToSubquery(n, sel, expanded)
		return subquery, markHistoricalExpansions(expanded.expansions, "range_virtual", n.Range, subquery.Step), true, nil
	default:
		return n, nil, false, nil
	}
}

func (s *expandState) expandVectorSelector(sel *parser.VectorSelector) (ruleExpansion, bool, error) {
	name := selectorMetricName(sel)
	if name == "" {
		return ruleExpansion{}, false, nil
	}
	if conflicts, ok := s.registry.Conflict(name); ok {
		return ruleExpansion{}, false, fmt.Errorf("recording rule %q is ambiguous across %d definitions", name, len(conflicts))
	}
	rule, ok := s.registry.Lookup(name)
	if !ok {
		return ruleExpansion{}, false, nil
	}
	staticLabels := mergedLabels(rule)
	staticLabels["__name__"] = rule.Name
	if empty, err := selectorStaticMismatch(sel.LabelMatchers, staticLabels); err != nil {
		return ruleExpansion{}, false, err
	} else if empty {
		return ruleExpansion{expr: emptyVectorExpr(), expansions: []Expansion{expansionForRule(rule, nil)}, rule: rule}, true, nil
	}
	child, childExps, err := s.expandRuleExpr(rule)
	if err != nil {
		return ruleExpansion{}, false, err
	}
	wrapped := wrapRuleExpression(child, rule)
	wrapped, err = applySelectorMatchers(wrapped, sel.LabelMatchers, staticLabels)
	if err != nil {
		return ruleExpansion{}, false, err
	}
	exps := append(childExps, expansionForRule(rule, childExps))
	return ruleExpansion{expr: wrapped, expansions: exps, rule: rule}, true, nil
}

func (s *expandState) expandRuleExpr(rule RecordingRule) (parser.Expr, []Expansion, error) {
	if expr, exps, ok := s.registry.CachedExpansion(rule.Name); ok {
		return expr, exps, nil
	}
	if len(s.visiting) >= maxExpansionDepth {
		return nil, nil, fmt.Errorf("recording rule expansion exceeds maximum depth %d at %q", maxExpansionDepth, rule.Name)
	}
	if _, ok := s.visiting[rule.Name]; ok {
		return nil, nil, fmt.Errorf("recording rule expansion cycle includes %q", rule.Name)
	}
	s.visiting[rule.Name] = struct{}{}
	defer delete(s.visiting, rule.Name)
	expanded, exps, _, err := s.expand(rule.Expr)
	if err != nil {
		return nil, nil, err
	}
	expanded = applyRuleQueryOffset(expanded, rule.QueryOffset)
	s.registry.storeCachedExpansion(rule.Name, expanded, exps)
	return expanded, exps, nil
}

func applyRuleQueryOffset(expr parser.Expr, offset time.Duration) parser.Expr {
	if offset == 0 || expr == nil {
		return expr
	}
	switch n := expr.(type) {
	case *parser.VectorSelector:
		clone := *n
		clone.OriginalOffset += offset
		clone.Offset += offset
		return &clone
	case *parser.MatrixSelector:
		clone := *n
		clone.VectorSelector = applyRuleQueryOffset(clone.VectorSelector, offset)
		return &clone
	case *parser.SubqueryExpr:
		clone := *n
		clone.OriginalOffset += offset
		clone.Offset += offset
		return &clone
	case *parser.AggregateExpr:
		clone := *n
		clone.Expr = applyRuleQueryOffset(clone.Expr, offset)
		return &clone
	case *parser.BinaryExpr:
		clone := *n
		clone.LHS = applyRuleQueryOffset(clone.LHS, offset)
		clone.RHS = applyRuleQueryOffset(clone.RHS, offset)
		return &clone
	case *parser.Call:
		clone := *n
		args := make(parser.Expressions, len(n.Args))
		for i, arg := range n.Args {
			args[i] = applyRuleQueryOffset(arg, offset)
		}
		clone.Args = args
		return &clone
	case *parser.UnaryExpr:
		clone := *n
		clone.Expr = applyRuleQueryOffset(clone.Expr, offset)
		return &clone
	case *parser.ParenExpr:
		clone := *n
		clone.Expr = applyRuleQueryOffset(clone.Expr, offset)
		return &clone
	case *parser.StepInvariantExpr:
		clone := *n
		clone.Expr = applyRuleQueryOffset(clone.Expr, offset)
		return &clone
	default:
		return expr
	}
}

func matrixSelectorToSubquery(matrix *parser.MatrixSelector, sel *parser.VectorSelector, expanded ruleExpansion) *parser.SubqueryExpr {
	step := time.Duration(0)
	if expanded.rule.Interval > 0 {
		step = expanded.rule.Interval
	}
	return &parser.SubqueryExpr{
		Expr:               expanded.expr,
		Range:              matrix.Range,
		RangeExpr:          matrix.RangeExpr,
		OriginalOffset:     sel.OriginalOffset,
		OriginalOffsetExpr: sel.OriginalOffsetExpr,
		Offset:             sel.Offset,
		Timestamp:          cloneInt64Pointer(sel.Timestamp),
		StartOrEnd:         sel.StartOrEnd,
		Step:               step,
		EndPos:             matrix.EndPos,
	}
}

func expansionForRule(rule RecordingRule, childExps []Expansion) Expansion {
	return Expansion{Record: rule.Name, Expr: rule.ExprString, Source: rule.Source, Labels: mergedLabels(rule), Mode: "instant_virtual", DependsOn: expansionRecords(childExps)}
}

func expansionRecords(expansions []Expansion) []string {
	seen := map[string]struct{}{}
	var records []string
	for _, expansion := range expansions {
		if expansion.Record == "" {
			continue
		}
		if _, ok := seen[expansion.Record]; ok {
			continue
		}
		seen[expansion.Record] = struct{}{}
		records = append(records, expansion.Record)
	}
	sort.Strings(records)
	return records
}

func markHistoricalExpansions(expansions []Expansion, mode string, window, step time.Duration) []Expansion {
	out := append([]Expansion(nil), expansions...)
	for i := range out {
		out[i].Mode = mode
		if window > 0 {
			out[i].VirtualRange = window.String()
		}
		if step > 0 {
			out[i].VirtualStep = step.String()
		}
	}
	return out
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
		predicate := selectorMatcherPredicate(expr, m)
		switch m.Type {
		case labels.MatchEqual, labels.MatchRegexp:
			wrapped = &parser.BinaryExpr{Op: parser.LAND, LHS: wrapped, RHS: predicate, VectorMatching: &parser.VectorMatching{Card: parser.CardManyToMany, MatchingLabels: []string{m.Name}, On: true}}
		case labels.MatchNotEqual, labels.MatchNotRegexp:
			wrapped = &parser.BinaryExpr{Op: parser.LUNLESS, LHS: wrapped, RHS: predicate, VectorMatching: &parser.VectorMatching{Card: parser.CardManyToMany, MatchingLabels: []string{m.Name}, On: true}}
		default:
			return nil, fmt.Errorf("recording rule selector matcher %s on dynamic label %q is not supported", m.Type, m.Name)
		}
	}
	return wrapped, nil
}

func selectorMatcherPredicate(expr parser.Expr, matcher *labels.Matcher) parser.Expr {
	if matcher.Type == labels.MatchEqual || matcher.Type == labels.MatchNotEqual {
		return labelReplace(vectorLiteral(1), matcher.Name, matcher.Value)
	}
	marked := labelReplaceFrom(expr, matcherLabelName, "1", matcher.Name, matcher.Value)
	marker := labelReplace(vectorLiteral(1), matcherLabelName, "1")
	return &parser.BinaryExpr{Op: parser.LAND, LHS: marked, RHS: marker, VectorMatching: &parser.VectorMatching{Card: parser.CardManyToMany, MatchingLabels: []string{matcherLabelName}, On: true}}
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
	return labelReplaceFrom(expr, dst, value, "", ".*")
}

func labelReplaceFrom(expr parser.Expr, dst, value, src, regex string) parser.Expr {
	call, err := parser.NewParser(parser.Options{EnableBinopFillModifiers: true, EnableExperimentalFunctions: true}).ParseExpr(fmt.Sprintf(`label_replace(vector(0), %q, %q, %q, %q)`, dst, value, src, regex))
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

func cloneInt64Pointer(in *int64) *int64 {
	if in == nil {
		return nil
	}
	out := *in
	return &out
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
