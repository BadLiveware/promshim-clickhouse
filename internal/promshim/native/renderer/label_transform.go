package renderer

import (
	"fmt"
	"strconv"
	"strings"

	logicalpkg "ch-observability/internal/promshim/logical"
	modelpkg "ch-observability/internal/promshim/model"
	"ch-observability/internal/promshim/native"
	"ch-observability/internal/promshim/storage"
)

func renderLabelTransformFragment(cfg storage.QueryConfig, fragment *native.NativeFragment, params RenderParams) (renderedFragment, error) {
	if fragment == nil || fragment.LabelTransform == nil || fragment.LabelTransform.Child == nil {
		return renderedFragment{}, fmt.Errorf("label transform fragment is missing child metadata")
	}
	spec := fragment.LabelTransform
	childSQL, childParams, err := renderFragmentSubquery(cfg, spec.Child, params, "label_child")
	if err != nil {
		return renderedFragment{}, err
	}
	mutatedTagsExpr, err := buildLabelTransformTagsExpr(spec, "label_child.tags")
	if err != nil {
		return renderedFragment{}, err
	}
	return renderLabelTransformFromSource(childSQL, childParams, mutatedTagsExpr, params)
}

// renderLabelTransformFromSource wraps a pre-rendered child source in the
// label-transform outer SELECTs. Shared by the Fragment path
// (renderLabelTransformFragment) and the direct path (lowerLabelTransform) so
// SQL stays byte-identical.
func renderLabelTransformFromSource(childSQL string, childParams map[string]string, mutatedTagsExpr string, params RenderParams) (renderedFragment, error) {
	switch params.Mode {
	case native.RenderModeInstant:
		rowsSQL := "SELECT " + mutatedTagsExpr + " AS tags, label_child.timestamp AS timestamp, label_child.value AS value FROM (" + childSQL + ") AS label_child"
		sql := "SELECT tags, timestamp, any(value) AS value FROM (" + rowsSQL + ") AS label_rows GROUP BY tags, timestamp HAVING throwIf(count() > 1, 'vector cannot contain metrics with the same labelset') = 0 ORDER BY tags ASC, timestamp ASC"
		return renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: childParams}, nil
	case native.RenderModeRange:
		rowsSQL := "SELECT " + mutatedTagsExpr + " AS tags, label_child.time_series AS time_series FROM (" + childSQL + ") AS label_child"
		groupedSQL := "SELECT tags, arraySort(item -> item.1, arrayFlatten(groupArray(time_series))) AS time_series FROM (" + rowsSQL + ") AS label_rows GROUP BY tags"
		dupeExpr := "arrayExists((idx, point) -> if(idx = 1, 0, tupleElement(point, 1) = tupleElement(arrayElement(time_series, idx - 1), 1)), arrayEnumerate(time_series), time_series)"
		sql := "SELECT tags, time_series FROM (" + groupedSQL + ") AS label_series WHERE throwIf(" + dupeExpr + ", 'vector cannot contain metrics with the same labelset') = 0 ORDER BY tags ASC"
		return renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: childParams}, nil
	default:
		return renderedFragment{}, fmt.Errorf("unknown render mode %q", params.Mode)
	}
}

func buildLabelTransformTagsExpr(spec *native.LabelTransformFragment, tagsExpr string) (string, error) {
	if spec == nil {
		return "", fmt.Errorf("label transform spec is nil")
	}
	switch spec.Func {
	case "label_replace":
		valueExpr := labelReplaceValueExpr(spec, tagsExpr)
		return setTagExpr(tagsExpr, spec.Dst, valueExpr), nil
	case "label_join":
		valueExpr := labelJoinValueExpr(spec, tagsExpr)
		return setTagExpr(tagsExpr, spec.Dst, valueExpr), nil
	default:
		return "", fmt.Errorf("unsupported label transform %q", spec.Func)
	}
}

// buildLabelTransformTagsExprFromLogical computes the mutated tags SQL
// expression for a LabelReplacePlan or LabelJoinPlan. Mirrors
// buildLabelTransformTagsExpr but reads fields from the logical node
// directly so the direct-render path does not need a *LabelTransformFragment.
// The returned string is byte-identical to buildLabelTransformTagsExpr given
// equivalent inputs — this is locked by TestBuildLabelTransformTagsExprLogicalMatchesFragment.
func buildLabelTransformTagsExprFromLogical(n logicalpkg.Node, tagsExpr string) (string, error) {
	switch p := n.(type) {
	case *logicalpkg.LabelReplacePlan:
		cfg := p.Config
		valueExpr := labelReplaceValueExprFromLogical(cfg, tagsExpr)
		return setTagExpr(tagsExpr, cfg.Dst, valueExpr), nil
	case *logicalpkg.LabelJoinPlan:
		cfg := p.Config
		valueExpr := labelJoinValueExprFromLogical(cfg, tagsExpr)
		return setTagExpr(tagsExpr, cfg.Dst, valueExpr), nil
	default:
		return "", fmt.Errorf("label transform: unsupported logical node type %T", n)
	}
}

