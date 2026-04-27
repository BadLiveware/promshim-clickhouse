package logical

import "time"

// TimeDomain describes the temporal shape of a node's output.
//
// The domain is a coarse classification that is intentionally narrower
// than Prometheus' ValueType: it distinguishes instant vectors produced
// at a single timestamp from range matrices spanning a window, and
// folds scalar and constant point-lookup expressions into their own
// enum values so downstream dispatch can fast-path trivial shapes.
type TimeDomain int

const (
	DomainUnknown TimeDomain = iota
	DomainInstant
	DomainRange
	DomainPointLookup
	DomainScalar
)

// Schema records the labels that must (Guaranteed) or may (Possible)
// appear on a node's output series. Two-valued tracking is required for
// instant aliasing, join safety, and correct groupby lowering — each
// depends on knowing which labels are certain vs speculative.
//
// Invariant: Guaranteed ⊆ Possible. AddGuaranteed maintains it by
// writing to both maps; AddPossible writes to Possible only.
type Schema struct {
	Guaranteed map[string]struct{}
	Possible   map[string]struct{}
}

// NewSchema returns a Schema with empty, non-nil label sets.
func NewSchema() Schema {
	return Schema{
		Guaranteed: map[string]struct{}{},
		Possible:   map[string]struct{}{},
	}
}

// AddGuaranteed records that `label` is certainly present on output
// and therefore also possibly present (Guaranteed ⊆ Possible).
func (s *Schema) AddGuaranteed(label string) {
	s.Guaranteed[label] = struct{}{}
	s.Possible[label] = struct{}{}
}

// AddPossible records that `label` may appear on output without
// guaranteeing it.
func (s *Schema) AddPossible(label string) {
	s.Possible[label] = struct{}{}
}

// GroupingKey records aggregation grouping shape: the listed labels,
// and whether they were specified with `by(...)` (Without=false) or
// `without(...)` (Without=true).
type GroupingKey struct {
	Labels  []string
	Without bool
}

// LabelLineageState enumerates what happened to a label between a
// node's input and its output.
//
// The native package uses a separate LabelLineageState enum with
// different values, and the two are not interchangeable.
type LabelLineageState int

const (
	LabelLineagePassthrough LabelLineageState = iota
	LabelLineagePreserved
	LabelLineageDropped
	LabelLineageReplaced
)

// LabelLineage captures how labels flow from a node's input to its
// output. Known lists per-label overrides; Wildcard describes the
// state of labels not explicitly named; MetricName tracks __name__
// separately because aggregations and range functions drop it
// independently of the rest of the labelset.
type LabelLineage struct {
	Known      map[string]string
	Wildcard   string
	MetricName LabelLineageState
}

// TimeRequirements describes the temporal slice a node needs from its
// underlying storage: how far back to read (Lookback), how far
// forward/backward to shift samples (Offset), and whether the node
// forces a subquery step grid to be materialized upstream.
type TimeRequirements struct {
	Lookback              time.Duration
	Offset                time.Duration
	NeedsSubqueryStepGrid bool
}

// NodeInfo is the per-node enrichment record produced by
// logical.Analyze. Fields are the Section 2 enrichment contract —
// Predicates are intentionally omitted until a later phase.
//
// Fragment-producing metadata (NativeLowerable, Fragment,
// NativeReason) lives on native.LoweringInfo until Task 6 deletes the
// Fragment tree; NodeInfo is deliberately decoupled from fragment
// construction.
type NodeInfo struct {
	Schema           Schema
	TimeDomain       TimeDomain
	GroupingKey      GroupingKey
	LabelLineage     LabelLineage
	DropsMetric      bool
	TimeRequirements TimeRequirements

	// Deterministic planning metadata. These fields are physical hints and
	// explain/candidate inputs, not semantic facts. Missing metadata must keep
	// routing conservative and must not affect PromQL results.
	SelectorFingerprint        string
	NormalizedMatchers         []string
	SelectorReuseGroup         string
	SelectorReuseBlockedReason string
	RequiredLabels             []string
	PrunableLabels             []string
	ProjectionUnsafeReason     string
}
