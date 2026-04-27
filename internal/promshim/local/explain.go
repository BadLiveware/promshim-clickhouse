package local

import (
	"sort"

	logicalpkg "github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
	logicalopt "github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical/opt"
	nativeplan "github.com/BadLiveware/promshim-clickhouse/internal/promshim/native"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/storage"
)

type LogicalOptimizationExplain struct {
	Disabled  bool                     `json:"disabled,omitempty"`
	EnvGate   string                   `json:"envGate,omitempty"`
	Passes    []logicalopt.PassResult  `json:"passes,omitempty"`
	Selectors []LogicalSelectorExplain `json:"selectors,omitempty"`
}

type LogicalSelectorExplain struct {
	Expr                    string   `json:"expr,omitempty"`
	Fingerprint             string   `json:"fingerprint,omitempty"`
	NormalizedMatchers      []string `json:"normalizedMatchers,omitempty"`
	ReuseGroup              string   `json:"reuseGroup,omitempty"`
	ReuseBlockedReason      string   `json:"reuseBlockedReason,omitempty"`
	RequiredLookbackSeconds float64  `json:"requiredLookbackSeconds,omitempty"`
	RequiredOffsetSeconds   float64  `json:"requiredOffsetSeconds,omitempty"`
	RequiredLabels          []string `json:"requiredLabels,omitempty"`
	ProjectionUnsafeReason  string   `json:"projectionUnsafeReason,omitempty"`
	NeedsSubqueryStepGrid   bool     `json:"needsSubqueryStepGrid,omitempty"`
}

func explainLogicalOptimization(trace *logicalopt.Trace, analysis *logicalpkg.Analysis) *LogicalOptimizationExplain {
	if trace == nil && analysis == nil {
		return nil
	}
	explain := &LogicalOptimizationExplain{}
	if trace != nil {
		explain.Disabled = trace.Disabled
		explain.EnvGate = trace.EnvGate
		explain.Passes = append([]logicalopt.PassResult(nil), trace.Passes...)
	}
	if analysis != nil {
		explain.Selectors = logicalSelectorExplain(analysis)
	}
	return explain
}

func logicalSelectorExplain(analysis *logicalpkg.Analysis) []LogicalSelectorExplain {
	if analysis == nil {
		return nil
	}
	selectors := []LogicalSelectorExplain{}
	for node, info := range analysis.Info {
		if info == nil || info.SelectorFingerprint == "" {
			continue
		}
		entry := LogicalSelectorExplain{
			Fingerprint:             info.SelectorFingerprint,
			NormalizedMatchers:      append([]string(nil), info.NormalizedMatchers...),
			ReuseGroup:              info.SelectorReuseGroup,
			ReuseBlockedReason:      info.SelectorReuseBlockedReason,
			RequiredLookbackSeconds: info.TimeRequirements.Lookback.Seconds(),
			RequiredOffsetSeconds:   info.TimeRequirements.Offset.Seconds(),
			RequiredLabels:          append([]string(nil), info.RequiredLabels...),
			ProjectionUnsafeReason:  info.ProjectionUnsafeReason,
			NeedsSubqueryStepGrid:   info.TimeRequirements.NeedsSubqueryStepGrid,
		}
		if described, ok := node.(interface{ ExprString() string }); ok {
			entry.Expr = described.ExprString()
		}
		selectors = append(selectors, entry)
	}
	sort.Slice(selectors, func(i, j int) bool {
		if selectors[i].Fingerprint == selectors[j].Fingerprint {
			return selectors[i].Expr < selectors[j].Expr
		}
		return selectors[i].Fingerprint < selectors[j].Fingerprint
	})
	return selectors
}

type planEstimate struct {
	RangeSeconds    float64 `json:"rangeSeconds,omitempty"`
	StepSeconds     float64 `json:"stepSeconds,omitempty"`
	PointsPerSeries int64   `json:"pointsPerSeries,omitempty"`
}

