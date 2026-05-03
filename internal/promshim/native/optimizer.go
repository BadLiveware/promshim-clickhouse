package native

import (
	"fmt"
	"strings"

	logicalpkg "github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/native/physical"
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
	PassEvaluationRangePropagation          OptimizerPass = "evaluation_range_propagation"
	PassCommonMatcherInference              OptimizerPass = "common_matcher_inference"
	PassLabelPredicatePushdown              OptimizerPass = "label_predicate_pushdown"
	PassProjectionPushdown                  OptimizerPass = "projection_pushdown"
	PassJoinNormalizationDuplicateDetection OptimizerPass = "join_normalization_and_duplicate_detection"
	PassFinalSQLShapingLateMaterialization  OptimizerPass = "final_sql_shaping_and_late_materialization"
)

var FixedPassOrder = []OptimizerPass{
	PassEvaluationRangePropagation,
	PassCommonMatcherInference,
	PassLabelPredicatePushdown,
	PassProjectionPushdown,
	PassJoinNormalizationDuplicateDetection,
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
	RulesApplied              []string
	PushedPredicates          []string
	InferredPredicates        []string
	RequiredColumns           []string
	MaterializedColumns       []string
	SemanticBarriers          []string
	PhysicalDecisions         []physical.Decision
	RenderedSQL               string
	RequiredInputStartMS      int64
	RequiredInputEndMS        int64
	FunctionCatalog           []string
	AppliedRewrites           []string
	JoinNormalization         string
	StaticLabelUnionDecisions []StaticLabelUnionDecision
}

type StaticLabelUnionDecision struct {
	Applied           bool   `json:"applied"`
	CandidateBranches int    `json:"candidateBranches"`
	CollapsedRows     int    `json:"collapsedRows"`
	RemainingGroups   int    `json:"remainingGroups"`
	Mode              string `json:"mode,omitempty"`
	SkipReason        string `json:"skipReason,omitempty"`
}

