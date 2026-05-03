package renderer

import (
	"fmt"
	"sort"
	"strings"

	logicalpkg "github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/native"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/native/physical"
	"github.com/prometheus/prometheus/promql/parser"
)

type staticLabelUnionBranch struct {
	Child  logicalpkg.Node
	Labels map[string]string
}

func tryLowerStaticLabelUnion(ctx LoweringCtx, n *logicalpkg.BinaryPlan) (RenderedQuery, bool, error) {
	branches, ok := staticLabelUnionBranches(n)
	if !ok || len(branches) < 2 {
		return RenderedQuery{}, false, nil
	}
	base := branches[0].Child
	baseKey := staticLabelUnionBaseKey(base)
	if baseKey == "" {
		return RenderedQuery{}, false, nil
	}
	labelNamesKey := staticLabelUnionLabelNamesSignature(branches[0].Labels)
	allSameBase := true
	seenLabels := map[string]struct{}{}
	for _, branch := range branches {
		branchBaseKey := staticLabelUnionBaseKey(branch.Child)
		if branchBaseKey == "" {
			return RenderedQuery{}, false, nil
		}
		if branchBaseKey != baseKey {
			allSameBase = false
		}
		if staticLabelUnionLabelNamesSignature(branch.Labels) != labelNamesKey {
			return RenderedQuery{}, false, nil
		}
		signature := staticLabelUnionLabelSignature(branch.Labels)
		if _, ok := seenLabels[signature]; ok {
			return RenderedQuery{}, false, nil
		}
		seenLabels[signature] = struct{}{}
	}
	var (
		rq  RenderedQuery
		err error
	)
	if allSameBase {
		rq, err = renderStaticLabelUnion(ctx, base, branches)
	} else {
		rq, err = renderStaticLabelDisjointUnion(ctx, branches)
	}
	if err != nil {
		return RenderedQuery{}, true, err
	}
	return rq, true, nil
}

func staticLabelUnionBranches(root logicalpkg.Node) ([]staticLabelUnionBranch, bool) {
	var branches []staticLabelUnionBranch
	if !collectStaticLabelUnionBranches(root, &branches) {
		return nil, false
	}
	return branches, true
}

func collectStaticLabelUnionBranches(node logicalpkg.Node, branches *[]staticLabelUnionBranch) bool {
	if binary, ok := node.(*logicalpkg.BinaryPlan); ok && binary.Op == parser.LOR && isSimpleManyToManyOr(binary.VectorMatching) && !binary.ReturnBool {
		return collectStaticLabelUnionBranches(binary.LHS, branches) && collectStaticLabelUnionBranches(binary.RHS, branches)
	}
	child, labels, ok := peelStaticLabelSet(node)
	if !ok || len(labels) == 0 {
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

func peelStaticLabelSet(node logicalpkg.Node) (logicalpkg.Node, map[string]string, bool) {
	labels := map[string]string{}
	for {
		plan, ok := node.(*logicalpkg.LabelReplacePlan)
		if !ok {
			break
		}
		value, ok := staticLabelReplaceValue(plan)
		if !ok {
			return nil, nil, false
		}
		if _, exists := labels[plan.Config.Dst]; !exists {
			labels[plan.Config.Dst] = value
		}
		node = plan.Child
	}
	if node == nil {
		return nil, nil, false
	}
	return node, labels, true
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
