package native

import (
	"fmt"

	planpkg "github.com/BadLiveware/promshim-ch/internal/promshim/plan"
)

func BuildFragment(node planpkg.LogicalPlan, analysis *Analysis) (*NativeFragment, error) {
	if node == nil {
		return nil, fmt.Errorf("native fragment build requires a logical plan node")
	}
	if analysis == nil {
		analysis = Analyze(node)
	}
	info := analysis.InfoFor(node)
	if info == nil {
		return nil, fmt.Errorf("native fragment build could not find lowering info for %T", node)
	}
	if info.Fragment == nil {
		return nil, fmt.Errorf("logical node %T is not lowerable to a native fragment", node)
	}
	return cloneFragment(info.Fragment), nil
}

func cloneFragment(fragment *NativeFragment) *NativeFragment {
	if fragment == nil {
		return nil
	}
	cloned := &NativeFragment{
		Kind:         fragment.Kind,
		OutputKind:   fragment.OutputKind,
		SourcePromQL: fragment.SourcePromQL,
		Selector:     cloneSelectorSource(fragment.Selector),
		ValueExpr:    fragment.ValueExpr,
		TagsExpr:     fragment.TagsExpr,
		DropsMetric:  fragment.DropsMetric,
	}
	if fragment.BinaryJoin != nil {
		cloned.BinaryJoin = &BinaryJoinFragment{
			Op:             fragment.BinaryJoin.Op,
			ReturnBool:     fragment.BinaryJoin.ReturnBool,
			VectorMatching: cloneVectorMatching(fragment.BinaryJoin.VectorMatching),
			JoinShape:      fragment.BinaryJoin.JoinShape,
			LHS:            cloneFragment(fragment.BinaryJoin.LHS),
			RHS:            cloneFragment(fragment.BinaryJoin.RHS),
		}
	}
	if fragment.RangeFunction != nil {
		cloned.RangeFunction = &RangeFunctionFragment{Func: fragment.RangeFunction.Func, ParamNumber: cloneFloat64Pointer(fragment.RangeFunction.ParamNumber), ParamNumbers: cloneFloat64Pointers(fragment.RangeFunction.ParamNumbers), Child: cloneFragment(fragment.RangeFunction.Child)}
	}
	if fragment.Subquery != nil {
		cloned.Subquery = &SubqueryFragment{Range: fragment.Subquery.Range, Step: fragment.Subquery.Step, Offset: fragment.Subquery.Offset, Timestamp: cloneInt64Pointer(fragment.Subquery.Timestamp), StartOrEnd: fragment.Subquery.StartOrEnd, Child: cloneFragment(fragment.Subquery.Child)}
	}
	if fragment.Aggregation != nil {
		cloned.Aggregation = &AggregationFragment{
			Op:          fragment.Aggregation.Op,
			Grouping:    append([]string(nil), fragment.Aggregation.Grouping...),
			Without:     fragment.Aggregation.Without,
			ParamNumber: cloneFloat64Pointer(fragment.Aggregation.ParamNumber),
			Source:      cloneFragment(fragment.Aggregation.Source),
		}
	}
	if fragment.Synthetic != nil {
		cloned.Synthetic = &SyntheticSeriesFragment{Func: fragment.Synthetic.Func}
	}
	if fragment.ScalarConvert != nil {
		cloned.ScalarConvert = &ScalarConvertFragment{Child: cloneFragment(fragment.ScalarConvert.Child)}
	}
	if fragment.InfoJoin != nil {
		cloned.InfoJoin = &InfoJoinFragment{Child: cloneFragment(fragment.InfoJoin.Child), InfoMetricName: fragment.InfoJoin.InfoMetricName, SelectorMatchers: cloneMatchers(fragment.InfoJoin.SelectorMatchers), CopyLabelNames: append([]string(nil), fragment.InfoJoin.CopyLabelNames...), DropUnmatched: fragment.InfoJoin.DropUnmatched}
	}
	return cloned
}

func cloneInt64Pointer(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneFloat64Pointer(value *float64) *float64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneFloat64Pointers(values []*float64) []*float64 {
	if len(values) == 0 {
		return nil
	}
	out := make([]*float64, 0, len(values))
	for _, value := range values {
		out = append(out, cloneFloat64Pointer(value))
	}
	return out
}
