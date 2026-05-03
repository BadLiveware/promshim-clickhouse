package renderer

import (
	"fmt"
	"sort"
	"strings"

	logicalpkg "github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/native"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/native/physical"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

type staticLabelUnionBranch struct {
	Child  logicalpkg.Node
	Labels map[string]string
}

const (
	staticLabelUnionSkipReasonDynamicLabelMutation      = "dynamic_label_mutation"
	staticLabelUnionSkipReasonIncompatibleStaticLabels  = "incompatible_static_labels"
	staticLabelUnionSkipReasonUnsupportedVectorMatching = "unsupported_vector_matching"
	staticLabelUnionSkipReasonUnsafeSelectorOverlap     = "unsafe_selector_overlap"
)

func tryLowerStaticLabelUnion(ctx LoweringCtx, n *logicalpkg.BinaryPlan) (RenderedQuery, bool, error) {
	branches, rejectReason, ok := staticLabelUnionBranches(n)
	if !ok {
		if len(branches) >= 2 {
			reportStaticLabelUnion(ctx, staticLabelUnionDecision(false, len(branches), 0, 0, "", rejectReason))
		}
		return RenderedQuery{}, false, nil
	}
	if len(branches) < 2 {
		return RenderedQuery{}, false, nil
	}
	base := branches[0].Child
	baseKey := staticLabelUnionCanonicalBaseKey(base)
	if baseKey == "" {
		reportStaticLabelUnion(ctx, staticLabelUnionDecision(false, len(branches), 0, 0, "", staticLabelUnionSkipReasonDynamicLabelMutation))
		return RenderedQuery{}, false, nil
	}
	labelNamesKey := staticLabelUnionLabelNamesSignature(branches[0].Labels)
	allSameCanonicalBase := true
	seenLabels := map[string]struct{}{}
	for _, branch := range branches {
		branchBaseKey := staticLabelUnionCanonicalBaseKey(branch.Child)
		if branchBaseKey == "" {
			reportStaticLabelUnion(ctx, staticLabelUnionDecision(false, len(branches), 0, 0, "", staticLabelUnionSkipReasonDynamicLabelMutation))
			return RenderedQuery{}, false, nil
		}
		if branchBaseKey != baseKey {
			allSameCanonicalBase = false
		}
		if staticLabelUnionLabelNamesSignature(branch.Labels) != labelNamesKey {
			reportStaticLabelUnion(ctx, staticLabelUnionDecision(false, len(branches), 0, 0, "", staticLabelUnionSkipReasonIncompatibleStaticLabels))
			return RenderedQuery{}, false, nil
		}
		signature := staticLabelUnionLabelSignature(branch.Labels)
		if _, ok := seenLabels[signature]; ok {
			reportStaticLabelUnion(ctx, staticLabelUnionDecision(false, len(branches), 0, 0, "", staticLabelUnionSkipReasonUnsafeSelectorOverlap))
			return RenderedQuery{}, false, nil
		}
		seenLabels[signature] = struct{}{}
	}
	var (
		rq             RenderedQuery
		err            error
		candidateCount int
		collapsedRows  int
		mode           string
	)
	candidateCount = len(branches)
	if allSameCanonicalBase {
		mode = "shared_selector_child"
		if candidateCount > 1 {
			collapsedRows = candidateCount - 1
		}
		rq, err = renderStaticLabelUnion(ctx, base, branches)
	} else {
		mode = "disjoint_children"
		rq, err = renderStaticLabelDisjointUnion(ctx, branches)
	}
	if err != nil {
		return RenderedQuery{}, true, err
	}
	reportStaticLabelUnion(ctx, native.StaticLabelUnionDecision{
		Applied:           true,
		CandidateBranches: candidateCount,
		CollapsedRows:     collapsedRows,
		RemainingGroups:   candidateCount,
		Mode:              mode,
	})
	return rq, true, nil
}

func staticLabelUnionBranches(root logicalpkg.Node) ([]staticLabelUnionBranch, string, bool) {
	var branches []staticLabelUnionBranch
	reason := ""
	if !collectStaticLabelUnionBranches(root, &branches, &reason) {
		if reason == "" {
			reason = staticLabelUnionSkipReasonUnsupportedVectorMatching
		}
		return branches, reason, false
	}
	return branches, "", true
}

func collectStaticLabelUnionBranches(node logicalpkg.Node, branches *[]staticLabelUnionBranch, reason *string) bool {
	if node == nil {
		if reason != nil {
			*reason = staticLabelUnionSkipReasonUnsupportedVectorMatching
		}
		return false
	}
	if binary, ok := node.(*logicalpkg.BinaryPlan); ok && binary.Op == parser.LOR && isSimpleManyToManyOr(binary.VectorMatching) && !binary.ReturnBool {
		if !collectStaticLabelUnionBranches(binary.LHS, branches, reason) {
			return false
		}
		return collectStaticLabelUnionBranches(binary.RHS, branches, reason)
	}
	if _, ok := node.(*logicalpkg.BinaryPlan); ok {
		if reason != nil {
			*reason = staticLabelUnionSkipReasonUnsupportedVectorMatching
		}
		return false
	}
	child, labels, ok, reasonHint := peelStaticLabelSet(node)
	if !ok || len(labels) == 0 {
		if reason != nil {
			*reason = reasonHint
		}
		return false
	}
	*branches = append(*branches, staticLabelUnionBranch{Child: child, Labels: labels})
	return true
}

func isSimpleManyToManyOr(m *parser.VectorMatching) bool {
	if m == nil {
		return true
	}
	return m.Card == parser.CardManyToMany && !m.On && len(m.MatchingLabels) == 0 && len(m.Include) == 0
}

func peelStaticLabelSet(node logicalpkg.Node) (logicalpkg.Node, map[string]string, bool, string) {
	labels := map[string]string{}
	for {
		plan, ok := node.(*logicalpkg.LabelReplacePlan)
		if !ok {
			break
		}
		value, ok := staticLabelReplaceValue(plan)
		if !ok {
			return nil, nil, false, staticLabelUnionSkipReasonDynamicLabelMutation
		}
		if _, exists := labels[plan.Config.Dst]; !exists {
			labels[plan.Config.Dst] = value
		}
		node = plan.Child
	}
	if node == nil {
		return nil, nil, false, staticLabelUnionSkipReasonDynamicLabelMutation
	}
	if len(labels) == 0 {
		return nil, nil, false, staticLabelUnionSkipReasonDynamicLabelMutation
	}
	return node, labels, true, ""
}

func staticLabelReplaceValue(plan *logicalpkg.LabelReplacePlan) (string, bool) {
	if plan == nil || plan.Config.Regex == nil {
		return "", false
	}
	if plan.Config.Src != "" || plan.Config.Regex.String() != "^(?s:.*)$" {
		return "", false
	}
	if strings.Contains(plan.Config.Repl, "$") {
		return "", false
	}
	return plan.Config.Repl, true
}

func staticLabelUnionDecision(applied bool, candidateBranches int, collapsedRows int, remainingGroups int, mode, skipReason string) native.StaticLabelUnionDecision {
	return native.StaticLabelUnionDecision{
		Applied:           applied,
		CandidateBranches: candidateBranches,
		CollapsedRows:     collapsedRows,
		RemainingGroups:   remainingGroups,
		Mode:              mode,
		SkipReason:        skipReason,
	}
}

func reportStaticLabelUnion(ctx LoweringCtx, decision native.StaticLabelUnionDecision) {
	if ctx.OptimizationReport == nil {
		return
	}
	ctx.OptimizationReport.StaticLabelUnionDecisions = append(ctx.OptimizationReport.StaticLabelUnionDecisions, decision)
}

const (
	staticLabelUnionSelectorValuePlaceholder = "__selector_value__"
)

func staticLabelUnionBaseKey(node logicalpkg.Node) string {
	if node == nil {
		return ""
	}
	described, ok := node.(interface {
		ExprString() string
		ValueType() parser.ValueType
	})
	if !ok || described.ExprString() == "" {
		return ""
	}
	return described.ExprString() + "\x00" + string(described.ValueType())
}

// staticLabelUnionCanonicalBaseKey returns a shape-based key for analysis tooling.
// It preserves the full logical shape while normalizing exact selector values,
// allowing callers to reason about selector-variant families without changing
// optimization behavior.
func staticLabelUnionCanonicalBaseKey(node logicalpkg.Node) string {
	if node == nil {
		return ""
	}
	described, ok := node.(interface {
		ExprString() string
		ValueType() parser.ValueType
	})
	if !ok || described.ExprString() == "" {
		return ""
	}
	expr, err := parser.NewParser(parser.Options{}).ParseExpr(described.ExprString())
	if err != nil {
		return described.ExprString() + "\x00" + string(described.ValueType())
	}
	return staticLabelUnionCanonicalExpr(expr).String() + "\x00" + string(described.ValueType())
}

func staticLabelUnionCanonicalExpr(expr parser.Expr) parser.Expr {
	switch e := expr.(type) {
	case *parser.StepInvariantExpr:
		return &parser.StepInvariantExpr{Expr: staticLabelUnionCanonicalExpr(e.Expr)}
	case *parser.MatrixSelector:
		vectorSelector, _ := staticLabelUnionCanonicalExpr(e.VectorSelector).(*parser.VectorSelector)
		return &parser.MatrixSelector{VectorSelector: vectorSelector, Range: e.Range, RangeExpr: e.RangeExpr, EndPos: e.EndPos}
	case *parser.SubqueryExpr:
		return &parser.SubqueryExpr{Expr: staticLabelUnionCanonicalExpr(e.Expr), Range: e.Range, RangeExpr: e.RangeExpr, OriginalOffset: e.OriginalOffset, OriginalOffsetExpr: e.OriginalOffsetExpr, Offset: e.Offset, Timestamp: e.Timestamp, StartOrEnd: e.StartOrEnd, Step: e.Step, StepExpr: e.StepExpr, EndPos: e.EndPos}
	case *parser.BinaryExpr:
		return &parser.BinaryExpr{Op: e.Op, LHS: staticLabelUnionCanonicalExpr(e.LHS), RHS: staticLabelUnionCanonicalExpr(e.RHS), VectorMatching: staticLabelUnionCloneVectorMatching(e.VectorMatching), ReturnBool: e.ReturnBool}
	case *parser.AggregateExpr:
		grouping := append([]string{}, e.Grouping...)
		sort.Strings(grouping)
		return &parser.AggregateExpr{Op: e.Op, Expr: staticLabelUnionCanonicalExpr(e.Expr), Param: staticLabelUnionCanonicalExpr(e.Param), Grouping: grouping, Without: e.Without, PosRange: e.PosRange}
	case *parser.Call:
		args := make(parser.Expressions, len(e.Args))
		for i, arg := range e.Args {
			args[i] = staticLabelUnionCanonicalExpr(arg)
		}
		return &parser.Call{Func: e.Func, Args: args, PosRange: e.PosRange}
	case *parser.VectorSelector:
		return staticLabelUnionCanonicalVectorSelector(e)
	case *parser.ParenExpr:
		return &parser.ParenExpr{Expr: staticLabelUnionCanonicalExpr(e.Expr), PosRange: e.PosRange}
	case *parser.UnaryExpr:
		return &parser.UnaryExpr{Op: e.Op, Expr: staticLabelUnionCanonicalExpr(e.Expr), StartPos: e.StartPos}
	case *parser.DurationExpr:
		return &parser.DurationExpr{Op: e.Op, LHS: staticLabelUnionCanonicalExpr(e.LHS), RHS: staticLabelUnionCanonicalExpr(e.RHS), Wrapped: e.Wrapped, StartPos: e.StartPos, EndPos: e.EndPos}
	default:
		return expr
	}
}

func staticLabelUnionCanonicalVectorSelector(sel *parser.VectorSelector) *parser.VectorSelector {
	if sel == nil {
		return nil
	}
	out := *sel
	if len(sel.LabelMatchers) == 0 {
		return &out
	}
	outMatchers := make([]*labels.Matcher, 0, len(sel.LabelMatchers))
	for _, matcher := range sel.LabelMatchers {
		if matcher == nil {
			outMatchers = append(outMatchers, nil)
			continue
		}
		clone := *matcher
		if matcher.Type == labels.MatchEqual {
			clone.Value = staticLabelUnionSelectorValuePlaceholder
		}
		outMatchers = append(outMatchers, &clone)
	}
	sort.SliceStable(outMatchers, func(i, j int) bool {
		a := outMatchers[i]
		b := outMatchers[j]
		if a == nil {
			return b != nil
		}
		if b == nil {
			return false
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		return a.Value < b.Value
	})
	out.LabelMatchers = outMatchers
	return &out
}

func staticLabelUnionCloneVectorMatching(vm *parser.VectorMatching) *parser.VectorMatching {
	if vm == nil {
		return nil
	}
	out := *vm
	out.MatchingLabels = append([]string{}, vm.MatchingLabels...)
	sort.Strings(out.MatchingLabels)
	out.Include = append([]string{}, vm.Include...)
	sort.Strings(out.Include)
	out.FillValues = parser.VectorMatchFillValues{}
	if vm.FillValues.LHS != nil {
		lhs := *vm.FillValues.LHS
		out.FillValues.LHS = &lhs
	}
	if vm.FillValues.RHS != nil {
		rhs := *vm.FillValues.RHS
		out.FillValues.RHS = &rhs
	}
	return &out
}

func staticLabelUnionLabelSignature(labels map[string]string) string {
	keys := staticLabelUnionSortedLabelNames(labels)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+labels[key])
	}
	return strings.Join(parts, "\xff")
}

