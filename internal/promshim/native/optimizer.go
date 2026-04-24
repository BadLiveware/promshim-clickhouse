package native

import (
	"fmt"
	"strings"

	logicalpkg "github.com/BadLiveware/promshim-ch/internal/promshim/logical"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

type OptimizerLayer string

const (
	OptimizerLayerLogical  OptimizerLayer = "logical"
	OptimizerLayerFragment OptimizerLayer = "fragment"
	OptimizerLayerFinalSQL OptimizerLayer = "final_sql"
)

type OptimizerPass string

const (
	PassTrivialExpressionNormalization      OptimizerPass = "trivial_expression_normalization"
	PassEvaluationRangePropagation          OptimizerPass = "evaluation_range_propagation"
	PassCommonMatcherInference              OptimizerPass = "common_matcher_inference"
	PassLabelPredicatePushdown              OptimizerPass = "label_predicate_pushdown"
	PassProjectionPushdown                  OptimizerPass = "projection_pushdown"
	PassFunctionPatternRewrites             OptimizerPass = "function_pattern_rewrites"
	PassJoinNormalizationDuplicateDetection OptimizerPass = "join_normalization_and_duplicate_detection"
	PassFlattenRedundantWrappers            OptimizerPass = "flatten_redundant_wrappers"
	PassFinalSQLShapingLateMaterialization  OptimizerPass = "final_sql_shaping_and_late_materialization"
)

var FixedPassOrder = []OptimizerPass{
	PassTrivialExpressionNormalization,
	PassEvaluationRangePropagation,
	PassCommonMatcherInference,
	PassLabelPredicatePushdown,
	PassProjectionPushdown,
	PassFunctionPatternRewrites,
	PassJoinNormalizationDuplicateDetection,
	PassFlattenRedundantWrappers,
	PassFinalSQLShapingLateMaterialization,
}

type OptimizationContext struct {
	Mode             RenderMode
	EvaluationTimeMS int64
	StartMS          int64
	EndMS            int64
	StepMS           int64
}

type OptimizationReport struct {
	RulesApplied         []string
	PushedPredicates     []string
	InferredPredicates   []string
	RequiredColumns      []string
	MaterializedColumns  []string
	SemanticBarriers     []string
	RenderedSQL          string
	RequiredInputStartMS int64
	RequiredInputEndMS   int64
	FunctionCatalog      []string
	AppliedRewrites      []string
	JoinNormalization    string
}

type OptimizedFragment struct {
	Fragment *NativeFragment
	Report   *OptimizationReport
}

type fragmentMutationKind string

const (
	mutationNone           fragmentMutationKind = "none"
	mutationSelectorFields fragmentMutationKind = "selector_fields"
	mutationStructural     fragmentMutationKind = "structural"
)

type optimizerPassSpec struct {
	ID      OptimizerPass
	Layer   OptimizerLayer
	Mutates fragmentMutationKind
	Apply   func(*optimizerState) error
}

var optimizerPasses = []optimizerPassSpec{
	{ID: PassTrivialExpressionNormalization, Layer: OptimizerLayerLogical, Mutates: mutationStructural, Apply: applyTrivialExpressionNormalization},
	{ID: PassEvaluationRangePropagation, Layer: OptimizerLayerLogical, Mutates: mutationNone, Apply: applyEvaluationRangePropagation},
	{ID: PassCommonMatcherInference, Layer: OptimizerLayerLogical, Mutates: mutationSelectorFields, Apply: applyCommonMatcherInference},
	{ID: PassLabelPredicatePushdown, Layer: OptimizerLayerFragment, Mutates: mutationSelectorFields, Apply: applyLabelPredicatePushdown},
	{ID: PassProjectionPushdown, Layer: OptimizerLayerFragment, Mutates: mutationSelectorFields, Apply: applyProjectionPushdown},
	{ID: PassFunctionPatternRewrites, Layer: OptimizerLayerFragment, Mutates: mutationNone, Apply: applyFunctionPatternRewrites},
	{ID: PassJoinNormalizationDuplicateDetection, Layer: OptimizerLayerFragment, Mutates: mutationNone, Apply: applyJoinNormalizationDuplicateDetection},
	{ID: PassFlattenRedundantWrappers, Layer: OptimizerLayerFragment, Mutates: mutationStructural, Apply: applyFlattenRedundantWrappers},
	{ID: PassFinalSQLShapingLateMaterialization, Layer: OptimizerLayerFinalSQL, Mutates: mutationNone, Apply: applyFinalSQLShapingLateMaterialization},
}

var functionRewriteCatalog = []string{
	"last_over_time",
	"sum_over_time",
	"avg_over_time",
	"min_over_time",
	"max_over_time",
	"count_over_time",
	"rate",
	"irate",
	"increase",
	"sum(rate(...))",
	"sum by(...) (rate(...))",
}

func BuildOptimizedFragment(node logicalpkg.Node, analysis *Analysis) (*OptimizedFragment, error) {
	return BuildOptimizedFragmentWithContext(node, analysis, OptimizationContext{})
}

func BuildOptimizedFragmentWithContext(node logicalpkg.Node, analysis *Analysis, ctx OptimizationContext) (*OptimizedFragment, error) {
	if node == nil {
		return nil, fmt.Errorf("native optimized fragment build requires a logical plan node")
	}
	if analysis == nil {
		analysis = Analyze(node)
	}
	info := analysis.InfoFor(node)
	if info == nil {
		return nil, fmt.Errorf("native optimized fragment build could not find lowering info for %T", node)
	}
	fragment, err := BuildFragment(node, analysis)
	if err != nil {
		return nil, err
	}
	return OptimizeFragment(fragment, info, ctx)
}

// OptimizeFromInfo wraps OptimizeFragment so tier-3 construction callers
// can drive optimization from a LoweringInfo without dereferencing
// info.Fragment themselves. Returns an error if info or info.Fragment is
// nil; callers should gate on info.SubtreeShape before invoking.
func OptimizeFromInfo(info *LoweringInfo, ctx OptimizationContext) (*OptimizedFragment, error) {
	if info == nil {
		return nil, fmt.Errorf("native optimizer requires lowering info")
	}
	return OptimizeFragment(info.Fragment, info, ctx)
}

func OptimizeFragment(fragment *NativeFragment, info *LoweringInfo, ctx OptimizationContext) (*OptimizedFragment, error) {
	if fragment == nil {
		return nil, fmt.Errorf("native optimizer requires a fragment")
	}
	state := &optimizerState{
		fragment: CloneFragment(fragment),
		report: &OptimizationReport{
			FunctionCatalog: append([]string(nil), functionRewriteCatalog...),
		},
		info:     info,
		ctx:      ctx,
		interner: newMatcherInterner(),
	}
	internMatchersInFragment(state.fragment, state.interner)
	for _, pass := range optimizerPasses {
		state.report.RulesApplied = append(state.report.RulesApplied, string(pass.ID))
		if err := pass.Apply(state); err != nil {
			return nil, fmt.Errorf("applying optimizer pass %s: %w", pass.ID, err)
		}
	}
	state.report.SemanticBarriers = mergeUniqueStrings(state.report.SemanticBarriers, semanticBarriersForFragment(state.fragment)...)
	return &OptimizedFragment{Fragment: state.fragment, Report: state.report}, nil
}

func ApplyRenderedSQLMetadata(report *OptimizationReport, mode RenderMode, sql string) error {
	if report == nil {
		return nil
	}
	if containsSelectStar(sql) {
		return fmt.Errorf("final SQL shaping forbids SELECT * in native fragments")
	}
	report.RenderedSQL = sql
	report.MaterializedColumns = mergeUniqueStrings(report.MaterializedColumns, renderedColumnsForMode(mode)...)
	return nil
}

type optimizerState struct {
	fragment *NativeFragment
	report   *OptimizationReport
	info     *LoweringInfo
	ctx      OptimizationContext
	interner *matcherInterner
}

type matcherKey struct {
	typ   labels.MatchType
	name  string
	value string
}

// matcherInterner deduplicates equal matcher structs within a single optimizer
// run so repeated selector/inferred/pushed matcher slices can share pointers.
type matcherInterner struct {
	byKey map[matcherKey]*labels.Matcher
}

func newMatcherInterner() *matcherInterner {
	return &matcherInterner{byKey: map[matcherKey]*labels.Matcher{}}
}

func matcherIdentityKey(matcher *labels.Matcher) matcherKey {
	if matcher == nil {
		return matcherKey{}
	}
	return matcherKey{typ: matcher.Type, name: matcher.Name, value: matcher.Value}
}

func (m *matcherInterner) intern(matcher *labels.Matcher) *labels.Matcher {
	if m == nil || matcher == nil {
		return matcher
	}
	key := matcherIdentityKey(matcher)
	if existing, ok := m.byKey[key]; ok {
		return existing
	}
	m.byKey[key] = matcher
	return matcher
}

func (m *matcherInterner) internSlice(matchers []*labels.Matcher) []*labels.Matcher {
	if len(matchers) == 0 {
		return nil
	}
	interned := matchers[:0]
	for _, matcher := range matchers {
		if matcher == nil {
			continue
		}
		interned = append(interned, m.intern(matcher))
	}
	if len(interned) == 0 {
		return nil
	}
	return interned
}

func applyTrivialExpressionNormalization(state *optimizerState) error {
	normalizeTrivialSourceExpressionsInPlace(state.fragment)
	return nil
}

func applyEvaluationRangePropagation(state *optimizerState) error {
	startMS, endMS, ok := requiredInputBounds(state.fragment, state.info, state.ctx)
	if !ok {
		return nil
	}
	state.report.RequiredInputStartMS = startMS
	state.report.RequiredInputEndMS = endMS
	state.report.SemanticBarriers = mergeUniqueStrings(state.report.SemanticBarriers, semanticBarriersFromTimeRequirements(state.info)...)
	return nil
}

func applyCommonMatcherInference(state *optimizerState) error {
	selector := BaseSelectorSource(state.fragment)
	if selector == nil {
		return nil
	}
	selector.InferredMatchers = state.interner.internSlice(inferSourceMatchers(selector))
	state.report.InferredPredicates = mergeUniqueStrings(state.report.InferredPredicates, matcherStrings(selector.InferredMatchers)...)
	return nil
}

func applyLabelPredicatePushdown(state *optimizerState) error {
	selector := BaseSelectorSource(state.fragment)
	if selector == nil {
		return nil
	}
	selector.PushedMatchers = mergeMatchers(state.interner, selector.Matchers, selector.InferredMatchers)
	state.report.PushedPredicates = mergeUniqueStrings(state.report.PushedPredicates, matcherStrings(selector.PushedMatchers)...)
	return nil
}

func applyProjectionPushdown(state *optimizerState) error {
	applySelectorProjection(state.fragment)
	state.report.RequiredColumns = mergeUniqueStrings(state.report.RequiredColumns, requiredColumnsForFragment(state.fragment)...)
	return nil
}

func applyFunctionPatternRewrites(state *optimizerState) error {
	// The current native subset has no typed function fragments yet, but the pass and
	// catalog are explicit so later function-native work plugs into a defined stage.
	if state.fragment.Kind == FragmentKindAggregation && state.fragment.Aggregation != nil {
		return nil
	}
	return nil
}

func applyJoinNormalizationDuplicateDetection(state *optimizerState) error {
	state.report.JoinNormalization = joinNormalizationForFragment(state.fragment)
	if state.report.JoinNormalization == "not_applicable" {
		return nil
	}
	state.report.SemanticBarriers = mergeUniqueStrings(state.report.SemanticBarriers, "join_key_normalization_boundary")
	return nil
}

func applyFlattenRedundantWrappers(state *optimizerState) error {
	flattenRedundantWrappersInPlace(state.fragment)
	return nil
}

func applyFinalSQLShapingLateMaterialization(state *optimizerState) error {
	state.report.MaterializedColumns = mergeUniqueStrings(state.report.MaterializedColumns, baseMaterializedColumnsForFragment(state.fragment)...)
	if hasTagsColumn(state.report.RequiredColumns) {
		state.report.SemanticBarriers = mergeUniqueStrings(state.report.SemanticBarriers, "late_tag_materialization")
	}
	return nil
}

func applyIdentityUnaryWrapperRewrite(fragment *NativeFragment) {
	if fragment == nil {
		return
	}
	if fragment.Kind == FragmentKindUnarySourceExpr && fragment.ValueExpr == "{value}" && fragment.TagsExpr == "{tags}" && !fragment.DropsMetric {
		fragment.Kind = FragmentKindLeafSource
	}
}

func walkChildFragments(fragment *NativeFragment, visit func(*NativeFragment)) {
	if fragment == nil || visit == nil {
		return
	}
	if fragment.Aggregation != nil {
		visit(fragment.Aggregation.Source)
	}
	if fragment.Absent != nil {
		visit(fragment.Absent.Child)
	}
	if fragment.HistogramProjection != nil {
		visit(fragment.HistogramProjection.Child)
	}
	if fragment.HistogramFunction != nil {
		visit(fragment.HistogramFunction.Child)
		for _, quantile := range fragment.HistogramFunction.Quantiles {
			visit(quantile)
		}
	}
	if fragment.SortTransform != nil {
		visit(fragment.SortTransform.Child)
	}
	if fragment.LabelTransform != nil {
		visit(fragment.LabelTransform.Child)
	}
	if fragment.ClampTransform != nil {
		visit(fragment.ClampTransform.Child)
		visit(fragment.ClampTransform.Min)
		visit(fragment.ClampTransform.Max)
	}
	if fragment.ValueTransform != nil {
		visit(fragment.ValueTransform.Child)
	}
}

func normalizeTrivialSourceExpressionsInPlace(fragment *NativeFragment) {
	if fragment == nil {
		return
	}
	walkChildFragments(fragment, normalizeTrivialSourceExpressionsInPlace)
	applyIdentityUnaryWrapperRewrite(fragment)
}

func flattenRedundantWrappersInPlace(fragment *NativeFragment) {
	if fragment == nil {
		return
	}
	walkChildFragments(fragment, flattenRedundantWrappersInPlace)
	applyIdentityUnaryWrapperRewrite(fragment)
}

func RequiredInputBounds(fragment *NativeFragment, info *LoweringInfo, ctx OptimizationContext) (int64, int64, bool) {
	return requiredInputBounds(fragment, info, ctx)
}

func ResolvedAnchorTimeMS(fragment *NativeFragment, ctx OptimizationContext) (int64, bool) {
	return resolvedFragmentAnchorTimeMS(fragment, BaseSelectorSource(fragment), ctx)
}

func requiredInputBounds(fragment *NativeFragment, info *LoweringInfo, ctx OptimizationContext) (int64, int64, bool) {
	if info == nil && fragment == nil {
		return 0, 0, false
	}
	lookbackMS := int64(0)
	offsetMS := int64(0)
	selector := BaseSelectorSource(fragment)
	if fragment != nil && fragment.Selector != nil {
		lookbackMS = fragment.Selector.Lookback.Milliseconds()
		offsetMS = fragment.Selector.Offset.Milliseconds()
	} else if info != nil {
		lookbackMS = info.TimeRequirements.Lookback.Milliseconds()
		offsetMS = info.TimeRequirements.Offset.Milliseconds()
	} else if selector != nil {
		lookbackMS = selector.Lookback.Milliseconds()
		offsetMS = selector.Offset.Milliseconds()
	}
	switch ctx.Mode {
	case RenderModeInstant:
		anchorMS := ctx.EvaluationTimeMS
		if resolved, ok := resolvedFragmentAnchorTimeMS(fragment, selector, ctx); ok {
			anchorMS = resolved
		}
		endMS := anchorMS - offsetMS
		startMS := endMS - lookbackMS
		return startMS, endMS, true
	case RenderModeRange:
		if anchorMS, ok := resolvedFragmentAnchorTimeMS(fragment, selector, ctx); ok {
			endMS := anchorMS - offsetMS
			startMS := endMS - lookbackMS
			return startMS, endMS, true
		}
		endMS := ctx.EndMS - offsetMS
		startMS := ctx.StartMS - offsetMS - lookbackMS
		return startMS, endMS, true
	default:
		if ctx.EvaluationTimeMS == 0 && ctx.StartMS == 0 && ctx.EndMS == 0 {
			return 0, 0, false
		}
		endMS := ctx.EvaluationTimeMS - offsetMS
		startMS := endMS - lookbackMS
		return startMS, endMS, true
	}
}

func resolvedFragmentAnchorTimeMS(fragment *NativeFragment, selector *SelectorSource, ctx OptimizationContext) (int64, bool) {
	if fragment == nil {
		return resolvedSelectorAnchorTimeMS(selector, ctx)
	}
	if fragment.Subquery != nil {
		if fragment.Subquery.Timestamp != nil {
			return *fragment.Subquery.Timestamp, true
		}
		switch fragment.Subquery.StartOrEnd {
		case parser.START:
			if ctx.Mode == RenderModeRange {
				return ctx.StartMS, true
			}
			return ctx.EvaluationTimeMS, true
		case parser.END:
			if ctx.Mode == RenderModeRange {
				return ctx.EndMS, true
			}
			return ctx.EvaluationTimeMS, true
		}
		if resolved, ok := resolvedFragmentAnchorTimeMS(fragment.Subquery.Child, nil, ctx); ok {
			return resolved, true
		}
	}
	if fragment.Aggregation != nil {
		if resolved, ok := resolvedFragmentAnchorTimeMS(fragment.Aggregation.Source, nil, ctx); ok {
			return resolved, true
		}
	}
	if fragment.RangeFunction != nil {
		if resolved, ok := resolvedFragmentAnchorTimeMS(fragment.RangeFunction.Child, nil, ctx); ok {
			return resolved, true
		}
	}
	if fragment.BinaryJoin != nil {
		if resolved, ok := resolvedFragmentAnchorTimeMS(fragment.BinaryJoin.LHS, nil, ctx); ok {
			return resolved, true
		}
		if resolved, ok := resolvedFragmentAnchorTimeMS(fragment.BinaryJoin.RHS, nil, ctx); ok {
			return resolved, true
		}
	}
	if fragment.ScalarConvert != nil {
		if resolved, ok := resolvedFragmentAnchorTimeMS(fragment.ScalarConvert.Child, nil, ctx); ok {
			return resolved, true
		}
	}
	if fragment.InfoJoin != nil {
		if resolved, ok := resolvedFragmentAnchorTimeMS(fragment.InfoJoin.Child, nil, ctx); ok {
			return resolved, true
		}
	}
	if fragment.Absent != nil {
		if resolved, ok := resolvedFragmentAnchorTimeMS(fragment.Absent.Child, nil, ctx); ok {
			return resolved, true
		}
	}
	if fragment.HistogramProjection != nil {
		if resolved, ok := resolvedFragmentAnchorTimeMS(fragment.HistogramProjection.Child, nil, ctx); ok {
			return resolved, true
		}
	}
	if fragment.HistogramFunction != nil {
		if resolved, ok := resolvedFragmentAnchorTimeMS(fragment.HistogramFunction.Child, nil, ctx); ok {
			return resolved, true
		}
		for _, quantile := range fragment.HistogramFunction.Quantiles {
			if resolved, ok := resolvedFragmentAnchorTimeMS(quantile, nil, ctx); ok {
				return resolved, true
			}
		}
	}
	if fragment.SortTransform != nil {
		if resolved, ok := resolvedFragmentAnchorTimeMS(fragment.SortTransform.Child, nil, ctx); ok {
			return resolved, true
		}
	}
	if fragment.LabelTransform != nil {
		if resolved, ok := resolvedFragmentAnchorTimeMS(fragment.LabelTransform.Child, nil, ctx); ok {
			return resolved, true
		}
	}
	if fragment.ClampTransform != nil {
		if resolved, ok := resolvedFragmentAnchorTimeMS(fragment.ClampTransform.Child, nil, ctx); ok {
			return resolved, true
		}
		if resolved, ok := resolvedFragmentAnchorTimeMS(fragment.ClampTransform.Min, nil, ctx); ok {
			return resolved, true
		}
		if resolved, ok := resolvedFragmentAnchorTimeMS(fragment.ClampTransform.Max, nil, ctx); ok {
			return resolved, true
		}
	}
	if fragment.ValueTransform != nil {
		if resolved, ok := resolvedFragmentAnchorTimeMS(fragment.ValueTransform.Child, nil, ctx); ok {
			return resolved, true
		}
	}
	return resolvedSelectorAnchorTimeMS(selector, ctx)
}

func resolvedSelectorAnchorTimeMS(selector *SelectorSource, ctx OptimizationContext) (int64, bool) {
	if selector == nil {
		return 0, false
	}
	if selector.Timestamp != nil {
		return *selector.Timestamp, true
	}
	switch selector.StartOrEnd {
	case parser.START:
		if ctx.Mode == RenderModeRange {
			return ctx.StartMS, true
		}
		return ctx.EvaluationTimeMS, true
	case parser.END:
		if ctx.Mode == RenderModeRange {
			return ctx.EndMS, true
		}
		return ctx.EvaluationTimeMS, true
	default:
		return 0, false
	}
}

func inferSourceMatchers(selector *SelectorSource) []*labels.Matcher {
	if selector == nil || selector.MetricName == "" {
		return nil
	}
	for _, matcher := range selector.Matchers {
		if matcher != nil && matcher.Type == labels.MatchEqual && matcher.Name == labels.MetricName && matcher.Value == selector.MetricName {
			return []*labels.Matcher{matcher}
		}
	}
	matcher, err := labels.NewMatcher(labels.MatchEqual, labels.MetricName, selector.MetricName)
	if err != nil {
		return nil
	}
	return []*labels.Matcher{matcher}
}

func internMatchersInFragment(fragment *NativeFragment, interner *matcherInterner) {
	if fragment == nil || interner == nil {
		return
	}
	if fragment.Selector != nil {
		fragment.Selector.Matchers = interner.internSlice(fragment.Selector.Matchers)
		fragment.Selector.InferredMatchers = interner.internSlice(fragment.Selector.InferredMatchers)
		fragment.Selector.PushedMatchers = interner.internSlice(fragment.Selector.PushedMatchers)
	}
	if fragment.Aggregation != nil {
		internMatchersInFragment(fragment.Aggregation.Source, interner)
	}
	if fragment.RangeFunction != nil {
		internMatchersInFragment(fragment.RangeFunction.Child, interner)
	}
	if fragment.Subquery != nil {
		internMatchersInFragment(fragment.Subquery.Child, interner)
	}
	if fragment.BinaryJoin != nil {
		internMatchersInFragment(fragment.BinaryJoin.LHS, interner)
		internMatchersInFragment(fragment.BinaryJoin.RHS, interner)
	}
	if fragment.ScalarConvert != nil {
		internMatchersInFragment(fragment.ScalarConvert.Child, interner)
	}
	if fragment.InfoJoin != nil {
		fragment.InfoJoin.SelectorMatchers = interner.internSlice(fragment.InfoJoin.SelectorMatchers)
		internMatchersInFragment(fragment.InfoJoin.Child, interner)
	}
	if fragment.Absent != nil {
		internMatchersInFragment(fragment.Absent.Child, interner)
	}
	if fragment.HistogramProjection != nil {
		internMatchersInFragment(fragment.HistogramProjection.Child, interner)
	}
	if fragment.HistogramFunction != nil {
		internMatchersInFragment(fragment.HistogramFunction.Child, interner)
		for _, quantile := range fragment.HistogramFunction.Quantiles {
			internMatchersInFragment(quantile, interner)
		}
	}
	if fragment.SortTransform != nil {
		internMatchersInFragment(fragment.SortTransform.Child, interner)
	}
	if fragment.LabelTransform != nil {
		internMatchersInFragment(fragment.LabelTransform.Child, interner)
	}
	if fragment.ClampTransform != nil {
		internMatchersInFragment(fragment.ClampTransform.Child, interner)
		internMatchersInFragment(fragment.ClampTransform.Min, interner)
		internMatchersInFragment(fragment.ClampTransform.Max, interner)
	}
	if fragment.ValueTransform != nil {
		internMatchersInFragment(fragment.ValueTransform.Child, interner)
	}
}

func matcherStrings(matchers []*labels.Matcher) []string {
	predicates := make([]string, 0, len(matchers))
	for _, matcher := range matchers {
		if matcher == nil {
			continue
		}
		predicates = append(predicates, matcher.String())
	}
	return predicates
}

func applySelectorProjection(fragment *NativeFragment) {
	if fragment == nil {
		return
	}
	if fragment.Aggregation != nil {
		applySelectorProjection(fragment.Aggregation.Source)
		if containsAggregationBoundary(fragment.Aggregation.Source) {
			return
		}
		selector := BaseSelectorSource(fragment.Aggregation.Source)
		if selector != nil {
			switch {
			case fragment.Aggregation.Without || containsLabelTransform(fragment.Aggregation.Source):
				selector.RequireFullTags = true
				selector.RequiredTagLabels = nil
			case len(fragment.Aggregation.Grouping) == 0:
				selector.RequireFullTags = false
				selector.RequiredTagLabels = nil
			default:
				selector.RequireFullTags = false
				selector.RequiredTagLabels = uniqueSortedStrings(fragment.Aggregation.Grouping)
			}
		}
		return
	}
	if fragment.Selector != nil {
		fragment.Selector.RequireFullTags = true
		fragment.Selector.RequiredTagLabels = nil
	}
}

func containsAggregationBoundary(fragment *NativeFragment) bool {
	if fragment == nil {
		return false
	}
	if fragment.Aggregation != nil {
		return true
	}
	if fragment.RangeFunction != nil {
		return containsAggregationBoundary(fragment.RangeFunction.Child)
	}
	if fragment.Subquery != nil {
		return containsAggregationBoundary(fragment.Subquery.Child)
	}
	if fragment.ScalarConvert != nil {
		return containsAggregationBoundary(fragment.ScalarConvert.Child)
	}
	if fragment.InfoJoin != nil {
		return containsAggregationBoundary(fragment.InfoJoin.Child)
	}
	if fragment.Absent != nil {
		return containsAggregationBoundary(fragment.Absent.Child)
	}
	if fragment.HistogramProjection != nil {
		return containsAggregationBoundary(fragment.HistogramProjection.Child)
	}
	if fragment.HistogramFunction != nil {
		if containsAggregationBoundary(fragment.HistogramFunction.Child) {
			return true
		}
		for _, quantile := range fragment.HistogramFunction.Quantiles {
			if containsAggregationBoundary(quantile) {
				return true
			}
		}
	}
	if fragment.SortTransform != nil {
		return containsAggregationBoundary(fragment.SortTransform.Child)
	}
	if fragment.LabelTransform != nil {
		return containsAggregationBoundary(fragment.LabelTransform.Child)
	}
	if fragment.ClampTransform != nil {
		return containsAggregationBoundary(fragment.ClampTransform.Child) || containsAggregationBoundary(fragment.ClampTransform.Min) || containsAggregationBoundary(fragment.ClampTransform.Max)
	}
	if fragment.ValueTransform != nil {
		return containsAggregationBoundary(fragment.ValueTransform.Child)
	}
	if fragment.BinaryJoin != nil {
		return containsAggregationBoundary(fragment.BinaryJoin.LHS) || containsAggregationBoundary(fragment.BinaryJoin.RHS)
	}
	return false
}

func containsLabelTransform(fragment *NativeFragment) bool {
	if fragment == nil {
		return false
	}
	if fragment.LabelTransform != nil {
		return true
	}
	if fragment.Aggregation != nil {
		return containsLabelTransform(fragment.Aggregation.Source)
	}
	if fragment.RangeFunction != nil {
		return containsLabelTransform(fragment.RangeFunction.Child)
	}
	if fragment.Subquery != nil {
		return containsLabelTransform(fragment.Subquery.Child)
	}
	if fragment.ScalarConvert != nil {
		return containsLabelTransform(fragment.ScalarConvert.Child)
	}
	if fragment.InfoJoin != nil {
		return containsLabelTransform(fragment.InfoJoin.Child)
	}
	if fragment.Absent != nil {
		return containsLabelTransform(fragment.Absent.Child)
	}
	if fragment.HistogramProjection != nil {
		return containsLabelTransform(fragment.HistogramProjection.Child)
	}
	if fragment.HistogramFunction != nil {
		if containsLabelTransform(fragment.HistogramFunction.Child) {
			return true
		}
		for _, quantile := range fragment.HistogramFunction.Quantiles {
			if containsLabelTransform(quantile) {
				return true
			}
		}
	}
	if fragment.SortTransform != nil {
		return containsLabelTransform(fragment.SortTransform.Child)
	}
	if fragment.ClampTransform != nil {
		return containsLabelTransform(fragment.ClampTransform.Child) || containsLabelTransform(fragment.ClampTransform.Min) || containsLabelTransform(fragment.ClampTransform.Max)
	}
	if fragment.ValueTransform != nil {
		return containsLabelTransform(fragment.ValueTransform.Child)
	}
	if fragment.BinaryJoin != nil {
		return containsLabelTransform(fragment.BinaryJoin.LHS) || containsLabelTransform(fragment.BinaryJoin.RHS)
	}
	return false
}

func BaseSelectorSource(fragment *NativeFragment) *SelectorSource {
	if fragment == nil {
		return nil
	}
	if fragment.Aggregation != nil {
		return BaseSelectorSource(fragment.Aggregation.Source)
	}
	if fragment.RangeFunction != nil {
		return BaseSelectorSource(fragment.RangeFunction.Child)
	}
	if fragment.Subquery != nil {
		return BaseSelectorSource(fragment.Subquery.Child)
	}
	if fragment.ScalarConvert != nil {
		return BaseSelectorSource(fragment.ScalarConvert.Child)
	}
	if fragment.InfoJoin != nil {
		return BaseSelectorSource(fragment.InfoJoin.Child)
	}
	if fragment.Absent != nil {
		return BaseSelectorSource(fragment.Absent.Child)
	}
	if fragment.HistogramProjection != nil {
		return BaseSelectorSource(fragment.HistogramProjection.Child)
	}
	if fragment.HistogramFunction != nil {
		if selector := BaseSelectorSource(fragment.HistogramFunction.Child); selector != nil {
			return selector
		}
		for _, quantile := range fragment.HistogramFunction.Quantiles {
			if selector := BaseSelectorSource(quantile); selector != nil {
				return selector
			}
		}
		return nil
	}
	if fragment.SortTransform != nil {
		return BaseSelectorSource(fragment.SortTransform.Child)
	}
	if fragment.LabelTransform != nil {
		return BaseSelectorSource(fragment.LabelTransform.Child)
	}
	if fragment.ClampTransform != nil {
		if selector := BaseSelectorSource(fragment.ClampTransform.Child); selector != nil {
			return selector
		}
		if selector := BaseSelectorSource(fragment.ClampTransform.Min); selector != nil {
			return selector
		}
		return BaseSelectorSource(fragment.ClampTransform.Max)
	}
	if fragment.ValueTransform != nil {
		return BaseSelectorSource(fragment.ValueTransform.Child)
	}
	if fragment.Selector != nil {
		return fragment.Selector
	}
	return nil
}

func requiredColumnsForFragment(fragment *NativeFragment) []string {
	if fragment == nil {
		return nil
	}
	columns := []string{"value"}
	if fragment.OutputKind == OutputKindRangeMatrix {
		columns = []string{"time_series"}
	}
	if fragmentRequiresTags(fragment) {
		columns = append(columns, "tags")
	}
	if fragment.Aggregation != nil {
		columns = append(columns, requiredColumnsForFragment(fragment.Aggregation.Source)...)
	}
	if fragment.RangeFunction != nil {
		columns = append(columns, requiredColumnsForFragment(fragment.RangeFunction.Child)...)
	}
	if fragment.Subquery != nil {
		columns = append(columns, requiredColumnsForFragment(fragment.Subquery.Child)...)
	}
	if fragment.ScalarConvert != nil {
		columns = append(columns, requiredColumnsForFragment(fragment.ScalarConvert.Child)...)
	}
	if fragment.InfoJoin != nil {
		columns = append(columns, requiredColumnsForFragment(fragment.InfoJoin.Child)...)
	}
	if fragment.Absent != nil {
		columns = append(columns, requiredColumnsForFragment(fragment.Absent.Child)...)
	}
	if fragment.HistogramProjection != nil {
		columns = append(columns, requiredColumnsForFragment(fragment.HistogramProjection.Child)...)
	}
	if fragment.HistogramFunction != nil {
		columns = append(columns, requiredColumnsForFragment(fragment.HistogramFunction.Child)...)
		for _, quantile := range fragment.HistogramFunction.Quantiles {
			columns = append(columns, requiredColumnsForFragment(quantile)...)
		}
	}
	if fragment.SortTransform != nil {
		columns = append(columns, requiredColumnsForFragment(fragment.SortTransform.Child)...)
	}
	if fragment.LabelTransform != nil {
		columns = append(columns, requiredColumnsForFragment(fragment.LabelTransform.Child)...)
	}
	if fragment.ClampTransform != nil {
		columns = append(columns, requiredColumnsForFragment(fragment.ClampTransform.Child)...)
		columns = append(columns, requiredColumnsForFragment(fragment.ClampTransform.Min)...)
		columns = append(columns, requiredColumnsForFragment(fragment.ClampTransform.Max)...)
	}
	if fragment.ValueTransform != nil {
		columns = append(columns, requiredColumnsForFragment(fragment.ValueTransform.Child)...)
	}
	return mergeUniqueStrings(nil, columns...)
}

func fragmentRequiresTags(fragment *NativeFragment) bool {
	if fragment == nil {
		return false
	}
	if fragment.Aggregation != nil || fragment.RangeFunction != nil || fragment.Subquery != nil || fragment.HistogramProjection != nil || fragment.HistogramFunction != nil || fragment.SortTransform != nil || fragment.LabelTransform != nil || fragment.ClampTransform != nil {
		return true
	}
	if fragment.ScalarConvert != nil {
		return false
	}
	if fragment.InfoJoin != nil {
		return true
	}
	switch fragment.OutputKind {
	case OutputKindInstantVector, OutputKindRangeMatrix:
		return true
	default:
		return false
	}
}

func baseMaterializedColumnsForFragment(fragment *NativeFragment) []string {
	if fragment == nil {
		return nil
	}
	if fragment.OutputKind == OutputKindRangeMatrix {
		return []string{"time_series"}
	}
	return []string{"value"}
}

func renderedColumnsForMode(mode RenderMode) []string {
	switch mode {
	case RenderModeInstant:
		return []string{"tags", "timestamp", "value"}
	case RenderModeRange:
		return []string{"tags", "time_series", "value"}
	default:
		return nil
	}
}

func semanticBarriersFromTimeRequirements(info *LoweringInfo) []string {
	if info == nil {
		return nil
	}
	barriers := []string{}
	if info.TimeRequirements.Lookback > 0 || info.TimeRequirements.Offset > 0 {
		barriers = append(barriers, "evaluation_range")
	}
	if info.TimeRequirements.NeedsSubqueryStepGrid {
		barriers = append(barriers, "subquery_step_grid")
	}
	return barriers
}

func semanticBarriersForFragment(fragment *NativeFragment) []string {
	if fragment == nil {
		return nil
	}
	barriers := []string{}
	if fragment.Kind == FragmentKindAggregation {
		barriers = append(barriers, "aggregation_boundary")
	}
	if fragment.Kind == FragmentKindSubquery {
		barriers = append(barriers, "subquery_step_grid")
	}
	if fragment.Kind == FragmentKindRangeFunction {
		barriers = append(barriers, "range_window_materialization_boundary")
	}
	if fragment.Kind == FragmentKindAbsent && fragment.Absent != nil && fragment.Absent.Func == "absent_over_time" {
		barriers = append(barriers, "range_window_materialization_boundary")
	}
	if fragment.Kind == FragmentKindHistogramProjection || fragment.Kind == FragmentKindHistogramFunction {
		barriers = append(barriers, "histogram_bucket_materialization_boundary")
	}
	if fragment.DropsMetric || (fragment.Aggregation != nil && fragment.Aggregation.Source != nil && fragment.Aggregation.Source.DropsMetric) {
		barriers = append(barriers, "metric_name_lineage_change")
	}
	return barriers
}

func joinNormalizationForFragment(fragment *NativeFragment) string {
	if fragment == nil {
		return "not_applicable"
	}
	switch fragment.Kind {
	case FragmentKindAggregation, FragmentKindLeafSource, FragmentKindUnarySourceExpr, FragmentKindBinaryScalarSourceExpr, FragmentKindSyntheticSeries, FragmentKindScalarConvert, FragmentKindInfoJoin, FragmentKindAbsent, FragmentKindHistogramProjection, FragmentKindHistogramFunction, FragmentKindSortTransform, FragmentKindLabelTransform, FragmentKindClampTransform, FragmentKindValueTransform:
		return "not_applicable"
	default:
		return "required"
	}
}

func mergeMatchers(interner *matcherInterner, groups ...[]*labels.Matcher) []*labels.Matcher {
	merged := make([]*labels.Matcher, 0)
	seen := map[matcherKey]struct{}{}
	for _, group := range groups {
		for _, matcher := range group {
			if matcher == nil {
				continue
			}
			matcher = interner.intern(matcher)
			key := matcherIdentityKey(matcher)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			merged = append(merged, matcher)
		}
	}
	return merged
}

func uniqueSortedStrings(values []string) []string {
	return mergeUniqueStrings(nil, values...)
}

func hasTagsColumn(columns []string) bool {
	for _, column := range columns {
		if column == "tags" {
			return true
		}
	}
	return false
}

func mergeUniqueStrings(base []string, values ...string) []string {
	seen := map[string]struct{}{}
	merged := make([]string, 0, len(base)+len(values))
	for _, value := range base {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		merged = append(merged, value)
	}
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		merged = append(merged, value)
	}
	return merged
}

func containsSelectStar(sql string) bool {
	normalized := strings.ToUpper(sql)
	return strings.Contains(normalized, "SELECT *")
}
