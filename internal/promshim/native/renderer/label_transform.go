package renderer

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/BadLiveware/promshim-ch/internal/promshim/native"
	"github.com/BadLiveware/promshim-ch/internal/promshim/storage"
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

func labelReplaceValueExpr(spec *native.LabelTransformFragment, tagsExpr string) string {
	srcExpr := labelValueExpr(tagsExpr, spec.Src)
	regex := sqlStringLiteral(spec.Regex)
	repl := sqlStringLiteral(goReplacementToClickHouse(spec.Repl, spec.RegexSubexpNames))
	return "if(match(" + srcExpr + ", " + regex + "), replaceRegexpOne(" + srcExpr + ", " + regex + ", " + repl + "), " + labelValueExpr(tagsExpr, spec.Dst) + ")"
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
		if idx >= 0 {
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