func staticLabelUnionLabelNamesSignature(labels map[string]string) string {
	return strings.Join(staticLabelUnionSortedLabelNames(labels), "\xff")
}

func staticLabelUnionSortedLabelNames(labels map[string]string) []string {
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func renderStaticLabelUnion(ctx LoweringCtx, child logicalpkg.Node, branches []staticLabelUnionBranch) (RenderedQuery, error) {
	childRendered, err := Lower(ctx, child)
	if err != nil {
		return RenderedQuery{}, err
	}
	defs, err := staticLabelDefinitionsSQL(branches)
	if err != nil {
		return RenderedQuery{}, err
	}
	childSQL := trimRenderedQuerySQL(childRendered.SQL)
	tagsExpr := "arraySort(tag -> tag.1, arrayConcat(arrayFilter(tag -> NOT has(tupleElement(rule_def, 2), tag.1), union_child.tags), tupleElement(rule_def, 1)))"
	var sql string
	switch ctx.Params.Mode {
	case native.RenderModeInstant:
		rowsSQL := "SELECT " + tagsExpr + " AS tags, union_child.timestamp AS timestamp, union_child.value AS value FROM (" + childSQL + ") AS union_child ARRAY JOIN " + defs + " AS rule_def"
		sql = "SELECT tags, timestamp, any(value) AS value FROM (" + rowsSQL + ") AS static_label_union_rows GROUP BY tags, timestamp HAVING throwIf(count() > 1, 'vector cannot contain metrics with the same labelset') = 0"
	case native.RenderModeRange:
		rowsSQL := "SELECT " + tagsExpr + " AS tags, union_child.time_series AS time_series FROM (" + childSQL + ") AS union_child ARRAY JOIN " + defs + " AS rule_def"
		groupedSQL := "SELECT tags, arraySort(item -> item.1, arrayFlatten(groupArray(time_series))) AS time_series FROM (" + rowsSQL + ") AS static_label_union_rows GROUP BY tags"
		dupeExpr := "arrayExists((idx, point) -> if(idx = 1, 0, tupleElement(point, 1) = tupleElement(arrayElement(time_series, idx - 1), 1)), arrayEnumerate(time_series), time_series)"
		sql = "SELECT tags, time_series FROM (" + groupedSQL + ") AS static_label_union_series WHERE throwIf(" + dupeExpr + ", 'vector cannot contain metrics with the same labelset') = 0"
	default:
		return RenderedQuery{}, fmt.Errorf("unknown render mode %q", ctx.Params.Mode)
	}
	return finalizeRenderedFragment(renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: childRendered.QueryParams, ExtraSettings: childRendered.QuerySettings, ExtraPhysicalDecisions: childRendered.PhysicalDecisions})
}