type OptimizedFragment struct {
	Report *OptimizationReport
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
	{ID: PassEvaluationRangePropagation, Layer: OptimizerLayerLogical, Mutates: mutationNone, Apply: applyEvaluationRangePropagation},
	{ID: PassCommonMatcherInference, Layer: OptimizerLayerLogical, Mutates: mutationSelectorFields, Apply: applyCommonMatcherInference},
	{ID: PassLabelPredicatePushdown, Layer: OptimizerLayerFragment, Mutates: mutationSelectorFields, Apply: applyLabelPredicatePushdown},
	{ID: PassProjectionPushdown, Layer: OptimizerLayerLogical, Mutates: mutationNone, Apply: applyProjectionPushdown},
	{ID: PassJoinNormalizationDuplicateDetection, Layer: OptimizerLayerFragment, Mutates: mutationNone, Apply: applyJoinNormalizationDuplicateDetection},
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

// OptimizeFromInfo drives the optimizer pass pipeline from a
// LoweringInfo + logical node. Tier-3 construction callers invoke it
// after Analyze has populated the info side-map. Returns an error if
// info is nil; callers should gate on info.SubtreeShape before
// invoking.
func OptimizeFromInfo(info *LoweringInfo, node logicalpkg.Node, analysis *Analysis, ctx OptimizationContext) (*OptimizedFragment, error) {
	if info == nil {
		return nil, fmt.Errorf("native optimizer requires lowering info")
	}
	if info.SubtreeShape == "" {
		return nil, fmt.Errorf("native optimizer requires a lowered subtree shape")
	}
	state := &optimizerState{
		report: &OptimizationReport{
			FunctionCatalog: append([]string(nil), functionRewriteCatalog...),
		},
		info:     info,
		node:     node,
		analysis: analysis,
		ctx:      ctx,
		interner: newMatcherInterner(),
	}
	for _, pass := range optimizerPasses {
		state.report.RulesApplied = append(state.report.RulesApplied, string(pass.ID))
		if err := pass.Apply(state); err != nil {
			return nil, fmt.Errorf("applying optimizer pass %s: %w", pass.ID, err)
		}
	}
	state.report.SemanticBarriers = mergeUniqueStrings(state.report.SemanticBarriers, semanticBarriersFromLogical(state.node, state.analysis)...)
	return &OptimizedFragment{Report: state.report}, nil
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

func ApplyPhysicalDecisionMetadata(report *OptimizationReport, decisions []physical.Decision) {
	if report == nil || len(decisions) == 0 {
		return
	}
	for _, decision := range decisions {
		if decision.Kind == "" || decision.Strategy == "" {
			continue
		}
		report.PhysicalDecisions = append(report.PhysicalDecisions, decision)
	}
}

type optimizerState struct {
	report *OptimizationReport
	info   *LoweringInfo
	// node + analysis carry the logical-tree context the info-based
	// passes walk via analysis.InfoFor(node). OptimizeFromInfo — the
	// sole production entrypoint — threads both through.
	node     logicalpkg.Node
	analysis *Analysis
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
	interned := make([]*labels.Matcher, 0, len(matchers))
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

func applyEvaluationRangePropagation(state *optimizerState) error {
	startMS, endMS, ok := requiredInputBoundsFromInfo(state.info, state.ctx)
	if !ok {
		return nil
	}
	state.report.RequiredInputStartMS = startMS
	state.report.RequiredInputEndMS = endMS
	state.report.SemanticBarriers = mergeUniqueStrings(state.report.SemanticBarriers, semanticBarriersFromTimeRequirements(state.info)...)
	return nil
}

func applyCommonMatcherInference(state *optimizerState) error {
	selector := baseSelectorFromInfo(state.info)
	if selector == nil {
		return nil
	}
	// Analyze has pre-populated InferredMatchers on the leaf
	// selector. This pass is pure report-emission.
	state.report.InferredPredicates = mergeUniqueStrings(state.report.InferredPredicates, matcherStrings(selector.InferredMatchers)...)
	return nil
}

func applyLabelPredicatePushdown(state *optimizerState) error {
	selector := baseSelectorFromInfo(state.info)
	if selector == nil {
		return nil
	}
	// PushedMatchers is pre-populated by Analyze on the production
	// path. Pure report-emission.
	state.report.PushedPredicates = mergeUniqueStrings(state.report.PushedPredicates, matcherStrings(selector.PushedMatchers)...)
	return nil
}

// baseSelectorFromInfo walks the info side-map to locate the leaf
// selector (first LeafSelector encountered along info.Children). It
// replaces the fragment-walking BaseSelectorSource.
func baseSelectorFromInfo(info *LoweringInfo) *SelectorSource {
	if info == nil {
		return nil
	}
	if info.LeafSelector != nil {
		return info.LeafSelector
	}
	for _, child := range info.Children {
		if sel := baseSelectorFromInfo(child); sel != nil {
			return sel
		}
	}
	return nil
}

// applyProjectionPushdown emits the RequiredColumns explain metadata
// walking the LoweringInfo side-map. Selector-side tag-narrowing was
// lifted to RenderParams in slice 13c-13 and is no longer emitted here.
func applyProjectionPushdown(state *optimizerState) error {
	state.report.RequiredColumns = mergeUniqueStrings(state.report.RequiredColumns, requiredColumnsFromInfo(state.info)...)
	return nil
}

func applyJoinNormalizationDuplicateDetection(state *optimizerState) error {
	state.report.JoinNormalization = joinNormalizationFromInfo(state.info)
	if state.report.JoinNormalization == "not_applicable" {
		return nil
	}
	state.report.SemanticBarriers = mergeUniqueStrings(state.report.SemanticBarriers, "join_key_normalization_boundary")
	return nil
}

func applyFinalSQLShapingLateMaterialization(state *optimizerState) error {
	state.report.MaterializedColumns = mergeUniqueStrings(state.report.MaterializedColumns, baseMaterializedColumnsFromInfo(state.info)...)
	if hasTagsColumn(state.report.RequiredColumns) {
		state.report.SemanticBarriers = mergeUniqueStrings(state.report.SemanticBarriers, "late_tag_materialization")
	}
	return nil
}

// requiredInputBoundsFromInfo derives the [startMS, endMS] envelope
// from info.TimeRequirements + the optimizer context. Live callers
// (native_subtree.go) override the report value with
// renderer.LogicalRequiredInputBounds, which handles @ anchor
// resolution by walking the logical tree; this helper therefore
// skips anchor resolution and only computes the lookback/offset
// envelope off info.
//
// TimeRequirements.Offset stores the absolute-value offset so it
// composes monotonically through subquery/aggregation walks; the
// explain-time envelope preserves the selector's signed offset when
// the root node is a leaf selector by reading info.LeafSelector.
// Non-leaf roots fall back to the absolute TimeRequirements.Offset,
// matching the pre-retirement behavior for nested subtrees.
func requiredInputBoundsFromInfo(info *LoweringInfo, ctx OptimizationContext) (int64, int64, bool) {
	if info == nil {
		return 0, 0, false
	}
	lookbackMS := info.TimeRequirements.Lookback.Milliseconds()
	offsetMS := info.TimeRequirements.Offset.Milliseconds()
	if info.LeafSelector != nil {
		offsetMS = info.LeafSelector.Offset.Milliseconds()
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
		if ctx.StartMS > 0 || ctx.EndMS > 0 {
			endMS := ctx.EndMS - offsetMS
			startMS := ctx.StartMS - offsetMS - lookbackMS
			return startMS, endMS, true
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
	for _, matcher := range selector.Matchers {
		if matcher != nil && matcher.Type == labels.MatchEqual && matcher.Name == "__name__" && matcher.Value == selector.MetricName {
			return []*labels.Matcher{matcher}
		}
	}
	matcher, err := labels.NewMatcher(labels.MatchEqual, "__name__", selector.MetricName)
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

// requiredColumnsFromInfo mirrors the legacy requiredColumnsForFragment
// walk over the LoweringInfo side-map. Each node contributes its output
// kind ("value" vs "time_series"), "tags" when the node shape requires
// tag materialization, and recursively unions its children's contributions.
func requiredColumnsFromInfo(info *LoweringInfo) []string {
	if info == nil {
		return nil
	}
	columns := []string{"value"}
	if info.OutputKind == OutputKindRangeMatrix {
		columns = []string{"time_series"}
	}
	if subtreeShapeRequiresTags(info) {
		columns = append(columns, "tags")
	}
	for _, child := range info.Children {
		columns = append(columns, requiredColumnsFromInfo(child)...)
	}
	return mergeUniqueStrings(nil, columns...)
}

// subtreeShapeRequiresTags mirrors the legacy fragmentRequiresTags
// predicate over info.SubtreeShape + info.OutputKind. The truth table
// tracks the original Fragment-walking version: shapes that emit tags
// either as grouping keys, transform inputs, or simple vector/matrix
// outputs return true; ScalarConvert (scalar output) returns false;
// scalar-typed bare selectors and synthetic-series nodes return false.
func subtreeShapeRequiresTags(info *LoweringInfo) bool {
	if info == nil {
		return false
	}
	switch info.SubtreeShape {
	case SubtreeShapeAggregation,
		SubtreeShapeRangeFunction,
		SubtreeShapeSubquery,
		SubtreeShapeHistogramProjection,
		SubtreeShapeHistogramFunction,
		SubtreeShapeSortTransform,
		SubtreeShapeLabelTransform,
		SubtreeShapeClampTransform,
		SubtreeShapeInfoJoin:
		return true
	case SubtreeShapeScalarConvert:
		return false
	}
	switch info.OutputKind {
	case OutputKindInstantVector, OutputKindRangeMatrix:
		return true
	default:
		return false
	}
}

// baseMaterializedColumnsFromInfo derives the base materialized
// column set (value vs time_series) from info.OutputKind. Used by
// applyFinalSQLShapingLateMaterialization.
func baseMaterializedColumnsFromInfo(info *LoweringInfo) []string {
	if info == nil {
		return nil
	}
	if info.OutputKind == OutputKindRangeMatrix {
		return []string{"time_series"}
	}
	return []string{"value"}
}

// joinNormalizationFromInfo derives the join-normalization
// categorization from info.SubtreeShape (populated by Analyze). Used
// by applyJoinNormalizationDuplicateDetection.
func joinNormalizationFromInfo(info *LoweringInfo) string {
	if info == nil {
		return "not_applicable"
	}
	switch info.SubtreeShape {
	case SubtreeShapeAggregation, SubtreeShapeLeafSource, SubtreeShapeUnarySourceExpr, SubtreeShapeBinaryScalarSourceExpr, SubtreeShapeSyntheticSeries, SubtreeShapeScalarConvert, SubtreeShapeInfoJoin, SubtreeShapeAbsent, SubtreeShapeHistogramProjection, SubtreeShapeHistogramFunction, SubtreeShapeSortTransform, SubtreeShapeLabelTransform, SubtreeShapeClampTransform, SubtreeShapeValueTransform:
		return "not_applicable"
	default:
		return "required"
	}
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

// semanticBarriersFromLogical sources the OptimizationReport's
// SemanticBarriers list from the LoweringInfo side-map populated by
// Analyze.
//
// Signals:
//   - info.SubtreeShape tracks the root node's shape.
//   - info.LabelLineage.MetricName == LabelLineageDropped marks the
//     "metric_name_lineage_change" barrier.
//   - For aggregation nodes, info.Children[0].LabelLineage covers the
//     "parent aggregation + metric-dropping child" shape.
//   - absent_over_time is identified via info.AbsentFunc.
func semanticBarriersFromLogical(node logicalpkg.Node, analysis *Analysis) []string {
	if node == nil || analysis == nil {
		return nil
	}
	info := analysis.InfoFor(node)
	if info == nil {
		return nil
	}
	barriers := []string{}
	switch info.SubtreeShape {
	case SubtreeShapeAggregation:
		barriers = append(barriers, "aggregation_boundary")
	case SubtreeShapeSubquery:
		barriers = append(barriers, "subquery_step_grid")
	case SubtreeShapeRangeFunction:
		barriers = append(barriers, "range_window_materialization_boundary")
	case SubtreeShapeAbsent:
		if info.AbsentFunc == "absent_over_time" {
			barriers = append(barriers, "range_window_materialization_boundary")
		}
	case SubtreeShapeHistogramProjection, SubtreeShapeHistogramFunction:
		barriers = append(barriers, "histogram_bucket_materialization_boundary")
	}
	if logicalNodeDropsMetricName(info) {
		barriers = append(barriers, "metric_name_lineage_change")
	}
	return barriers
}

// logicalNodeDropsMetricName reports whether a node's lineage
// transitions into dropped __name__. Two shapes emit
// "metric_name_lineage_change":
//
//  1. Non-aggregation root that itself drops __name__ — detected via
//     info.LabelLineage.MetricName == LabelLineageDropped. Aggregation
//     nodes always have dropped lineage by construction (see
//     aggregationLabelLineage), so we exclude them from this case and
//     handle them below.
//  2. Aggregation root whose source fragment drops __name__ — we check
//     the first child's LabelLineage. Aggregation plan nodes always
//     record exactly one child in info.Children[0], which mirrors the
//     source fragment's lineage.
func logicalNodeDropsMetricName(info *LoweringInfo) bool {
	if info == nil {
		return false
	}
	if info.SubtreeShape == SubtreeShapeAggregation {
		if len(info.Children) > 0 && info.Children[0] != nil && info.Children[0].LabelLineage.MetricName == LabelLineageDropped {
			return true
		}
		return false
	}
	return info.LabelLineage.MetricName == LabelLineageDropped
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