func labelReplaceValueExpr(spec *native.LabelTransformFragment, tagsExpr string) string {
	srcExpr := labelValueExpr(tagsExpr, spec.Src)
	regex := sqlStringLiteral(spec.Regex)
	repl := sqlStringLiteral(goReplacementToClickHouse(spec.Repl, spec.RegexSubexpNames))
	return "if(match(" + srcExpr + ", " + regex + "), replaceRegexpOne(" + srcExpr + ", " + regex + ", " + repl + "), " + labelValueExpr(tagsExpr, spec.Dst) + ")"
}

func labelReplaceValueExprFromLogical(cfg modelpkg.LabelReplaceConfig, tagsExpr string) string {
	srcExpr := labelValueExpr(tagsExpr, cfg.Src)
	regex := sqlStringLiteral(cfg.Regex.String())
	repl := sqlStringLiteral(goReplacementToClickHouse(cfg.Repl, cfg.Regex.SubexpNames()))
	return "if(match(" + srcExpr + ", " + regex + "), replaceRegexpOne(" + srcExpr + ", " + regex + ", " + repl + "), " + labelValueExpr(tagsExpr, cfg.Dst) + ")"
}

func labelJoinValueExpr(spec *native.LabelTransformFragment, tagsExpr string) string {
	if len(spec.SrcLabels) == 0 {
		return "''"
	}
	parts := make([]string, 0, len(spec.SrcLabels))
	for _, label := range spec.SrcLabels {
		parts = append(parts, labelValueExpr(tagsExpr, label))
	}
	return "arrayStringConcat([" + strings.Join(parts, ", ") + "], " + sqlStringLiteral(spec.Separator) + ")"
}

func labelJoinValueExprFromLogical(cfg modelpkg.LabelJoinConfig, tagsExpr string) string {
	if len(cfg.SrcLabels) == 0 {
		return "''"
	}
	parts := make([]string, 0, len(cfg.SrcLabels))
	for _, label := range cfg.SrcLabels {
		parts = append(parts, labelValueExpr(tagsExpr, label))
	}
	return "arrayStringConcat([" + strings.Join(parts, ", ") + "], " + sqlStringLiteral(cfg.Separator) + ")"
}

func labelValueExpr(tagsExpr, label string) string {
	quoted := sqlStringLiteral(label)
	return "tupleElement(arrayFirst(tag -> tag.1 = " + quoted + ", arrayPushBack(" + tagsExpr + ", tuple(" + quoted + ", ''))), 2)"
}

func setTagExpr(tagsExpr, label, valueExpr string) string {
	quoted := sqlStringLiteral(label)
	filtered := "arrayFilter(tag -> tag.1 != " + quoted + ", " + tagsExpr + ")"
	return "if(" + valueExpr + " = '', " + filtered + ", arraySort(tag -> tag.1, arrayPushBack(" + filtered + ", tuple(" + quoted + ", " + valueExpr + "))))"
}

func goReplacementToClickHouse(repl string, subexpNames []string) string {
	var out strings.Builder
	for i := 0; i < len(repl); {
		if repl[i] != '$' {
			out.WriteByte(repl[i])
			i++
			continue
		}
		if i+1 < len(repl) && repl[i+1] == '$' {
			out.WriteByte('$')
			i += 2
			continue
		}
		name, next, ok := parseGoReplacementName(repl, i)
		if !ok {
			out.WriteByte('$')
			i++
			continue
		}
		if idx := captureIndexForReplacementName(name, subexpNames); idx >= 0 {
			out.WriteByte('\\')
			out.WriteString(strconv.Itoa(idx))
		}
		i = next
	}
	return out.String()
}

func parseGoReplacementName(repl string, start int) (string, int, bool) {
	if start >= len(repl) || repl[start] != '$' {
		return "", start, false
	}
	if start+1 >= len(repl) {
		return "", start, false
	}
	if repl[start+1] == '{' {
		end := start + 2
		for end < len(repl) && repl[end] != '}' {
			end++
		}
		if end >= len(repl) || end == start+2 {
			return "", start, false
		}
		return repl[start+2 : end], end + 1, true
	}
	end := start + 1
	for end < len(repl) && isGoReplacementNameChar(repl[end]) {
		end++
	}
	if end == start+1 {
		return "", start, false
	}
	return repl[start+1 : end], end, true
}

func isGoReplacementNameChar(ch byte) bool {
	return ch == '_' || (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z')
}

func captureIndexForReplacementName(name string, subexpNames []string) int {
	if name == "" {
		return -1
	}
	allDigits := true
	for i := 0; i < len(name); i++ {
		if name[i] < '0' || name[i] > '9' {
			allDigits = false
			break
		}
	}
	if allDigits {
		idx, err := strconv.Atoi(name)
		if err != nil {
			return -1
		}
		if idx >= 0 && idx < len(subexpNames) {
			return idx
		}
		return -1
	}
	for idx, subexpName := range subexpNames {
		if subexpName == name {
			return idx
		}
	}
	return -1
}
