package renderer

import (
	"strings"
)

// renderSortTransformInstantFromSource wraps a pre-rendered child instant-vector
// SQL in the SORT outer SELECT. Used by the direct path (lowerSortTransform).
func renderSortTransformInstantFromSource(childSQL string, childParams map[string]string, funcName string, labels []string) (renderedFragment, error) {
	orderBy := buildSortOrderBy(funcName, labels, "sort_child")
	sql := "SELECT tags, timestamp, value FROM (" + childSQL + ") AS sort_child ORDER BY " + orderBy
	return renderedFragment{RawSQL: trimRenderedQuerySQL(sql), ExtraParams: childParams}, nil
}

func buildSortOrderBy(name string, labels []string, alias string) string {
	tagExpr := alias + ".tags"
	timestampExpr := alias + ".timestamp"
	valueExpr := alias + ".value"
	switch name {
	case "sort":
		return "isNaN(" + valueExpr + ") ASC, " + valueExpr + " ASC, " + tagExpr + " ASC, " + timestampExpr + " ASC"
	case "sort_desc":
		return "isNaN(" + valueExpr + ") ASC, " + valueExpr + " DESC, " + tagExpr + " DESC, " + timestampExpr + " DESC"
	case "sort_by_label":
		parts := make([]string, 0, len(labels)+2)
		for _, label := range labels {
			labelExpr := sortLabelValueExpr(tagExpr, label)
			parts = append(parts, "naturalSortKey("+labelExpr+") ASC")
		}
		parts = append(parts, tagExpr+" ASC", timestampExpr+" ASC")
		return strings.Join(parts, ", ")
	case "sort_by_label_desc":
		parts := make([]string, 0, len(labels)+2)
		for _, label := range labels {
			labelExpr := sortLabelValueExpr(tagExpr, label)
			parts = append(parts, "naturalSortKey("+labelExpr+") DESC")
		}
		parts = append(parts, tagExpr+" DESC", timestampExpr+" DESC")
		return strings.Join(parts, ", ")
	default:
		return tagExpr + " ASC, " + timestampExpr + " ASC"
	}
}

func sortLabelValueExpr(tagsExpr, label string) string {
	quoted := sortSQLStringLiteral(label)
	return "tupleElement(arrayFirst(tag -> tag.1 = " + quoted + ", arrayPushBack(" + tagsExpr + ", tuple(" + quoted + ", ''))), 2)"
}

func sortSQLStringLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
