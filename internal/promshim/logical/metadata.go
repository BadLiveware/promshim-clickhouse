package logical

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/prometheus/prometheus/promql/parser"
)

// FinalizeMetadata derives deterministic, non-semantic planning metadata from
// the semantic analysis facts. These annotations are hints for candidate
// grouping, explain output, and later renderer/CBE work; absence or uncertainty
// must not change query results.
func (a *Analysis) FinalizeMetadata() {
	if a == nil {
		return
	}
	counts := map[string]int{}
	for node, info := range a.Info {
		if info == nil {
			continue
		}
		populateProjectionMetadata(info)
		populateSelectorMetadata(node, info)
		if info.SelectorFingerprint != "" {
			counts[info.SelectorFingerprint]++
		}
	}
	for _, info := range a.Info {
		if info == nil || info.SelectorFingerprint == "" {
			continue
		}
		if counts[info.SelectorFingerprint] > 1 {
			info.SelectorReuseGroup = "selector:" + shortFingerprint(info.SelectorFingerprint)
			info.SelectorReuseBlockedReason = ""
		} else {
			info.SelectorReuseBlockedReason = "unique_selector"
		}
	}
}

func populateProjectionMetadata(info *NodeInfo) {
	labels := make([]string, 0, len(info.Schema.Possible))
	for label := range info.Schema.Possible {
		if label == "" {
			continue
		}
		labels = append(labels, label)
	}
	sort.Strings(labels)
	info.RequiredLabels = labels
	if info.LabelLineage.Wildcard == LineageStateString(LabelLineageReplaced) || info.LabelLineage.Wildcard == LineageStateString(LabelLineageDropped) {
		info.ProjectionUnsafeReason = "label_lineage_wildcard_changed"
	}
}

func populateSelectorMetadata(node Node, info *NodeInfo) {
	selector, matrixRange, ok := selectorFromNode(node)
	if !ok || selector == nil {
		return
	}
	matchers := normalizedMatchers(selector)
	info.NormalizedMatchers = matchers
	parts := []string{"selector"}
	if selector.Name != "" {
		parts = append(parts, "__name__"+"="+selector.Name)
	}
	if matrixRange > 0 {
		parts = append(parts, fmt.Sprintf("range_ms=%d", matrixRange.Milliseconds()))
	}
	if selector.OriginalOffset != 0 {
		parts = append(parts, fmt.Sprintf("offset_ms=%d", selector.OriginalOffset.Milliseconds()))
	}
	parts = append(parts, matchers...)
	info.SelectorFingerprint = strings.Join(parts, "|")

	required := map[string]struct{}{}
	for _, label := range info.RequiredLabels {
		required[label] = struct{}{}
	}
	if selector.Name != "" {
		required["__name__"] = struct{}{}
	}
	for _, matcher := range selector.LabelMatchers {
		if matcher == nil || matcher.Name == "" {
			continue
		}
		required[matcher.Name] = struct{}{}
	}
	info.RequiredLabels = sortedKeys(required)
}

func selectorFromNode(node Node) (*parser.VectorSelector, time.Duration, bool) {
	leaf, ok := node.(*LeafExprPlan)
	if !ok || leaf == nil {
		return nil, 0, false
	}
	switch expr := leaf.Expr.(type) {
	case *parser.VectorSelector:
		return expr, 0, true
	case *parser.MatrixSelector:
		if vector, ok := expr.VectorSelector.(*parser.VectorSelector); ok {
			return vector, expr.Range, true
		}
	}
	return nil, 0, false
}

func normalizedMatchers(selector *parser.VectorSelector) []string {
	if selector == nil {
		return nil
	}
	matchers := make([]string, 0, len(selector.LabelMatchers))
	for _, matcher := range selector.LabelMatchers {
		if matcher == nil {
			continue
		}
		matchers = append(matchers, fmt.Sprintf("%s:%s:%s", matcher.Name, matcher.Type.String(), matcher.Value))
	}
	sort.Strings(matchers)
	return matchers
}

func sortedKeys(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		if value != "" {
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

func shortFingerprint(value string) string {
	sum := sha1.Sum([]byte(value))
	return hex.EncodeToString(sum[:])[:12]
}