func renderStaticLabelDisjointUnion(ctx LoweringCtx, branches []staticLabelUnionBranch) (RenderedQuery, error) {
	var (
		rows      []string
		params    = map[string]string{}
		settings  = map[string]any{}
		decisions []physical.Decision
		rangeMode = ctx.Params.Mode == native.RenderModeRange
	)
	for i, branch := range branches {
		childRendered, err := Lower(ctx, branch.Child)
		if err != nil {
			return RenderedQuery{}, err
		}
		alias := fmt.Sprintf("static_label_union_%d", i)
		childSQL, namespacedParams, err := namespaceRenderedQuery(trimRenderedQuerySQL(childRendered.SQL), childRendered.QueryParams, alias)
		if err != nil {
			return RenderedQuery{}, err
		}
		for key, value := range namespacedParams {
			params[key] = value
		}
		mergeRenderedQuerySettings(settings, childRendered.QuerySettings)
		decisions = appendRenderedQueryPhysicalDecisions(decisions, childRendered.PhysicalDecisions...)
		rowSQL, err := staticLabelUnionBranchRowsSQL(childSQL, branch.Labels, rangeMode)
		if err != nil {
			return RenderedQuery{}, err
		}
		rows = append(rows, rowSQL)
	}
	unionSQL := strings.Join(rows, " UNION ALL ")
	var sql string
	switch ctx.Params.Mode {
	case native.RenderModeInstant:
		sql = "SELECT tags, timestamp, any(value) AS value FROM (" + unionSQL + ") AS static_label_union_rows GROUP BY tags, timestamp HAVING throwIf(count() > 1, 'vector cannot contain metrics with the same labelset') = 0"
	case native.RenderModeRange:
		groupedSQL := "SELECT tags, arraySort(item -> item.1, arrayFlatten(groupArray(time_series))) AS time_series FROM (" + unionSQL + ") AS static_label_union_rows GROUP BY tags"
		dupeExpr := "arrayExists((idx, point) -> if(idx = 1, 0, tupleElement(point, 1) = tupleElement(arrayElement(time_series, idx - 1), 1)), arrayEnumerate(time_series), time_series)"
		sql = "SELECT tags, time_series FROM (" + groupedSQL + ") AS static_label_union_series WHERE throwIf(" + dupeExpr + ", 'vector cannot contain metrics with the same labelset') = 0"
	default:
		return RenderedQuery{}, fmt.Errorf("unknown render mode %q", ctx.Params.Mode)
	}
	return finalizeRenderedFragment(renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: params, ExtraSettings: settings, ExtraPhysicalDecisions: decisions})
}

