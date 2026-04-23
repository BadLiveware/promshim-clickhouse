package native

import (
	"fmt"

	logicalpkg "ch-observability/internal/promshim/logical"
)

func BuildFragment(node logicalpkg.Node, analysis *Analysis) (*NativeFragment, error) {
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
	return CloneFragment(info.Fragment), nil
}

func CloneFragment(fragment *NativeFragment) *NativeFragment {
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
			VectorMatching: CloneVectorMatching(fragment.BinaryJoin.VectorMatching),
			JoinShape:      fragment.BinaryJoin.JoinShape,
			LHS:            CloneFragment(fragment.BinaryJoin.LHS),
			RHS:            CloneFragment(fragment.BinaryJoin.RHS),
		}
	}
	if fragment.RangeFunction != nil {
		cloned.RangeFunction = &RangeFunctionFragment{Func: fragment.RangeFunction.Func, ParamNumber: cloneFloat64Pointer(fragment.RangeFunction.ParamNumber), ParamNumbers: cloneFloat64Pointers(fragment.RangeFunction.ParamNumbers), Child: CloneFragment(fragment.RangeFunction.Child)}
	}
	if fragment.Subquery != nil {
		cloned.Subquery = &SubqueryFragment{Range: fragment.Subquery.Range, Step: fragment.Subquery.Step, Offset: fragment.Subquery.Offset, Timestamp: cloneInt64Pointer(fragment.Subquery.Timestamp), StartOrEnd: fragment.Subquery.StartOrEnd, Child: CloneFragment(fragment.Subquery.Child)}
	}
	if fragment.Aggregation != nil {
		cloned.Aggregation = &AggregationFragment{
			Op:              fragment.Aggregation.Op,
			Grouping:        append([]string(nil), fragment.Aggregation.Grouping...),
			Without:         fragment.Aggregation.Without,
			ParamNumber:     cloneFloat64Pointer(fragment.Aggregation.ParamNumber),
			ParamString:     fragment.Aggregation.ParamString,
			Source:          CloneFragment(fragment.Aggregation.Source),
			EmitZeroOnEmpty: fragment.Aggregation.EmitZeroOnEmpty,
		}
	}
	if fragment.Synthetic != nil {
		cloned.Synthetic = &SyntheticSeriesFragment{Func: fragment.Synthetic.Func, Value: cloneFloat64Pointer(fragment.Synthetic.Value)}
	}
	if fragment.ScalarConvert != nil {
		cloned.ScalarConvert = &ScalarConvertFragment{Child: CloneFragment(fragment.ScalarConvert.Child)}
	}
	if fragment.InfoJoin != nil {
		cloned.InfoJoin = &InfoJoinFragment{Child: CloneFragment(fragment.InfoJoin.Child), InfoMetricName: fragment.InfoJoin.InfoMetricName, SelectorMatchers: CloneMatchers(fragment.InfoJoin.SelectorMatchers), CopyLabelNames: append([]string(nil), fragment.InfoJoin.CopyLabelNames...), DropUnmatched: fragment.InfoJoin.DropUnmatched}
	}
	if fragment.Absent != nil {
		cloned.Absent = &AbsentFragment{Func: fragment.Absent.Func, OutputMetric: cloneStringMap(fragment.Absent.OutputMetric), Child: CloneFragment(fragment.Absent.Child)}
	}
	if fragment.HistogramProjection != nil {
		cloned.HistogramProjection = &HistogramProjectionFragment{Func: fragment.HistogramProjection.Func, Child: CloneFragment(fragment.HistogramProjection.Child)}
	}
	if fragment.HistogramFunction != nil {
		quantiles := make([]*NativeFragment, 0, len(fragment.HistogramFunction.Quantiles))
		for _, quantile := range fragment.HistogramFunction.Quantiles {
			quantiles = append(quantiles, CloneFragment(quantile))
		}
		cloned.HistogramFunction = &HistogramFunctionFragment{Func: fragment.HistogramFunction.Func, Label: fragment.HistogramFunction.Label, Quantile: cloneFloat64Pointer(fragment.HistogramFunction.Quantile), Quantiles: quantiles, Lower: cloneFloat64Pointer(fragment.HistogramFunction.Lower), Upper: cloneFloat64Pointer(fragment.HistogramFunction.Upper), Child: CloneFragment(fragment.HistogramFunction.Child)}
	}
	if fragment.SortTransform != nil {
		cloned.SortTransform = &SortTransformFragment{Func: fragment.SortTransform.Func, Labels: append([]string(nil), fragment.SortTransform.Labels...), Child: CloneFragment(fragment.SortTransform.Child)}
	}
	if fragment.LabelTransform != nil {
		cloned.LabelTransform = &LabelTransformFragment{Func: fragment.LabelTransform.Func, Dst: fragment.LabelTransform.Dst, Repl: fragment.LabelTransform.Repl, Regex: fragment.LabelTransform.Regex, RegexSubexpNames: append([]string(nil), fragment.LabelTransform.RegexSubexpNames...), Src: fragment.LabelTransform.Src, Separator: fragment.LabelTransform.Separator, SrcLabels: append([]string(nil), fragment.LabelTransform.SrcLabels...), Child: CloneFragment(fragment.LabelTransform.Child)}
	}
	if fragment.ClampTransform != nil {
		cloned.ClampTransform = &ClampTransformFragment{Func: fragment.ClampTransform.Func, Child: CloneFragment(fragment.ClampTransform.Child), Min: CloneFragment(fragment.ClampTransform.Min), Max: CloneFragment(fragment.ClampTransform.Max)}
	}
	if fragment.ValueTransform != nil {
		cloned.ValueTransform = &ValueTransformFragment{
			Child:            CloneFragment(fragment.ValueTransform.Child),
			ValueExpr:        fragment.ValueTransform.ValueExpr,
			FilterExpr:       fragment.ValueTransform.FilterExpr,
			DropsMetric:      fragment.ValueTransform.DropsMetric,
			RuntimeTransform: cloneRuntimeValueTransform(fragment.ValueTransform.RuntimeTransform),
		}
	}
	return cloned
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
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

func cloneRuntimeValueTransform(transform *RuntimeValueTransform) *RuntimeValueTransform {
	if transform == nil {
		return nil
	}
	return &RuntimeValueTransform{
		Op:           transform.Op,
		Scalar:       cloneFloat64Pointer(transform.Scalar),
		ScalarOnLeft: transform.ScalarOnLeft,
	}
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
