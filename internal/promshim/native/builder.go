package native

import (
	"fmt"

	planpkg "ch-observability/internal/promshim/plan"
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
	if fragment.Aggregation != nil {
		cloned.Aggregation = &AggregationFragment{
			Op:       fragment.Aggregation.Op,
			Grouping: append([]string(nil), fragment.Aggregation.Grouping...),
			Without:  fragment.Aggregation.Without,
			Source:   cloneFragment(fragment.Aggregation.Source),
		}
	}
	return cloned
}
