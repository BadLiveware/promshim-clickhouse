package native

import (
	"fmt"
	"strings"

	planpkg "github.com/BadLiveware/promshim-ch/internal/promshim/plan"
	"github.com/prometheus/prometheus/model/labels"
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

type optimizerPassSpec struct {
	ID    OptimizerPass
	Layer OptimizerLayer
	Apply func(*optimizerState) error
}

var optimizerPasses = []optimizerPassSpec{
	{ID: PassTrivialExpressionNormalization, Layer: OptimizerLayerLogical, Apply: applyTrivialExpressionNormalization},
	{ID: PassEvaluationRangePropagation, Layer: OptimizerLayerLogical, Apply: applyEvaluationRangePropagation},
	{ID: PassCommonMatcherInference, Layer: OptimizerLayerLogical, Apply: applyCommonMatcherInference},
	{ID: PassLabelPredicatePushdown, Layer: OptimizerLayerFragment, Apply: applyLabelPredicatePushdown},
	{ID: PassProjectionPushdown, Layer: OptimizerLayerFragment, Apply: applyProjectionPushdown},
	{ID: PassFunctionPatternRewrites, Layer: OptimizerLayerFragment, Apply: applyFunctionPatternRewrites},
	{ID: PassJoinNormalizationDuplicateDetection, Layer: OptimizerLayerFragment, Apply: applyJoinNormalizationDuplicateDetection},
	{ID: PassFlattenRedundantWrappers, Layer: OptimizerLayerFragment, Apply: applyFlattenRedundantWrappers},
	{ID: PassFinalSQLShapingLateMaterialization, Layer: OptimizerLayerFinalSQL, Apply: applyFinalSQLShapingLateMaterialization},
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

func BuildOptimizedFragment(node planpkg.LogicalPlan, analysis *Analysis) (*OptimizedFragment, error) {
	return BuildOptimizedFragmentWithContext(node, analysis, OptimizationContext{})
}

func BuildOptimizedFragmentWithContext(node planpkg.LogicalPlan, analysis *Analysis, ctx OptimizationContext) (*OptimizedFragment, error) {
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

func OptimizeFragment(fragment *NativeFragment, info *LoweringInfo, ctx OptimizationContext) (*OptimizedFragment, error) {
	if fragment == nil {
		return nil, fmt.Errorf("native optimizer requires a fragment")
	}
	state := &optimizerState{
		fragment: cloneFragment(fragment),
		report: &OptimizationReport{
			FunctionCatalog: append([]string(nil), functionRewriteCatalog...),
		},
		info: info,
		ctx:  ctx,
	}
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
}

func applyTrivialExpressionNormalization(state *optimizerState) error {
	state.fragment = normalizeTrivialSourceExpressions(state.fragment)
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
	selector := baseSelectorSource(state.fragment)
	if selector == nil {
		return nil
	}
	selector.InferredMatchers = inferSourceMatchers(selector)
	state.report.InferredPredicates = mergeUniqueStrings(state.report.InferredPredicates, matcherStrings(selector.InferredMatchers)...)
	return nil
}

func applyLabelPredicatePushdown(state *optimizerState) error {
	selector := baseSelectorSource(state.fragment)
	if selector == nil {
		return nil
	}
	selector.PushedMatchers = mergeMatchers(selector.Matchers, selector.InferredMatchers)
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
	state.fragment = flattenRedundantWrappers(state.fragment)
	return nil
}

func applyFinalSQLShapingLateMaterialization(state *optimizerState) error {
	state.report.MaterializedColumns = mergeUniqueStrings(state.report.MaterializedColumns, baseMaterializedColumnsForFragment(state.fragment)...)
	if hasTagsColumn(state.report.RequiredColumns) {
		state.report.SemanticBarriers = mergeUniqueStrings(state.report.SemanticBarriers, "late_tag_materialization")
	}
	return nil
}

func normalizeTrivialSourceExpressions(fragment *NativeFragment) *NativeFragment {
	if fragment == nil {
		return nil
	}
	normalized := cloneFragment(fragment)
	if normalized.Aggregation != nil {
		normalized.Aggregation.Source = normalizeTrivialSourceExpressions(normalized.Aggregation.Source)
	}
	if normalized.Kind == FragmentKindUnarySourceExpr && normalized.ValueExpr == "{value}" && normalized.TagsExpr == "{tags}" && !normalized.DropsMetric {
		normalized.Kind = FragmentKindLeafSource
	}
	return normalized
}

func flattenRedundantWrappers(fragment *NativeFragment) *NativeFragment {
	if fragment == nil {
		return nil
	}
	flattened := cloneFragment(fragment)
	if flattened.Aggregation != nil {
		flattened.Aggregation.Source = flattenRedundantWrappers(flattened.Aggregation.Source)
	}
	if flattened.Kind == FragmentKindUnarySourceExpr && flattened.ValueExpr == "{value}" && flattened.TagsExpr == "{tags}" && !flattened.DropsMetric {
		flattened.Kind = FragmentKindLeafSource
	}
	return flattened
}

func requiredInputBounds(fragment *NativeFragment, info *LoweringInfo, ctx OptimizationContext) (int64, int64, bool) {
	if info == nil && fragment == nil {
		return 0, 0, false
	}
	lookbackMS := int64(0)
	offsetMS := int64(0)
	if info != nil {
		lookbackMS = info.TimeRequirements.Lookback.Milliseconds()
		offsetMS = info.TimeRequirements.Offset.Milliseconds()
	}
	if selector := baseSelectorSource(fragment); selector != nil {
		lookbackMS = selector.Lookback.Milliseconds()
		offsetMS = selector.Offset.Milliseconds()
	}
	switch ctx.Mode {
	case RenderModeInstant:
		endMS := ctx.EvaluationTimeMS - offsetMS
		startMS := endMS - lookbackMS
		return startMS, endMS, true
	case RenderModeRange:
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

func inferSourceMatchers(selector *SelectorSource) []*labels.Matcher {
	if selector == nil || selector.MetricName == "" {
		return nil
	}
	matcher, err := labels.NewMatcher(labels.MatchEqual, labels.MetricName, selector.MetricName)
	if err != nil {
		return nil
	}
	return []*labels.Matcher{matcher}
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
		selector := baseSelectorSource(fragment.Aggregation.Source)
		if selector != nil {
			switch {
			case fragment.Aggregation.Without:
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

func baseSelectorSource(fragment *NativeFragment) *SelectorSource {
	if fragment == nil {
		return nil
	}
	if fragment.Aggregation != nil {
		return baseSelectorSource(fragment.Aggregation.Source)
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
	if fragmentRequiresTags(fragment) {
		columns = append(columns, "tags")
	}
	if fragment.Aggregation != nil {
		columns = append(columns, requiredColumnsForFragment(fragment.Aggregation.Source)...)
	}
	return mergeUniqueStrings(nil, columns...)
}

func fragmentRequiresTags(fragment *NativeFragment) bool {
	if fragment == nil {
		return false
	}
	if fragment.Aggregation != nil {
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
	case FragmentKindAggregation, FragmentKindLeafSource, FragmentKindUnarySourceExpr, FragmentKindBinaryScalarSourceExpr:
		return "not_applicable"
	default:
		return "required"
	}
}

func mergeMatchers(groups ...[]*labels.Matcher) []*labels.Matcher {
	merged := make([]*labels.Matcher, 0)
	seen := map[string]struct{}{}
	for _, group := range groups {
		for _, matcher := range group {
			if matcher == nil {
				continue
			}
			key := matcher.String()
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			merged = append(merged, labels.MustNewMatcher(matcher.Type, matcher.Name, matcher.Value))
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
