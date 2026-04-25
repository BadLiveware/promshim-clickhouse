package logical

import (
	"time"

	promlabels "github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

// DefaultInstantSelectorLookback mirrors the value used by
// native.selector; duplicated here to avoid the logical → native import
// edge. Keep the two in sync; a short-term approach until the native
// lookback constant moves to a lower layer.
const DefaultInstantSelectorLookback = 5 * time.Minute

// Analysis is the bottom-up walk result: the root the walk started
// from, and a per-node side-map. Consumers that know a node by
// reference can look its NodeInfo up directly; there is no tree
// traversal API on Analysis.
type Analysis struct {
	Root Node
	Info map[Node]*NodeInfo
}

// Analyze walks `root` bottom-up and fills in a NodeInfo for each
// visited node.
func Analyze(root Node) *Analysis {
	a := &Analysis{Root: root, Info: map[Node]*NodeInfo{}}
	a.walk(root)
	a.FinalizeMetadata()
	return a
}

// InfoFor returns the NodeInfo recorded for `node`, or nil if the
// node was never visited.
func (a *Analysis) InfoFor(node Node) *NodeInfo {
	if a == nil {
		return nil
	}
	return a.Info[node]
}

func (a *Analysis) walk(node Node) *NodeInfo {
	if node == nil {
		return nil
	}
	if existing, ok := a.Info[node]; ok {
		return existing
	}
	info := &NodeInfo{
		Schema:       NewSchema(),
		LabelLineage: leafLineage(),
	}
	a.Info[node] = info

	switch n := node.(type) {
	case *LeafExprPlan:
		fillLeaf(info, n)
	case *ScalarLiteralPlan:
		info.TimeDomain = DomainScalar
		info.DropsMetric = true
	case *UnaryPlan:
		child := a.walk(n.Child)
		fillUnary(info, child)
	case *BinaryPlan:
		lhs := a.walk(n.LHS)
		rhs := a.walk(n.RHS)
		fillBinary(info, n, lhs, rhs)
	case *AggregationPlan:
		child := a.walk(n.Child)
		fillAggregation(info, n, child)
	case *HistogramQuantilePlan:
		child := a.walk(n.Child)
		fillHistogramOutput(info, child)
	case *HistogramFractionPlan:
		child := a.walk(n.Child)
		fillHistogramOutput(info, child)
	case *HistogramProjectionPlan:
		child := a.walk(n.Child)
		fillHistogramOutput(info, child)
	case *HistogramQuantilesPlan:
		child := a.walk(n.Child)
		fillHistogramOutput(info, child)
		if n.Label != "" {
			info.Schema.AddPossible(n.Label)
			info.LabelLineage.Known[n.Label] = LineageStateString(LabelLineageReplaced)
		}
		for _, paramChild := range n.ParamChildren {
			paramInfo := a.walk(paramChild)
			if paramInfo != nil {
				info.TimeRequirements = combineTimeRequirements(info.TimeRequirements, paramInfo.TimeRequirements)
			}
		}
	case *RangeFunctionPlan:
		child := a.walk(n.Child)
		fillRangeFunction(info, child, rangeFunctionPreservesMetricName(n.Func))
	case *VectorPlan:
		child := a.walk(n.Child)
		fillVector(info, child)
	case *RoundPlan:
		child := a.walk(n.Child)
		fillPassthroughDrop(info, child)
	case *SortPlan:
		child := a.walk(n.Child)
		fillPassthrough(info, child)
	case *ScalarConvertPlan:
		child := a.walk(n.Child)
		fillScalarConvert(info, child)
	case *InfoPlan:
		child := a.walk(n.Child)
		fillPassthrough(info, child)
	case *PointwiseFunctionPlan:
		fillPointwise(info, n, a)
	case *ScalarBuiltinPlan:
		info.TimeDomain = DomainScalar
		info.DropsMetric = true
		info.LabelLineage = syntheticLineage()
	case *RatePlan:
		child := a.walk(n.Child)
		fillRangeFunction(info, child, false)
	case *IncreasePlan:
		child := a.walk(n.Child)
		fillRangeFunction(info, child, false)
	case *DeltaPlan:
		child := a.walk(n.Child)
		fillRangeFunction(info, child, false)
	case *ChangesPlan:
		child := a.walk(n.Child)
		fillRangeFunction(info, child, false)
	case *DerivPlan:
		child := a.walk(n.Child)
		fillRangeFunction(info, child, false)
	case *QuantileOverTimePlan:
		child := a.walk(n.Child)
		fillRangeFunction(info, child, false)
	case *AbsentPlan:
		child := a.walk(n.Child)
		fillAbsent(info, n.OutputMetric, child)
	case *AbsentOverTimePlan:
		child := a.walk(n.Child)
		fillAbsent(info, n.OutputMetric, child)
	case *SubqueryPlan:
		child := a.walk(n.Child)
		fillSubquery(info, n.Range, n.Offset, child)
	case *LabelReplacePlan:
		child := a.walk(n.Child)
		fillPassthrough(info, child)
		if n.Config.Dst != "" {
			info.Schema.AddPossible(n.Config.Dst)
			info.LabelLineage.Known[n.Config.Dst] = LineageStateString(LabelLineageReplaced)
		}
	case *LabelJoinPlan:
		child := a.walk(n.Child)
		fillPassthrough(info, child)
		if n.Config.Dst != "" {
			info.Schema.AddPossible(n.Config.Dst)
			info.LabelLineage.Known[n.Config.Dst] = LineageStateString(LabelLineageReplaced)
		}
	default:
		_ = n
		info.TimeDomain = DomainUnknown
		info.LabelLineage = unknownLineage()
	}
	return info
}

// fillLeaf populates a leaf node's NodeInfo from its underlying PromQL
// selector expression.
func fillLeaf(info *NodeInfo, n *LeafExprPlan) {
	info.TimeRequirements = leafTimeRequirements(n.Expr)
	info.LabelLineage = leafLineage()
	switch expr := n.Expr.(type) {
	case *parser.VectorSelector:
		info.TimeDomain = DomainInstant
		schemaFromSelector(&info.Schema, expr)
	case *parser.MatrixSelector:
		info.TimeDomain = DomainRange
		if v, ok := expr.VectorSelector.(*parser.VectorSelector); ok {
			schemaFromSelector(&info.Schema, v)
		}
	case *parser.NumberLiteral, *parser.StringLiteral:
		info.TimeDomain = DomainScalar
		info.DropsMetric = true
	default:
		// Unknown shapes default to DomainUnknown; richer analysis
		// is not required here.
		info.TimeDomain = DomainUnknown
	}
}

func fillUnary(info *NodeInfo, child *NodeInfo) {
	if child == nil {
		info.TimeDomain = DomainUnknown
		return
	}
	info.TimeDomain = child.TimeDomain
	info.LabelLineage = passthroughLineage(child.LabelLineage)
	info.TimeRequirements = combineTimeRequirements(child.TimeRequirements)
	info.Schema = cloneSchema(child.Schema)
	info.DropsMetric = child.DropsMetric
}

func fillBinary(info *NodeInfo, n *BinaryPlan, lhs, rhs *NodeInfo) {
	info.LabelLineage = unknownLineage()
	info.TimeRequirements = combineTimeRequirements(
		timeRequirementsOrZero(lhs),
		timeRequirementsOrZero(rhs),
	)
	info.TimeDomain = binaryDomain(lhs, rhs)
	// Binary ops collapse the schema conservatively — label identity
	// depends on vector matching rules that are not tracked here.
	// Propagate a union of Possible labels from whichever sides have
	// vector output. Guaranteed is only kept when both sides agree
	// (or the other side is a scalar).
	lhsVec := lhs != nil && isVectorDomain(lhs.TimeDomain)
	rhsVec := rhs != nil && isVectorDomain(rhs.TimeDomain)
	switch {
	case lhsVec && rhsVec:
		// Schema is tricky for vector-vector joins. Keep Possible as
		// the union; Guaranteed stays empty — the match side owns
		// label semantics and we do not model it here.
		for label := range lhs.Schema.Possible {
			info.Schema.AddPossible(label)
		}
		for label := range rhs.Schema.Possible {
			info.Schema.AddPossible(label)
		}
	case lhsVec:
		info.Schema = cloneSchema(lhs.Schema)
	case rhsVec:
		info.Schema = cloneSchema(rhs.Schema)
	}
	// Comparison and arithmetic both drop __name__ unless one side is
	// scalar passthrough; conservative default is to drop it.
	if !n.ReturnBool && isArithmeticOp(n.Op) {
		info.DropsMetric = true
		info.LabelLineage.MetricName = LabelLineageDropped
		info.LabelLineage.Known[promlabels.MetricName] = LineageStateString(LabelLineageDropped)
	}
}

func fillAggregation(info *NodeInfo, n *AggregationPlan, child *NodeInfo) {
	info.GroupingKey = GroupingKey{
		Labels:  append([]string(nil), n.Grouping...),
		Without: n.Without,
	}
	info.DropsMetric = true
	info.TimeRequirements = combineTimeRequirements(timeRequirementsOrZero(child))
	info.TimeDomain = DomainInstant
	if child != nil {
		info.TimeDomain = child.TimeDomain
	}
	// Schema: by(...) keeps grouping labels only; without(...) keeps
	// everything except grouping labels. Guaranteed vs Possible is
	// inherited from the child's schema per label.
	if n.Without {
		dropped := map[string]struct{}{}
		for _, label := range n.Grouping {
			dropped[label] = struct{}{}
		}
		if child != nil {
			for label := range child.Schema.Possible {
				if _, drop := dropped[label]; drop {
					continue
				}
				if _, guaranteed := child.Schema.Guaranteed[label]; guaranteed {
					info.Schema.AddGuaranteed(label)
				} else {
					info.Schema.AddPossible(label)
				}
			}
		}
	} else {
		for _, label := range n.Grouping {
			if child == nil {
				info.Schema.AddPossible(label)
				continue
			}
			if _, guaranteed := child.Schema.Guaranteed[label]; guaranteed {
				info.Schema.AddGuaranteed(label)
			} else {
				info.Schema.AddPossible(label)
			}
		}
	}
	// count_values synthesizes the destination label from sample
	// values; it is guaranteed to appear on output.
	if n.Op == parser.COUNT_VALUES && n.ParamString != "" {
		info.Schema.AddGuaranteed(n.ParamString)
	}
	info.LabelLineage = aggregationLineage(childLineage(child), n.Grouping, n.Without)
	if n.Op == parser.COUNT_VALUES && n.ParamString != "" {
		info.LabelLineage.Known[n.ParamString] = LineageStateString(LabelLineageReplaced)
	}
}

func fillHistogramOutput(info *NodeInfo, child *NodeInfo) {
	info.TimeDomain = DomainInstant
	info.DropsMetric = true
	info.TimeRequirements = combineTimeRequirements(timeRequirementsOrZero(child))
	info.LabelLineage = syntheticLineage()
	if child != nil {
		for label, state := range child.LabelLineage.Known {
			if label == promlabels.MetricName || label == "le" {
				continue
			}
			info.LabelLineage.Known[label] = state
		}
		info.LabelLineage.Wildcard = child.LabelLineage.Wildcard
		for label := range child.Schema.Possible {
			if label == "le" || label == promlabels.MetricName {
				continue
			}
			if _, guaranteed := child.Schema.Guaranteed[label]; guaranteed {
				info.Schema.AddGuaranteed(label)
			} else {
				info.Schema.AddPossible(label)
			}
		}
	}
	info.LabelLineage.MetricName = LabelLineageDropped
	info.LabelLineage.Known[promlabels.MetricName] = LineageStateString(LabelLineageDropped)
}

func fillRangeFunction(info *NodeInfo, child *NodeInfo, preservesName bool) {
	info.TimeDomain = DomainInstant
	info.TimeRequirements = combineTimeRequirements(timeRequirementsOrZero(child))
	if child != nil {
		info.Schema = cloneSchema(child.Schema)
		info.LabelLineage = passthroughLineage(child.LabelLineage)
	}
	if !preservesName {
		info.DropsMetric = true
		info.LabelLineage.MetricName = LabelLineageDropped
		info.LabelLineage.Known[promlabels.MetricName] = LineageStateString(LabelLineageDropped)
	}
}

func fillVector(info *NodeInfo, child *NodeInfo) {
	info.TimeDomain = DomainInstant
	info.DropsMetric = true
	info.LabelLineage = syntheticLineage()
	if child != nil {
		info.TimeRequirements = combineTimeRequirements(child.TimeRequirements)
	}
}

func fillPassthrough(info *NodeInfo, child *NodeInfo) {
	if child == nil {
		return
	}
	info.TimeDomain = child.TimeDomain
	info.Schema = cloneSchema(child.Schema)
	info.LabelLineage = passthroughLineage(child.LabelLineage)
	info.TimeRequirements = combineTimeRequirements(child.TimeRequirements)
	info.DropsMetric = child.DropsMetric
}

func fillPassthroughDrop(info *NodeInfo, child *NodeInfo) {
	fillPassthrough(info, child)
	info.DropsMetric = true
	info.LabelLineage.MetricName = LabelLineageDropped
	info.LabelLineage.Known[promlabels.MetricName] = LineageStateString(LabelLineageDropped)
}

func fillScalarConvert(info *NodeInfo, child *NodeInfo) {
	info.TimeDomain = DomainScalar
	info.DropsMetric = true
	info.LabelLineage = syntheticLineage()
	if child != nil {
		info.TimeRequirements = combineTimeRequirements(child.TimeRequirements)
	}
}

func fillPointwise(info *NodeInfo, n *PointwiseFunctionPlan, a *Analysis) {
	info.DropsMetric = true
	info.LabelLineage = syntheticLineage()
	if n.Child == nil {
		// Synthetic date/time functions with no vector child (e.g. time()).
		info.TimeDomain = DomainInstant
		for _, paramChild := range n.ParamChildren {
			if paramChild == nil {
				continue
			}
			paramInfo := a.walk(paramChild)
			if paramInfo != nil {
				info.TimeRequirements = combineTimeRequirements(info.TimeRequirements, paramInfo.TimeRequirements)
			}
		}
		return
	}
	child := a.walk(n.Child)
	if child != nil {
		info.TimeDomain = child.TimeDomain
		info.Schema = cloneSchema(child.Schema)
		info.LabelLineage = passthroughLineage(child.LabelLineage)
		info.TimeRequirements = combineTimeRequirements(child.TimeRequirements)
	}
	info.DropsMetric = true
	info.LabelLineage.MetricName = LabelLineageDropped
	info.LabelLineage.Known[promlabels.MetricName] = LineageStateString(LabelLineageDropped)
	for _, paramChild := range n.ParamChildren {
		if paramChild == nil {
			continue
		}
		paramInfo := a.walk(paramChild)
		if paramInfo != nil {
			info.TimeRequirements = combineTimeRequirements(info.TimeRequirements, paramInfo.TimeRequirements)
		}
	}
}

func fillAbsent(info *NodeInfo, outputMetric map[string]string, child *NodeInfo) {
	info.TimeDomain = DomainInstant
	info.DropsMetric = true
	info.LabelLineage = syntheticOutputLineage(outputMetric)
	if child != nil {
		info.TimeRequirements = combineTimeRequirements(child.TimeRequirements)
	}
	for label := range outputMetric {
		info.Schema.AddGuaranteed(label)
	}
}

func fillSubquery(info *NodeInfo, rangeDur, offset time.Duration, child *NodeInfo) {
	info.TimeDomain = DomainRange
	if child != nil {
		info.Schema = cloneSchema(child.Schema)
		info.LabelLineage = passthroughLineage(child.LabelLineage)
		info.TimeRequirements = subqueryTimeRequirements(child.TimeRequirements, rangeDur, offset)
		info.DropsMetric = child.DropsMetric
		return
	}
	info.TimeRequirements = subqueryTimeRequirements(TimeRequirements{}, rangeDur, offset)
}

// schemaFromSelector fills `schema` with label guarantees derived from
// a vector selector's matchers: equality matchers produce guaranteed
// labels, non-equality and regex matchers produce possible labels.
func schemaFromSelector(schema *Schema, selector *parser.VectorSelector) {
	if selector == nil {
		return
	}
	for _, matcher := range selector.LabelMatchers {
		if matcher == nil || matcher.Name == "" {
			continue
		}
		switch matcher.Type {
		case promlabels.MatchEqual:
			// Empty-string equality means "label absent" — do not
			// add to either schema set.
			if matcher.Value == "" {
				continue
			}
			schema.AddGuaranteed(matcher.Name)
		case promlabels.MatchRegexp, promlabels.MatchNotEqual, promlabels.MatchNotRegexp:
			schema.AddPossible(matcher.Name)
		}
	}
}

// binaryDomain combines two child domains: scalar+scalar stays scalar;
// scalar+vector becomes vector; otherwise the wider of the two domains
// wins (Range > Instant > Scalar > Unknown) so a binary over a range
// selector keeps the range shape.
func binaryDomain(lhs, rhs *NodeInfo) TimeDomain {
	if lhs == nil && rhs == nil {
		return DomainUnknown
	}
	if lhs == nil {
		return rhs.TimeDomain
	}
	if rhs == nil {
		return lhs.TimeDomain
	}
	if lhs.TimeDomain == DomainScalar && rhs.TimeDomain == DomainScalar {
		return DomainScalar
	}
	if lhs.TimeDomain == DomainScalar {
		return rhs.TimeDomain
	}
	if rhs.TimeDomain == DomainScalar {
		return lhs.TimeDomain
	}
	if lhs.TimeDomain == DomainRange || rhs.TimeDomain == DomainRange {
		return DomainRange
	}
	return DomainInstant
}

func isVectorDomain(d TimeDomain) bool {
	return d == DomainInstant || d == DomainRange
}

func isArithmeticOp(op parser.ItemType) bool {
	switch op {
	case parser.ADD, parser.SUB, parser.MUL, parser.DIV, parser.MOD, parser.POW:
		return true
	}
	return false
}

// cloneSchema returns a deep copy so propagation never shares the
// underlying maps.
func cloneSchema(src Schema) Schema {
	out := NewSchema()
	for label := range src.Guaranteed {
		out.AddGuaranteed(label)
	}
	for label := range src.Possible {
		out.AddPossible(label)
	}
	return out
}

// timeRequirementsOrZero returns the child's requirements or an empty
// struct if the child is nil.
func timeRequirementsOrZero(child *NodeInfo) TimeRequirements {
	if child == nil {
		return TimeRequirements{}
	}
	return child.TimeRequirements
}

// childLineage returns a safe clone of a child lineage, or the leaf
// default when the child is nil.
func childLineage(child *NodeInfo) LabelLineage {
	if child == nil {
		return leafLineage()
	}
	return cloneLineage(child.LabelLineage)
}

// rangeFunctionPreservesMetricName mirrors the native
// RangeFunctionPreservesMetricName mapping. The list is duplicated
// intentionally — the native-package equivalent lives behind a
// larger API surface and would drag fragment code into logical.
func rangeFunctionPreservesMetricName(name string) bool {
	switch name {
	case "avg_over_time",
		"min_over_time",
		"max_over_time",
		"sum_over_time",
		"last_over_time",
		"mad_over_time",
		"median_over_time",
		"stddev_over_time",
		"stdvar_over_time",
		"count_over_time",
		"quantile_over_time",
		"ts_of_min_over_time",
		"ts_of_max_over_time",
		"ts_of_first_over_time",
		"ts_of_last_over_time",
		"first_over_time":
		return true
	}
	return false
}