type ExplainNode struct {
	Kind                 string                          `json:"kind"`
	Strategy             string                          `json:"strategy"`
	SelectedStrategy     string                          `json:"selectedStrategy,omitempty"`
	NativeScope          string                          `json:"nativeScope,omitempty"`
	Expr                 string                          `json:"expr,omitempty"`
	Reason               string                          `json:"reason,omitempty"`
	FallbackReason       string                          `json:"fallbackReason,omitempty"`
	Estimate             *planEstimate                   `json:"estimate,omitempty"`
	Lowering             *nativeplan.ExplainInfo         `json:"lowering,omitempty"`
	RulesApplied         []string                        `json:"rulesApplied,omitempty"`
	PushedPredicates     []string                        `json:"pushedPredicates,omitempty"`
	InferredPredicates   []string                        `json:"inferredPredicates,omitempty"`
	RequiredColumns      []string                        `json:"requiredColumns,omitempty"`
	MaterializedColumns  []string                        `json:"materializedColumns,omitempty"`
	SemanticBarriers     []string                        `json:"semanticBarriers,omitempty"`
	RequiredInputStartMS int64                           `json:"requiredInputStartMs,omitempty"`
	RequiredInputEndMS   int64                           `json:"requiredInputEndMs,omitempty"`
	RenderedSQL          string                          `json:"renderedSQL,omitempty"`
	SettingsProfile      *storage.SettingsProfileExplain `json:"settingsProfile,omitempty"`
	LogicalOptimization  *LogicalOptimizationExplain     `json:"logicalOptimization,omitempty"`
	JoinShape            string                          `json:"joinShape,omitempty"`
	JoinLabels           []string                        `json:"joinLabels,omitempty"`
	Children             []ExplainNode                   `json:"children,omitempty"`
}

func estimateRangePointsPerSeries(ctx PlanContext) int64 {
	if ctx.Mode != EvalModeRange || ctx.Step <= 0 || ctx.End.Before(ctx.Start) {
		return 0
	}
	return int64(ctx.End.Sub(ctx.Start)/ctx.Step) + 1
}

func estimateRangePlan(ctx PlanContext) *planEstimate {
	pointsPerSeries := estimateRangePointsPerSeries(ctx)
	if pointsPerSeries == 0 {
		return nil
	}
	return &planEstimate{
		RangeSeconds:    ctx.End.Sub(ctx.Start).Seconds(),
		StepSeconds:     ctx.Step.Seconds(),
		PointsPerSeries: pointsPerSeries,
	}
}

func ExplainPlan(plan Plan) ExplainNode {
	if plan == nil {
		return ExplainNode{}
	}
	explain := plan.explain()
	finalizeExplainNode(&explain)
	return explain
}

func ExplainPlanWithLowering(plan Plan, lowering *nativeplan.LoweringInfo) ExplainNode {
	explain := ExplainPlan(plan)
	annotateExplainNode(&explain, lowering)
	return explain
}

func annotateExplainNode(node *ExplainNode, lowering *nativeplan.LoweringInfo) {
	if node == nil {
		return
	}
	if lowering != nil {
		node.Lowering = lowering.ExplainInfo()
		for index := 0; index < len(node.Children) && index < len(lowering.Children); index++ {
			annotateExplainNode(&node.Children[index], lowering.Children[index])
		}
		return
	}
	for index := range node.Children {
		annotateExplainNode(&node.Children[index], nil)
	}
}

func finalizeExplainNode(node *ExplainNode) {
	if node == nil {
		return
	}
	node.SelectedStrategy = node.Strategy
	switch node.Strategy {
	case "native_sql":
		node.NativeScope = "subtree"
	case "delegated_promql":
		node.NativeScope = "delegated"
	default:
		node.NativeScope = "none"
	}
	if node.Strategy != "native_sql" {
		node.FallbackReason = node.Reason
	}
	for index := range node.Children {
		finalizeExplainNode(&node.Children[index])
	}
}