func staticLabelUnionBranchRowsSQL(childSQL string, labels map[string]string, rangeMode bool) (string, error) {
	labelArray, labelNames, err := staticLabelArraysSQL(labels)
	if err != nil {
		return "", err
	}
	tagsExpr := "arraySort(tag -> tag.1, arrayConcat(arrayFilter(tag -> NOT has(" + labelNames + ", tag.1), union_child.tags), " + labelArray + "))"
	if rangeMode {
		return "SELECT " + tagsExpr + " AS tags, union_child.time_series AS time_series FROM (" + childSQL + ") AS union_child", nil
	}
	return "SELECT " + tagsExpr + " AS tags, union_child.timestamp AS timestamp, union_child.value AS value FROM (" + childSQL + ") AS union_child", nil
}

func staticLabelArraysSQL(labels map[string]string) (string, string, error) {
	if len(labels) == 0 {
		return "", "", fmt.Errorf("static label union branch has no labels")
	}
	keys := staticLabelUnionSortedLabelNames(labels)
	labelTuples := make([]string, 0, len(keys))
	labelNames := make([]string, 0, len(keys))
	for _, key := range keys {
		labelTuples = append(labelTuples, "tuple("+sqlStringLiteral(key)+", "+sqlStringLiteral(labels[key])+")")
		labelNames = append(labelNames, sqlStringLiteral(key))
	}
	return "CAST([" + strings.Join(labelTuples, ", ") + "], 'Array(Tuple(String, String))')", "[" + strings.Join(labelNames, ", ") + "]", nil
}

func staticLabelDefinitionsSQL(branches []staticLabelUnionBranch) (string, error) {
	if len(branches) == 0 {
		return "", fmt.Errorf("static label union requires at least one branch")
	}
	defs := make([]string, 0, len(branches))
	seen := map[string]struct{}{}
	for _, branch := range branches {
		if len(branch.Labels) == 0 {
			return "", fmt.Errorf("static label union branch has no labels")
		}
		labelArray, labelNames, err := staticLabelArraysSQL(branch.Labels)
		if err != nil {
			return "", err
		}
		signature := staticLabelUnionLabelSignature(branch.Labels)
		if _, ok := seen[signature]; ok {
			return "", fmt.Errorf("static label union contains duplicate output label set")
		}
		seen[signature] = struct{}{}
		defs = append(defs, "tuple("+labelArray+", "+labelNames+")")
	}
	return "[" + strings.Join(defs, ", ") + "]", nil
}
