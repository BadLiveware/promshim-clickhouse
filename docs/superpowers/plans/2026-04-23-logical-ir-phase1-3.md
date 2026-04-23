# Logical IR Phase 1–3 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace promshim's tier-2 stringly Fragment pipeline with a logical IR (`internal/promshim/logical/`) where each node carries enrichment (schema, time domain, grouping, lineage, drops-metric, time requirements) and the renderer dispatches on node type through a pure `Lower` entry point. Add a fixpoint `Pass` infrastructure. Retire `NativeFragment`.

**Architecture:** Shape C per the spec — enrich today's PromQL-mirror nodes with a side-map `logical.NodeInfo`; keep node vocabulary PromQL-shaped. Renderer switches from `FragmentKind`-keyed dispatch to Go type-switch on `logical.Node`. SQL emission goes exclusively through the existing `internal/promshim/emit/` vocabulary — no `sqlb.RawLit{V: "..."}` in lowering files. Passes operate on nodes only (pure — no in-place mutation); `Optimize` re-analyzes between rewriting passes and caps at 8 iterations.

**Tech Stack:** Go 1.22; `github.com/prometheus/prometheus/promql/parser` (logical input); ClickHouse native SQL (emit target); existing `internal/promshim/emit/`, `internal/promshim/native/sqlb/`, `internal/promshim/storage/` (leaf SQL builders — no rewrite).

**Spec:** `docs/superpowers/specs/2026-04-23-logical-ir-phase1-3-design.md`

---

## File Structure

Packages created, moved, or deleted by this plan (relative to `internal/promshim/`):

| Action | Path | Responsibility |
|---|---|---|
| **Rename** | `plan/` → `logical/` | Logical IR node types (28 existing). `Logical*Plan` → `*Plan` names. |
| **New** | `logical/analyze.go`, `logical/nodeinfo.go` | `logical.Analyze(root) → *Analysis` bottom-up walk; `NodeInfo` struct + `Schema`/`TimeDomain` types. |
| **New** | `logical/build.go` | `logical.ToLogical(parser.Expr) → Node, error`. Body moved from `local.BuildLogicalPlan`. |
| **New** | `logical/opt/pass.go` | `Pass` interface + `Optimize(root, passes) → (Node, *Analysis, error)` fixpoint runner. |
| **New** | `logical/opt/constant_fold_unary.go` | First concrete pass: `-(-x) → x`. |
| **New** | `native/renderer/lower.go` | `Lower(ctx, node)` type-switch dispatcher + `LoweringCtx`. |
| **Modify** | `native/renderer/{source,aggregation_range_fused,histogram,join,clamp,sort,label_transform,range,renderer}.go` | Each exports a per-node-kind `lower*` function; Phase 3 reorganizes to `lower_*.go`. |
| **Modify** | `native/optimizer.go` | Fragment-level optimizer still runs after `Lower`; keeps its current pass list in Phase 1–2. |
| **Modify** | `local/logical_builder.go` | Thin forwarding shim calling `logical.ToLogical`. Deprecated comment. |
| **Modify** | `local/planner.go`, `local/aggregation_pushdown.go`, `compliance/measurement.go` | Call `logical.Analyze` instead of `nativeplan.Analyze`. |
| **Delete (Task 6)** | `native/analysis.go`, `native/analysis_*.go`, `native/builder.go`, `native/types.go` (Fragment types only) | Fragment-construction code retires once every surface routes through `Lower`. |

Files in `native/sqlb/`, `emit/`, `storage/`, and the upstream parser are unchanged. `emit/` grows — every `sqlb.RawLit{V: "..."}` occurrence moved out of lowering functions becomes an `emit.*` helper as the surfaces port.

---

## Task 0: Capture pre-work baselines

**Goal:** Freeze the compliance and bench numbers before any refactor so every later task can diff against the same snapshot.

**Files:**
- Create: `.tmp/baselines/2026-04-23-prework.md` (local, gitignored)

**Acceptance Criteria:**
- [ ] Compliance passes at 538/539 in `prefer` mode with `topk-tie-break-ordering` as the only allowlist hit.
- [ ] `harness/bench/baseline.json` exists and matches current committed state (or is freshly updated if drifted).
- [ ] `.tmp/baselines/2026-04-23-prework.md` records compliance count + bench p50 matrix for quick reference.

**Verify:**
```bash
cat .tmp/baselines/2026-04-23-prework.md
```
Expected: file shows compliance 538/539 and at least one baseline query-profile row from bench-matrix.

**Steps:**

- [ ] **Step 1: Run compliance baseline**

```bash
scripts/run-compliance.sh 2>&1 | tee .tmp/baselines/compliance-prework.log
```
Expected tail: `538/539 passed; 1 allowlisted (topk-tie-break-ordering)`.

- [ ] **Step 2: Run bench baseline + long-range + matrix**

```bash
scripts/run-bench.sh --matrix | tee .tmp/baselines/bench-matrix-prework.md
scripts/run-bench.sh --long-range all
scripts/bench-matrix.sh > .tmp/baselines/bench-crossprofile-prework.md
```
Expected: all three commands exit zero; the matrix markdown lists N/P ratios per category.

- [ ] **Step 3: Write the pre-work snapshot**

```bash
mkdir -p .tmp/baselines
cat > .tmp/baselines/2026-04-23-prework.md <<'EOF'
# Pre-work baselines (2026-04-23)

## Compliance
538/539 — allowlist: topk-tie-break-ordering

## Bench matrix
See bench-matrix-prework.md and bench-crossprofile-prework.md.

## Git ref
EOF
git rev-parse HEAD >> .tmp/baselines/2026-04-23-prework.md
```

- [ ] **Step 4: No commit**

Baselines live in `.tmp/` (gitignored). This is a check, not a code change.

---

## Task 1: Rename `plan/` to `logical/`; drop `Logical` prefix on node types

**Goal:** Move `internal/promshim/plan/` to `internal/promshim/logical/`. Rename 28 exported types (`LogicalAggregationPlan` → `AggregationPlan`, etc.). Update every import site. No behavior change.

**Files:**
- Rename: `internal/promshim/plan/*.go` → `internal/promshim/logical/*.go`
- Modify (import path + type name updates): every file listed under `grep -l 'ch-observability/internal/promshim/plan\|planpkg\.\|plan\.Logical'` (~24 files).

**Acceptance Criteria:**
- [ ] Package declaration in moved files is `package logical`.
- [ ] No Go file compiles with `import "ch-observability/internal/promshim/logical"`.
- [ ] No `plan.Logical` or `planpkg.Logical` identifiers remain in code (comments allowed temporarily).
- [ ] All types keep their method sets unchanged; `logicalPlan()` marker method stays (rename to `logicalNode()` to match new package-name hygiene).
- [ ] `go build ./...` green.
- [ ] `go test ./internal/promshim/...` green.
- [ ] Compliance still 538/539.

**Verify:**
```bash
go build ./... && go test ./internal/promshim/... && scripts/run-compliance.sh
```
Expected: build + tests pass; compliance 538/539 unchanged.

**Steps:**

- [ ] **Step 1: Move files verbatim**

```bash
git mv internal/promshim/plan internal/promshim/logical
```

Verify:
```bash
ls internal/promshim/logical/
```
Expected: `logicaltypes.go`, `promql.go`, `promql_calls_misc.go`, `promql_calls_rangefunc.go`, `promql_test.go`.

- [ ] **Step 2: Change the package declaration in every moved file**

Edit each of the five moved files. Replace the single occurrence:

```diff
-package plan
+package logical
```

- [ ] **Step 3: Rename the marker method and all 28 exported types**

Within `internal/promshim/logical/logicaltypes.go`:

```diff
-type LogicalPlan interface {
-	logicalPlan()
+type Node interface {
+	logicalNode()
 	valueType() parser.ValueType
 	exprString() string
 }
```

Then, for each of the 28 `Logical*Plan` types, drop the `Logical` prefix:

```diff
-type LogicalLeafExprPlan struct{ Expr parser.Expr }
-func (*LogicalLeafExprPlan) logicalPlan()                  {}
+type LeafExprPlan struct{ Expr parser.Expr }
+func (*LeafExprPlan) logicalNode()                         {}
```

Apply the same pattern to: `ScalarLiteralPlan`, `UnaryPlan`, `BinaryPlan`, `AggregationPlan`, `HistogramQuantilePlan`, `HistogramFractionPlan`, `HistogramProjectionPlan`, `HistogramQuantilesPlan`, `RangeFunctionPlan`, `VectorPlan`, `RoundPlan`, `SortPlan`, `ScalarConvertPlan`, `InfoPlan`, `PointwiseFunctionPlan`, `ScalarBuiltinPlan`, `RatePlan`, `IncreasePlan`, `DeltaPlan`, `ChangesPlan`, `DerivPlan`, `QuantileOverTimePlan`, `AbsentPlan`, `AbsentOverTimePlan`, `SubqueryPlan`, `LabelReplacePlan`, `LabelJoinPlan`.

Quick way:
```bash
# NOTE: `Plan` suffix stays; only the Logical prefix drops.
# Run from repo root, reviewed diff before committing.
sed -i 's/\bLogical\([A-Z][A-Za-z]*Plan\)\b/\1/g' internal/promshim/logical/*.go
sed -i 's/logicalPlan()/logicalNode()/g' internal/promshim/logical/*.go
```

- [ ] **Step 4: Rewrite import aliases across the codebase**

The existing imports are mostly `planpkg "ch-observability/internal/promshim/logical"` and `nativeplan "ch-observability/internal/promshim/native"` (note: `nativeplan` is the alias for the *native* package — keep that one). Target pattern:

```diff
-	planpkg "ch-observability/internal/promshim/logical"
+	logicalpkg "ch-observability/internal/promshim/logical"
```

and the matching callsites:

```diff
-case *planpkg.LogicalAggregationPlan:
+case *logicalpkg.AggregationPlan:
```

Apply across these files (full list — verify with `grep -rl '"ch-observability/internal/promshim/logical"' internal/promshim/`):
`native/*.go`, `native/renderer/*.go`, `local/*.go`, `compliance/measurement.go`, `service.go`.

Automated helper — review diff before committing:
```bash
git grep -l '"ch-observability/internal/promshim/logical"' | xargs sed -i 's#"ch-observability/internal/promshim/logical"#"ch-observability/internal/promshim/logical"#g'
git grep -l 'planpkg\.' | xargs sed -i 's/\bplanpkg\./logicalpkg./g; s/planpkg "ch/logicalpkg "ch/g'
git grep -l 'plan\.Logical' | xargs sed -i 's/\bplan\.Logical\([A-Z][A-Za-z]*Plan\)/logical.\1/g'
```

Also update the `local/logical_builder.go` type aliases:

```diff
-type logicalPlan = plan.LogicalPlan
-type logicalLeafExprPlan = plan.LogicalLeafExprPlan
+type logicalPlan = logical.Node
+type logicalLeafExprPlan = logical.LeafExprPlan
```

(repeat for the remaining 27 aliases — names lose the `Logical` prefix only in the RHS).

- [ ] **Step 5: Build**

```bash
go build ./...
```
Expected: zero errors. If build fails, use `go build ./... 2>&1 | head -40` and fix residual references (commonly: typos in the sed pattern, missed `_test.go` file, or a `planpkg` identifier inside a string literal).

- [ ] **Step 6: Test**

```bash
go test ./internal/promshim/...
```
Expected: PASS. Any FAIL in existing tests is not from the rename (which is pure renaming) — revert the rename and bisect.

- [ ] **Step 7: Compliance check**

```bash
scripts/run-compliance.sh
```
Expected tail: `538/539 passed; 1 allowlisted`.

- [ ] **Step 8: Commit**

```bash
git add -A
git commit -m "$(cat <<'EOF'
Rename plan/ package to logical/ and drop Logical prefix from node types

Pure move + rename. Package is now internal/promshim/logical/; types are
Node / LeafExprPlan / AggregationPlan / ... The logicalPlan() marker method
becomes logicalNode() to match the new package name. No behavior change;
compliance 538/539 and all unit tests green.

First commit in the logical-IR Phase 1-3 plan
(docs/superpowers/specs/2026-04-23-logical-ir-phase1-3-design.md).

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: Extract `logical.ToLogical` entry point

**Goal:** Move `local.BuildLogicalPlan`'s body into `logical.ToLogical(parser.Expr) → (logical.Node, error)`. `local.BuildLogicalPlan` becomes a one-line deprecated forwarding shim so every existing caller still compiles.

**Files:**
- Create: `internal/promshim/logical/build.go`
- Modify: `internal/promshim/local/logical_builder.go` (thin shim)
- Modify: `internal/promshim/local/logical_builder_helpers.go` (move helpers that only depend on `parser.Expr` / node types into `logical/build.go`; keep callsite-specific helpers in `local/`)

**Acceptance Criteria:**
- [ ] `logical.ToLogical` is the canonical entry. Exported, documented, called by `local`, `compliance`, and `service`.
- [ ] `local.BuildLogicalPlan` still compiles and delegates to `logical.ToLogical`. Has `// Deprecated: use logical.ToLogical` comment.
- [ ] No new behavior — same errors for the same inputs. Existing `local/logical_builder_test.go` passes unchanged.
- [ ] `go test ./internal/promshim/logical/... -run TestToLogical` passes (new tests mirror existing ones).

**Verify:**
```bash
go test ./internal/promshim/... && scripts/run-compliance.sh
```
Expected: tests pass; compliance 538/539.

**Steps:**

- [ ] **Step 1: Write a failing test for ToLogical**

Create `internal/promshim/logical/build_test.go`:

```go
package logical

import (
	"testing"

	"github.com/prometheus/prometheus/promql/parser"
)

func TestToLogicalLeafPlanForSelector(t *testing.T) {
	expr, err := parser.ParseExpr(`up{job="api"}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	node, err := ToLogical(expr)
	if err != nil {
		t.Fatalf("ToLogical: %v", err)
	}
	leaf, ok := node.(*LeafExprPlan)
	if !ok {
		t.Fatalf("expected *LeafExprPlan, got %T", node)
	}
	if leaf.Expr.String() != `up{job="api"}` {
		t.Fatalf("expr roundtrip: %q", leaf.Expr.String())
	}
}

func TestToLogicalRejectsUnsupportedExpressions(t *testing.T) {
	_, err := ToLogical(nil)
	if err == nil {
		t.Fatal("expected error for nil expression")
	}
}
```

Run:
```bash
go test ./internal/promshim/logical/ -run TestToLogical
```
Expected: FAIL — `undefined: ToLogical`.

- [ ] **Step 2: Move the builder body into `logical/build.go`**

Create `internal/promshim/logical/build.go`:

```go
package logical

import (
	"fmt"

	"ch-observability/internal/promshim/model"
	promlabels "github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

// ToLogical translates a Prometheus parser expression into the promshim
// logical IR. It is the canonical entry point for tiers 1-3 planning and
// tier-2 SQL lowering. The returned Node tree is structurally identifiable
// by PromQL shape; enrichment (schema, time domain, grouping, etc.) is
// produced separately by Analyze.
func ToLogical(expr parser.Expr) (Node, error) {
	if expr == nil {
		return nil, fmt.Errorf("logical: ToLogical requires a non-nil expression")
	}
	return buildLogicalPlan(expr)
}

func buildLogicalPlan(expr parser.Expr) (Node, error) {
	// MOVE: the existing body of local.BuildLogicalPlan + every helper it
	// calls that only touches parser.Expr / logical types. Rename the
	// recursive call sites from BuildLogicalPlan → buildLogicalPlan.
}
```

Move the *entire* body of `local.BuildLogicalPlan` (currently at `internal/promshim/local/logical_builder.go:70-473`) into `buildLogicalPlan` above. Also move these helpers from `local/logical_builder_helpers.go` (they're self-contained):
- `buildLogicalCallPlan`
- `buildLogicalPointwiseCallPlan`
- any helper on `local/logical_builder_helpers.go` that does not reference a `local/`-specific type.

Keep helpers that touch `local.PlanContext`, `local.ExecPlan`, or `local.*Error*` in `local/`.

Use `model` and `promlabels` imports as they appear in the current `local/` file.

- [ ] **Step 3: Replace `local.BuildLogicalPlan` with a shim**

Edit `internal/promshim/local/logical_builder.go`:

```go
package local

import (
	"ch-observability/internal/promshim/logical"
	"github.com/prometheus/prometheus/promql/parser"
)

// Type aliases for migrating callers — kept for backwards compatibility.
type logicalPlan = logical.Node

type logicalLeafExprPlan = logical.LeafExprPlan
// ... remaining 27 aliases, all referencing logical.<name>

// Deprecated: use logical.ToLogical.
func BuildLogicalPlan(expr parser.Expr) (logicalPlan, error) {
	return logical.ToLogical(expr)
}
```

Delete the moved helper bodies from `local/logical_builder_helpers.go`.

- [ ] **Step 4: Run the unit tests**

```bash
go test ./internal/promshim/logical/ -run TestToLogical
go test ./internal/promshim/local/
```
Expected: both PASS. The `local` tests exercising `BuildLogicalPlan` still pass because it forwards.

- [ ] **Step 5: Compliance check**

```bash
scripts/run-compliance.sh
```
Expected: 538/539.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "$(cat <<'EOF'
Extract logical.ToLogical entry point from local.BuildLogicalPlan

ToLogical is the canonical parser-expr-to-logical-IR translation. local
retains BuildLogicalPlan as a deprecated forwarding shim plus type aliases
so existing call sites compile. No semantic change; unit tests and
compliance (538/539) unchanged.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: Introduce `logical.Analyze` + `NodeInfo` side-map

**Goal:** Add `logical.Analyze(root) → *logical.Analysis` with a per-node `NodeInfo` carrying the Section 2 enrichment contract (Schema.Guaranteed / Schema.Possible, TimeDomain, GroupingKey, LabelLineage, DropsMetric, TimeRequirements — Predicates deferred). Coexist with `native.Analyze` for now; Fragment-producing fields (`NativeLowerable`, `Fragment`, `NativeReason`) stay on `native.LoweringInfo` until Task 6.

**Files:**
- Create: `internal/promshim/logical/nodeinfo.go`
- Create: `internal/promshim/logical/analyze.go`
- Create: `internal/promshim/logical/analyze_test.go`

**Acceptance Criteria:**
- [ ] `logical.NodeInfo` has fields: `Schema Schema`, `TimeDomain TimeDomain`, `GroupingKey GroupingKey`, `LabelLineage LabelLineage`, `DropsMetric bool`, `TimeRequirements TimeRequirements`. No Predicates.
- [ ] `Schema` is two-valued: `Guaranteed` and `Possible`, both `map[string]struct{}` (set semantics). `Guaranteed ⊆ Possible` invariant is enforced.
- [ ] `TimeDomain` is an enum: `DomainInstant`, `DomainRange`, `DomainPointLookup`, `DomainScalar`.
- [ ] `Analyze` is a single bottom-up walk; returns `*Analysis{ Root Node; Info map[Node]*NodeInfo }`.
- [ ] `LabelLineage` and `TimeRequirements` are copied from the existing `native` implementations (identical semantics, new home).
- [ ] Unit tests cover enrichment for: leaf selector, scalar literal, unary, binary, aggregation-by, aggregation-without, range function, subquery, `label_replace`, `count_values`, `histogram_quantile`.

**Verify:**
```bash
go test ./internal/promshim/logical/ -run TestAnalyze -v
```
Expected: all analyze tests pass.

**Steps:**

- [ ] **Step 1: Define the enrichment types**

Create `internal/promshim/logical/nodeinfo.go`:

```go
package logical

import "time"

type TimeDomain int

const (
	DomainUnknown TimeDomain = iota
	DomainInstant
	DomainRange
	DomainPointLookup
	DomainScalar
)

type Schema struct {
	Guaranteed map[string]struct{}
	Possible   map[string]struct{}
}

// NewSchema returns a zero-valued schema — no labels guaranteed, none possible.
func NewSchema() Schema {
	return Schema{
		Guaranteed: map[string]struct{}{},
		Possible:   map[string]struct{}{},
	}
}

// AddGuaranteed adds label to both Guaranteed and Possible (invariant: Guaranteed ⊆ Possible).
func (s *Schema) AddGuaranteed(label string) {
	s.Guaranteed[label] = struct{}{}
	s.Possible[label] = struct{}{}
}

// AddPossible adds label to Possible only.
func (s *Schema) AddPossible(label string) {
	s.Possible[label] = struct{}{}
}

type GroupingKey struct {
	Labels  []string
	Without bool
}

// LabelLineage and TimeRequirements keep their existing shape from native;
// moved verbatim with package rename. See analyze.go for adapters.
type LabelLineage struct {
	Known      map[string]string
	Wildcard   string
	MetricName LabelLineageState
}

type LabelLineageState int

const (
	LabelLineagePassthrough LabelLineageState = iota
	LabelLineagePreserved
	LabelLineageDropped
	LabelLineageReplaced
)

type TimeRequirements struct {
	Lookback              time.Duration
	Offset                time.Duration
	NeedsSubqueryStepGrid bool
}

// NodeInfo is the enrichment sidecar for a logical.Node. It lives in
// Analysis.Info keyed by the node pointer; nodes themselves stay
// structurally comparable (no attribute noise).
type NodeInfo struct {
	Schema           Schema
	TimeDomain       TimeDomain
	GroupingKey      GroupingKey
	LabelLineage     LabelLineage
	DropsMetric      bool
	TimeRequirements TimeRequirements
}
```

- [ ] **Step 2: Write failing tests for Analyze**

Create `internal/promshim/logical/analyze_test.go`:

```go
package logical

import (
	"testing"

	"github.com/prometheus/prometheus/promql/parser"
)

func mustToLogical(t *testing.T, q string) Node {
	t.Helper()
	expr, err := parser.ParseExpr(q)
	if err != nil {
		t.Fatalf("parse %q: %v", q, err)
	}
	node, err := ToLogical(expr)
	if err != nil {
		t.Fatalf("ToLogical %q: %v", q, err)
	}
	return node
}

func TestAnalyzeLeafHasInstantDomainAndNoGrouping(t *testing.T) {
	root := mustToLogical(t, `up{job="api"}`)
	a := Analyze(root)
	info := a.Info[root]
	if info == nil {
		t.Fatal("no info for leaf")
	}
	if info.TimeDomain != DomainInstant {
		t.Errorf("leaf TimeDomain = %v, want DomainInstant", info.TimeDomain)
	}
	if len(info.GroupingKey.Labels) != 0 {
		t.Errorf("leaf GroupingKey should be empty, got %v", info.GroupingKey.Labels)
	}
	if info.DropsMetric {
		t.Error("leaf should not drop __name__")
	}
}

func TestAnalyzeAggregationSetsGroupingKey(t *testing.T) {
	root := mustToLogical(t, `sum by (job) (up)`)
	a := Analyze(root)
	info := a.Info[root]
	if info == nil {
		t.Fatal("no info for aggregation")
	}
	if info.GroupingKey.Without {
		t.Error("sum by(job) should have Without=false")
	}
	if got := info.GroupingKey.Labels; len(got) != 1 || got[0] != "job" {
		t.Errorf("GroupingKey.Labels = %v, want [job]", got)
	}
	if _, ok := info.Schema.Guaranteed["job"]; !ok {
		t.Error("sum by(job) should guarantee the job label on output")
	}
	if !info.DropsMetric {
		t.Error("aggregations drop __name__")
	}
}

func TestAnalyzeLabelReplaceAddsPossibleLabel(t *testing.T) {
	root := mustToLogical(t, `label_replace(up, "target", "${1}", "instance", "(.*):.*")`)
	a := Analyze(root)
	info := a.Info[root]
	if _, ok := info.Schema.Possible["target"]; !ok {
		t.Error("label_replace should list the destination label as Possible")
	}
	if _, ok := info.Schema.Guaranteed["target"]; ok {
		t.Error("label_replace output label must NOT be Guaranteed (regex may not match)")
	}
}

func TestAnalyzeRangeFunctionHasRangeDomain(t *testing.T) {
	root := mustToLogical(t, `rate(up[5m])`)
	a := Analyze(root)
	info := a.Info[root]
	if info.TimeDomain != DomainInstant {
		// rate() consumes a matrix and returns an instant vector.
		t.Errorf("rate TimeDomain = %v, want DomainInstant", info.TimeDomain)
	}
	if info.TimeRequirements.Lookback.Seconds() != 300 {
		t.Errorf("rate lookback = %v, want 5m", info.TimeRequirements.Lookback)
	}
}

func TestAnalyzeScalarLiteralDomain(t *testing.T) {
	root := mustToLogical(t, `42`)
	a := Analyze(root)
	info := a.Info[root]
	if info.TimeDomain != DomainScalar {
		t.Errorf("literal TimeDomain = %v, want DomainScalar", info.TimeDomain)
	}
}
```

Run:
```bash
go test ./internal/promshim/logical/ -run TestAnalyze
```
Expected: FAIL — `undefined: Analyze`.

- [ ] **Step 3: Implement Analyze**

Create `internal/promshim/logical/analyze.go`:

```go
package logical

import (
	"github.com/prometheus/prometheus/promql/parser"
)

type Analysis struct {
	Root Node
	Info map[Node]*NodeInfo
}

func Analyze(root Node) *Analysis {
	a := &Analysis{Root: root, Info: map[Node]*NodeInfo{}}
	a.walk(root)
	return a
}

func (a *Analysis) walk(node Node) *NodeInfo {
	if node == nil {
		return nil
	}
	if existing, ok := a.Info[node]; ok {
		return existing
	}
	info := &NodeInfo{Schema: NewSchema()}
	a.Info[node] = info

	switch n := node.(type) {
	case *LeafExprPlan:
		info.TimeDomain = DomainInstant
		info.LabelLineage = leafLabelLineage()
		info.TimeRequirements = leafTimeRequirements(n.Expr)
		schemaFromSelector(&info.Schema, n.Expr)

	case *ScalarLiteralPlan:
		info.TimeDomain = DomainScalar
		info.DropsMetric = true

	case *UnaryPlan:
		child := a.walk(n.Child)
		propagateFromChild(info, child)

	case *BinaryPlan:
		lhs := a.walk(n.LHS)
		rhs := a.walk(n.RHS)
		combineBinary(info, n, lhs, rhs)

	case *AggregationPlan:
		child := a.walk(n.Child)
		info.DropsMetric = true
		info.GroupingKey = GroupingKey{Labels: append([]string(nil), n.Grouping...), Without: n.Without}
		info.TimeDomain = child.TimeDomain
		info.TimeRequirements = child.TimeRequirements
		schemaForAggregation(&info.Schema, child.Schema, n.Grouping, n.Without)

	case *RangeFunctionPlan:
		child := a.walk(n.Child)
		info.TimeDomain = DomainInstant
		info.LabelLineage = child.LabelLineage
		info.TimeRequirements = child.TimeRequirements
		info.Schema = child.Schema

	case *LabelReplacePlan:
		child := a.walk(n.Child)
		info.TimeDomain = child.TimeDomain
		info.Schema = child.Schema
		info.Schema.AddPossible(n.Dst) // regex may fail to match; destination label is not guaranteed
		info.LabelLineage = child.LabelLineage
		info.TimeRequirements = child.TimeRequirements

	// ...repeat for every Node subtype. See the existing native/analysis*.go
	// for LabelLineage and TimeRequirements propagation — copy those
	// propagation helpers verbatim (rename package to logical).

	default:
		// Unknown node type. Leave NodeInfo at zero values; callers that
		// need enrichment will see DomainUnknown and can fall back.
	}
	return info
}

// Helper adapters moved from native/analysis_support.go (same implementation,
// new package). Keep signatures identical so tests translate 1:1.

func leafLabelLineage() LabelLineage { /* copy from native */ }

func leafTimeRequirements(expr parser.Expr) TimeRequirements { /* copy from native */ }

func propagateFromChild(info *NodeInfo, child *NodeInfo) { /* copy from native */ }

func combineBinary(info *NodeInfo, n *BinaryPlan, lhs, rhs *NodeInfo) { /* copy from native */ }

func schemaFromSelector(s *Schema, expr parser.Expr) { /* new: populate Guaranteed from "=" matchers, AddPossible from "=~" */ }

func schemaForAggregation(out *Schema, in Schema, grouping []string, without bool) {
	if without {
		// Without: start from child's Possible, remove grouping labels.
		for l := range in.Possible {
			skip := false
			for _, g := range grouping {
				if l == g {
					skip = true
					break
				}
			}
			if !skip {
				out.AddPossible(l)
			}
		}
		for l := range in.Guaranteed {
			skip := false
			for _, g := range grouping {
				if l == g {
					skip = true
					break
				}
			}
			if !skip {
				out.AddGuaranteed(l)
			}
		}
		return
	}
	// By: output schema is exactly the grouping labels — guaranteed if they were possible on the input.
	for _, g := range grouping {
		if _, ok := in.Possible[g]; ok {
			// Guaranteed only if also guaranteed on input; else Possible.
			if _, g2 := in.Guaranteed[g]; g2 {
				out.AddGuaranteed(g)
			} else {
				out.AddPossible(g)
			}
		} else {
			out.AddPossible(g)
		}
	}
}
```

For every existing `native/analysis_*.go` case statement (21 node kinds), port the body into the type-switch above. Reuse propagation helpers by copying them verbatim — `native.walk` is the reference implementation. Do not add Schema enrichment for nodes where the existing native code doesn't already compute label sets; leave Schema empty for them (safe: consumers check `Guaranteed` / `Possible` membership, not presence of the map).

- [ ] **Step 4: Run unit tests**

```bash
go test ./internal/promshim/logical/ -run TestAnalyze -v
```
Expected: all tests PASS.

- [ ] **Step 5: Hook existing callers in parallel (non-breaking)**

Leave `native.Analyze` in place — it still drives Fragment construction. Add a parallel call for downstream consumers that want the new enrichment:

```diff
 // internal/promshim/local/planner.go
 func BuildEntireQueryDelegatedPlan(expr parser.Expr) (Plan, *nativeplan.Analysis, error) {
-	logical, err := BuildLogicalPlan(expr)
+	logicalRoot, err := logicalpkg.ToLogical(expr)
 	if err != nil {
 		return nil, nil, err
 	}
-	analysis := nativeplan.Analyze(logical)
-	return annotateQueryPlan(&delegatedExprPlan{Expr: expr}, analysis.Root), analysis, nil
+	nativeAnalysis := nativeplan.Analyze(logicalRoot)
+	_ = logicalpkg.Analyze(logicalRoot) // enrichment available; consumers wired in Task 4
+	return annotateQueryPlan(&delegatedExprPlan{Expr: expr}, nativeAnalysis.Root), nativeAnalysis, nil
 }
```

Repeat the `_ = logicalpkg.Analyze(...)` insertion everywhere `nativeplan.Analyze` is called. This keeps the walk warm and shakes out crashes under real workloads before Task 4 reads the results.

- [ ] **Step 6: Compliance + bench**

```bash
scripts/run-compliance.sh
scripts/run-bench.sh --matrix
```
Expected: 538/539; bench within ±3% of Task 0 baseline.

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "$(cat <<'EOF'
Introduce logical.Analyze and NodeInfo side-map

Bottom-up walk producing per-node enrichment (Schema.Guaranteed /
Schema.Possible, TimeDomain, GroupingKey, LabelLineage, DropsMetric,
TimeRequirements). Coexists with native.Analyze; Fragment-producing
fields stay on native.LoweringInfo until renderer surfaces port.

Compliance 538/539; bench within baseline tolerance.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: Add `renderer.Lower` dispatch for the core subset

**Goal:** Introduce `renderer.Lower(ctx LoweringCtx, node logical.Node) (RenderedQuery, error)` with a type-switch. Phase 1 handles three node kinds through the new path: `LeafExprPlan`, `ScalarLiteralPlan`, and `BinaryPlan` (scalar-scalar and scalar-vector trivial forms). Everything else continues through `RenderFragment`. A hierarchical router picks which path a whole query uses; a single query is never split between paths.

**Files:**
- Create: `internal/promshim/native/renderer/lower.go`
- Create: `internal/promshim/native/renderer/lower_test.go`
- Modify: `internal/promshim/native/renderer/source.go` — expose `lowerLeaf(ctx, *logical.LeafExprPlan)` alongside existing `renderSourceFragment`.
- Modify: `internal/promshim/service.go` — choose Lower vs Fragment based on node kind of `root`.

**Acceptance Criteria:**
- [ ] `Lower(ctx, node)` dispatches via type switch; unsupported node kinds return `errUnsupportedLowerNode` (a sentinel the router checks to fall back).
- [ ] Per-kind `lower*` functions are pure given `ctx`. No package-level state. They build SQL by calling `emit/` helpers and/or `storage/` builders — no `sqlb.RawLit{V: "..."}` in the new code.
- [ ] The hierarchical router: at `service.go` entry, it attempts `Lower(ctx, root)`. If `Lower` returns `errUnsupportedLowerNode`, the router falls back to the existing Fragment path for that entire query.
- [ ] `Lower` produces byte-identical SQL to the Fragment path for the three supported node kinds (verified by a differential test in `lower_test.go`).
- [ ] Compliance 538/539. Bench within ±3% of Task 0 baseline.

**Verify:**
```bash
go test ./internal/promshim/native/renderer/ -run TestLower
scripts/run-compliance.sh
scripts/run-bench.sh --matrix
```
Expected: unit tests pass; compliance 538/539; bench within tolerance.

**Steps:**

- [ ] **Step 1: Write the dispatcher signature + sentinel**

Create `internal/promshim/native/renderer/lower.go`:

```go
package renderer

import (
	"errors"
	"fmt"

	"ch-observability/internal/promshim/logical"
	"ch-observability/internal/promshim/storage"
)

var errUnsupportedLowerNode = errors.New("renderer: node kind not yet supported by Lower — fall back to Fragment path")

// LoweringCtx carries everything Lower needs that is not on the node itself.
// It is immutable for the duration of a Lower call. Lowering functions
// consume it read-only.
type LoweringCtx struct {
	Config   storage.QueryConfig
	Analysis *logical.Analysis
	Params   RenderParams
}

// Lower translates a logical.Node subtree into a rendered ClickHouse query.
// Unsupported node kinds return errUnsupportedLowerNode; callers handle
// hierarchical fallback to the Fragment dispatcher.
func Lower(ctx LoweringCtx, node logical.Node) (RenderedQuery, error) {
	if node == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: Lower called with nil node")
	}
	if ctx.Analysis == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: Lower requires an Analysis")
	}
	switch n := node.(type) {
	case *logical.LeafExprPlan:
		return lowerLeaf(ctx, n)
	case *logical.ScalarLiteralPlan:
		return lowerScalarLiteral(ctx, n)
	case *logical.BinaryPlan:
		return lowerBinary(ctx, n)
	default:
		return RenderedQuery{}, errUnsupportedLowerNode
	}
}

// IsUnsupportedByLower reports whether err means Lower couldn't handle the
// node kind (and the caller should fall back to Fragment).
func IsUnsupportedByLower(err error) bool { return errors.Is(err, errUnsupportedLowerNode) }
```

- [ ] **Step 2: Implement `lowerLeaf` + `lowerScalarLiteral`**

Add to `internal/promshim/native/renderer/lower.go`:

```go
func lowerLeaf(ctx LoweringCtx, n *logical.LeafExprPlan) (RenderedQuery, error) {
	// Reuse the existing leaf-source builder. The Fragment body for
	// FragmentKindLeafSource is equivalent; we construct the same
	// NativeFragment transparently to share SQL emission until Task 6
	// replaces it with direct emit/ calls.
	fragment := &native.NativeFragment{
		Kind:         native.FragmentKindLeafSource,
		OutputKind:   native.OutputKindInstantVector,
		SourcePromQL: n.Expr,
		// Selector recomputed from the parser expr — no need for native.walk
		// to have produced it, because leaves are self-describing.
		Selector: native.BuildSelectorSourceFromExpr(n.Expr),
		ValueExpr: "{value}",
		TagsExpr:  "{tags}",
	}
	rf, err := renderFragment(ctx.Config, fragment, ctx.Params)
	if err != nil {
		return RenderedQuery{}, err
	}
	return finalizeRenderedFragment(rf)
}

func lowerScalarLiteral(ctx LoweringCtx, n *logical.ScalarLiteralPlan) (RenderedQuery, error) {
	fragment := &native.NativeFragment{
		Kind:        native.FragmentKindSyntheticSeries,
		OutputKind:  native.OutputKindScalar,
		DropsMetric: true,
		Synthetic:   &native.SyntheticSeriesFragment{Func: "literal", Value: native.CloneFloat64(&n.Value)},
	}
	return RenderFragment(ctx.Config, fragment, ctx.Params)
}
```

If `native.BuildSelectorSourceFromExpr` / `native.CloneFloat64` aren't exported today, expose them from `native/selector.go` / `native/builder.go`. Both are trivially exportable.

- [ ] **Step 3: Implement `lowerBinary` — scalar-only subset**

```go
func lowerBinary(ctx LoweringCtx, n *logical.BinaryPlan) (RenderedQuery, error) {
	lhsInfo := ctx.Analysis.Info[n.LHS]
	rhsInfo := ctx.Analysis.Info[n.RHS]
	if lhsInfo == nil || rhsInfo == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: binary node missing analysis")
	}
	// Phase 1 handles only scalar-scalar and scalar-vector trivial binaries.
	// Anything else falls back to Fragment.
	if lhsInfo.TimeDomain != logical.DomainScalar && rhsInfo.TimeDomain != logical.DomainScalar {
		return RenderedQuery{}, errUnsupportedLowerNode
	}
	// Delegate to the existing Fragment code path for now by constructing a
	// BinaryJoin fragment — the new lowerBinary takes over the vocabulary
	// for *emission*, but reuses join.go's SQL-producing helpers.
	return renderBinaryViaFragment(ctx, n)
}

func renderBinaryViaFragment(ctx LoweringCtx, n *logical.BinaryPlan) (RenderedQuery, error) {
	// Temporarily: use BuildFragment + RenderFragment for the scalar-only
	// case. In Task 6 this function is rewritten to call emit/ helpers
	// directly and join.go's lowerBinaryScalarVectorJoin(ctx, n).
	fragment, err := native.BuildFragment(n, nativeAnalysisFromLogical(ctx.Analysis))
	if err != nil {
		return RenderedQuery{}, err
	}
	return RenderFragment(ctx.Config, fragment, ctx.Params)
}

// nativeAnalysisFromLogical is a temporary bridge. Task 6 deletes it.
func nativeAnalysisFromLogical(a *logical.Analysis) *native.Analysis {
	return native.Analyze(a.Root)
}
```

- [ ] **Step 4: Hierarchical router in `service.go`**

Find the tier-2 rendering call — it currently goes through `RenderFragment` directly. Wrap it:

```go
func (s *Service) renderTier2(ctx context.Context, root logical.Node, analysis *logical.Analysis, params renderer.RenderParams) (renderer.RenderedQuery, error) {
	lowerCtx := renderer.LoweringCtx{
		Config:   s.storageConfig,
		Analysis: analysis,
		Params:   params,
	}
	rq, err := renderer.Lower(lowerCtx, root)
	if err == nil {
		return rq, nil
	}
	if !renderer.IsUnsupportedByLower(err) {
		return renderer.RenderedQuery{}, err
	}
	// Fallback: Fragment dispatch for the whole query. Hierarchical split —
	// never mix Lower and Fragment within one query.
	fragment, err := native.BuildFragment(root, native.Analyze(root))
	if err != nil {
		return renderer.RenderedQuery{}, err
	}
	return renderer.RenderFragment(s.storageConfig, fragment, params)
}
```

- [ ] **Step 5: Write differential test**

Create `internal/promshim/native/renderer/lower_test.go`:

```go
package renderer

import (
	"testing"

	"ch-observability/internal/promshim/logical"
	"ch-observability/internal/promshim/native"
	"github.com/prometheus/prometheus/promql/parser"
)

func renderBothPaths(t *testing.T, q string) (string, string) {
	t.Helper()
	expr, err := parser.ParseExpr(q)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	root, err := logical.ToLogical(expr)
	if err != nil {
		t.Fatalf("ToLogical: %v", err)
	}
	analysis := logical.Analyze(root)
	params := RenderParams{Mode: native.RenderModeInstant, EvaluationTimeMS: 1_700_000_000_000}
	ctx := LoweringCtx{Config: testConfig(), Analysis: analysis, Params: params}

	lowerRQ, err := Lower(ctx, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}

	fragment, err := native.BuildFragment(root, native.Analyze(root))
	if err != nil {
		t.Fatalf("BuildFragment: %v", err)
	}
	fragmentRQ, err := RenderFragment(testConfig(), fragment, params)
	if err != nil {
		t.Fatalf("RenderFragment: %v", err)
	}

	return lowerRQ.SQL, fragmentRQ.SQL
}

func TestLowerLeafMatchesFragment(t *testing.T) {
	got, want := renderBothPaths(t, `up{job="api"}`)
	if got != want {
		t.Errorf("SQL differs:\nLower:    %s\nFragment: %s", got, want)
	}
}

func TestLowerScalarLiteralMatchesFragment(t *testing.T) {
	got, want := renderBothPaths(t, `42`)
	if got != want {
		t.Errorf("SQL differs:\nLower:    %s\nFragment: %s", got, want)
	}
}

func TestLowerScalarBinaryMatchesFragment(t *testing.T) {
	got, want := renderBothPaths(t, `up * 2`)
	if got != want {
		t.Errorf("SQL differs:\nLower:    %s\nFragment: %s", got, want)
	}
}

func TestLowerUnsupportedFallsBack(t *testing.T) {
	// Aggregation is out-of-scope for Lower in Phase 1 — it must return the
	// sentinel so the router falls back.
	expr, _ := parser.ParseExpr(`sum by (job) (up)`)
	root, _ := logical.ToLogical(expr)
	analysis := logical.Analyze(root)
	_, err := Lower(LoweringCtx{Config: testConfig(), Analysis: analysis}, root)
	if !IsUnsupportedByLower(err) {
		t.Errorf("expected errUnsupportedLowerNode, got %v", err)
	}
}
```

Run:
```bash
go test ./internal/promshim/native/renderer/ -run TestLower -v
```
Expected: first three PASS (byte-identical SQL); the fourth PASS (sentinel check).

- [ ] **Step 6: Compliance + bench**

```bash
scripts/run-compliance.sh
scripts/run-bench.sh --matrix
scripts/run-bench.sh --long-range 7d
```
Expected: 538/539; N/P ratios within ±3% of Task 0.

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "$(cat <<'EOF'
Add renderer.Lower dispatch for leaf/scalar/binary-trivial subset

Introduces Lower(ctx, logical.Node) with a type-switch dispatcher that
handles LeafExprPlan, ScalarLiteralPlan, and scalar-trivial BinaryPlan.
Unsupported kinds return errUnsupportedLowerNode; service.go falls back
hierarchically to Fragment dispatch for the whole query. A differential
test asserts byte-identical SQL for the supported kinds.

Compliance 538/539; bench within ±3%.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: Pass infrastructure + first pass + httpapi wiring

**Goal:** Create `internal/promshim/logical/opt/` with a `Pass` interface, a fixpoint `Optimize` runner (cap 8 iterations, re-Analyze between passes, pure — no in-place mutation), and one concrete pass: `constant-fold-unary-negation` (`-(-x) → x`). Wire `Optimize` into the httpapi pipeline between `ToLogical` and render.

**Files:**
- Create: `internal/promshim/logical/opt/pass.go`
- Create: `internal/promshim/logical/opt/pass_test.go`
- Create: `internal/promshim/logical/opt/constant_fold_unary.go`
- Create: `internal/promshim/logical/opt/constant_fold_unary_test.go`
- Modify: `internal/promshim/httpapi/router.go` and/or `internal/promshim/service.go` — call `opt.Optimize(root, opt.DefaultPasses)` between `logical.ToLogical` and render.

**Acceptance Criteria:**
- [ ] `opt.Pass` interface: `Name() string` and `Apply(logical.Node, *logical.Analysis) (logical.Node, bool, error)`.
- [ ] `opt.Optimize(root, passes)` runs passes in fixed order, re-analyzes between any rewriting pass, errors after 8 iterations without fixpoint.
- [ ] `opt.DefaultPasses` is an exported slice literal. No discovery.
- [ ] Passes never mutate in-place: if `changed == true`, the returned `Node` is a different pointer or a structurally-modified subtree.
- [ ] `constantFoldUnaryNegation{}`.`Apply` rewrites `(-(-x))` to `x` anywhere in the tree. Test asserts it fires on a corpus query.
- [ ] Compliance 538/539. Bench unchanged materially (trivial pass only).

**Verify:**
```bash
go test ./internal/promshim/logical/opt/... -v
scripts/run-compliance.sh
scripts/run-bench.sh --matrix
```
Expected: unit tests PASS; compliance 538/539.

**Steps:**

- [ ] **Step 1: Write a failing test for the pass contract**

Create `internal/promshim/logical/opt/pass_test.go`:

```go
package opt_test

import (
	"errors"
	"testing"

	"ch-observability/internal/promshim/logical"
	"ch-observability/internal/promshim/logical/opt"
	"github.com/prometheus/prometheus/promql/parser"
)

type noopPass struct{}

func (noopPass) Name() string { return "noop" }
func (noopPass) Apply(n logical.Node, _ *logical.Analysis) (logical.Node, bool, error) {
	return n, false, nil
}

type alwaysErrPass struct{}

func (alwaysErrPass) Name() string { return "alwaysErr" }
func (alwaysErrPass) Apply(_ logical.Node, _ *logical.Analysis) (logical.Node, bool, error) {
	return nil, false, errors.New("boom")
}

func mustLogical(t *testing.T, q string) logical.Node {
	t.Helper()
	expr, _ := parser.ParseExpr(q)
	node, err := logical.ToLogical(expr)
	if err != nil {
		t.Fatalf("ToLogical: %v", err)
	}
	return node
}

func TestOptimizeNoChangeReachesFixpoint(t *testing.T) {
	root := mustLogical(t, `up`)
	out, _, err := opt.Optimize(root, []opt.Pass{noopPass{}})
	if err != nil {
		t.Fatalf("Optimize: %v", err)
	}
	if out != root {
		t.Error("noop pass must return same root pointer")
	}
}

func TestOptimizePropagatesError(t *testing.T) {
	root := mustLogical(t, `up`)
	_, _, err := opt.Optimize(root, []opt.Pass{alwaysErrPass{}})
	if err == nil {
		t.Error("expected error propagation")
	}
}

func TestOptimizeCapsIterations(t *testing.T) {
	// An infinite-churn pass: marks changed=true every iteration. Optimize
	// must error out after the fixpoint cap.
	root := mustLogical(t, `up`)
	churn := churnPass{}
	_, _, err := opt.Optimize(root, []opt.Pass{churn})
	if err == nil {
		t.Error("expected fixpoint-cap error")
	}
}

type churnPass struct{}

func (churnPass) Name() string { return "churn" }
func (churnPass) Apply(n logical.Node, _ *logical.Analysis) (logical.Node, bool, error) {
	// Return a fresh LeafExprPlan so the runner sees "change" and re-runs.
	if leaf, ok := n.(*logical.LeafExprPlan); ok {
		return &logical.LeafExprPlan{Expr: leaf.Expr}, true, nil
	}
	return n, false, nil
}
```

Run:
```bash
go test ./internal/promshim/logical/opt/
```
Expected: FAIL — package doesn't exist.

- [ ] **Step 2: Implement `Pass` and `Optimize`**

Create `internal/promshim/logical/opt/pass.go`:

```go
package opt

import (
	"fmt"

	"ch-observability/internal/promshim/logical"
)

// Pass is a pure rewrite over the logical IR. Apply returns the
// (possibly-rewritten) root, whether it made any change, and any error.
// Implementations MUST NOT mutate the input node tree in place.
type Pass interface {
	Name() string
	Apply(root logical.Node, analysis *logical.Analysis) (logical.Node, bool, error)
}

const maxOptimizeIterations = 8

// Optimize runs passes in fixed order, re-analyzing between any rewriting
// pass, until a full iteration produces no change. It errors if the
// fixpoint cap is exceeded.
func Optimize(root logical.Node, passes []Pass) (logical.Node, *logical.Analysis, error) {
	if root == nil {
		return nil, nil, fmt.Errorf("opt: Optimize requires a non-nil root")
	}
	analysis := logical.Analyze(root)
	for iter := 0; iter < maxOptimizeIterations; iter++ {
		changed := false
		for _, p := range passes {
			next, didChange, err := p.Apply(root, analysis)
			if err != nil {
				return nil, nil, fmt.Errorf("opt: pass %q: %w", p.Name(), err)
			}
			if didChange {
				root = next
				analysis = logical.Analyze(root)
				changed = true
			}
		}
		if !changed {
			return root, analysis, nil
		}
	}
	return nil, nil, fmt.Errorf("opt: did not reach fixpoint in %d iterations", maxOptimizeIterations)
}

// DefaultPasses is the canonical ordered pass list applied to every query.
// Passes are appended in execution order.
var DefaultPasses = []Pass{
	constantFoldUnaryNegation{},
}
```

Run:
```bash
go test ./internal/promshim/logical/opt/ -run TestOptimize
```
Expected: `TestOptimizeNoChangeReachesFixpoint` PASS, `TestOptimizePropagatesError` PASS, `TestOptimizeCapsIterations` PASS.

- [ ] **Step 3: Implement the constant-fold-unary-negation pass**

Create `internal/promshim/logical/opt/constant_fold_unary.go`:

```go
package opt

import (
	"ch-observability/internal/promshim/logical"
	"github.com/prometheus/prometheus/promql/parser"
)

type constantFoldUnaryNegation struct{}

func (constantFoldUnaryNegation) Name() string { return "constant_fold_unary_negation" }

// Apply rewrites every (-(-x)) subtree to x. Pure — produces a new tree
// when it rewrites; returns the input unchanged otherwise.
func (constantFoldUnaryNegation) Apply(root logical.Node, _ *logical.Analysis) (logical.Node, bool, error) {
	newRoot, changed := foldDoubleNegations(root)
	return newRoot, changed, nil
}

func foldDoubleNegations(node logical.Node) (logical.Node, bool) {
	if node == nil {
		return nil, false
	}
	switch n := node.(type) {
	case *logical.UnaryPlan:
		if n.Op == parser.SUB {
			if inner, ok := n.Child.(*logical.UnaryPlan); ok && inner.Op == parser.SUB {
				// Fold: return the grandchild, continue walking into it.
				folded, _ := foldDoubleNegations(inner.Child)
				return folded, true
			}
		}
		child, childChanged := foldDoubleNegations(n.Child)
		if !childChanged {
			return n, false
		}
		clone := *n
		clone.Child = child
		return &clone, true
	case *logical.BinaryPlan:
		lhs, lhsChanged := foldDoubleNegations(n.LHS)
		rhs, rhsChanged := foldDoubleNegations(n.RHS)
		if !lhsChanged && !rhsChanged {
			return n, false
		}
		clone := *n
		clone.LHS = lhs
		clone.RHS = rhs
		return &clone, true
	// ...repeat for every Node subtype that has children: AggregationPlan,
	// RangeFunctionPlan, SubqueryPlan, LabelReplacePlan, LabelJoinPlan,
	// HistogramQuantilePlan, ClampTransform-ish kinds, etc.
	default:
		return node, false
	}
}
```

Write `constant_fold_unary_test.go`:

```go
package opt_test

import (
	"testing"

	"ch-observability/internal/promshim/logical"
	"ch-observability/internal/promshim/logical/opt"
)

func TestConstantFoldDoubleNegation(t *testing.T) {
	root := mustLogical(t, `- -up`)
	out, _, err := opt.Optimize(root, opt.DefaultPasses)
	if err != nil {
		t.Fatalf("Optimize: %v", err)
	}
	if _, ok := out.(*logical.LeafExprPlan); !ok {
		t.Fatalf("expected LeafExprPlan after fold, got %T", out)
	}
}

func TestConstantFoldSingleNegationUnchanged(t *testing.T) {
	root := mustLogical(t, `-up`)
	out, _, err := opt.Optimize(root, opt.DefaultPasses)
	if err != nil {
		t.Fatalf("Optimize: %v", err)
	}
	if _, ok := out.(*logical.UnaryPlan); !ok {
		t.Fatalf("expected UnaryPlan unchanged, got %T", out)
	}
}
```

Run:
```bash
go test ./internal/promshim/logical/opt/ -run TestConstantFold -v
```
Expected: both PASS.

- [ ] **Step 4: Wire Optimize into the httpapi pipeline**

Find the entry point that calls `logical.ToLogical` + render (Task 4 Step 4 added a call to `ToLogical` in `service.go`; or earlier in `router.go`). Insert:

```diff
-	root, err := logical.ToLogical(expr)
+	root, err := logical.ToLogical(expr)
 	if err != nil {
 		return nil, err
 	}
-	analysis := logical.Analyze(root)
+	root, analysis, err := opt.Optimize(root, opt.DefaultPasses)
+	if err != nil {
+		return nil, err
+	}
```

- [ ] **Step 5: Explain-endpoint sanity check**

```bash
curl -s 'http://localhost:29091/api/v1/query_explain?query=- -up' | jq .
```
(or whichever fixture is easier). Expected: explain plan shows the rewrite — the root in the rendered plan is a LeafExprPlan, not a UnaryPlan of UnaryPlan.

If the explain endpoint doesn't currently surface the rewrite, add an "applied passes" field to the explain response structure in `httpapi/response.go` and populate from `opt.Optimize`'s return. Keep the additional field opt-in (JSON omitempty) so existing explain consumers don't break.

- [ ] **Step 6: Compliance + bench**

```bash
scripts/run-compliance.sh
scripts/run-bench.sh --matrix
```
Expected: 538/539. Bench p50 shifts are noise-level on the double-negation fold.

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "$(cat <<'EOF'
Add logical/opt Pass infrastructure and constant-fold-unary-negation

Introduces the Pass interface, a fixpoint Optimize runner (cap 8
iterations, re-analyze between rewriting passes, pure), and one concrete
pass that folds -(-x) -> x. Wired into the httpapi pipeline between
ToLogical and render. DefaultPasses is the canonical ordered list.

Compliance 538/539; bench unchanged.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: Port remaining renderer surfaces; retire NativeFragment

**Goal:** Port every remaining PromQL shape from `RenderFragment` dispatch to `Lower` dispatch, one surface per commit. Each per-surface commit: replaces the lowering body with direct `emit/` + `storage/` calls, migrates that surface's tests to golden-SQL, and deletes the Fragment-producing `native/analysis_*.go` branch for that kind. When every surface is ported, delete `NativeFragment`, `native.BuildFragment`, `native.Analyze`, and the Fragment type tree.

**This task is a sequence of per-surface commits.** Estimate ~2 weeks total; each per-surface commit is green-green, and a half-ported state between commits is acceptable indefinitely.

**Files (per-surface pattern):**
- Create: `internal/promshim/native/renderer/lower_<kind>.go` (e.g. `lower_aggregation.go`)
- Create: `internal/promshim/native/renderer/testdata/<kind>_<case>.sql` (golden)
- Delete: `internal/promshim/native/renderer/<kind>.go` existing Fragment-keyed body (or shrink to a thin shim that errors)
- Delete: `internal/promshim/native/analysis_<kind>.go` Fragment branch
- Modify: `internal/promshim/native/renderer/lower.go` — add type-switch case

**Final-commit files:**
- Delete: `internal/promshim/native/types.go` Fragment types (`NativeFragment` + all `*Fragment` structs). Keep `RenderMode`, `OutputKind` if still used — move to `renderer/types.go`.
- Delete: `internal/promshim/native/builder.go`, `internal/promshim/native/analysis.go`, `native/analysis_*.go`, `native/lineage.go` (or move into `logical/`)
- Modify: every remaining caller of `native.BuildFragment` / `native.Analyze` to use `logical` equivalents only.

**Surface order (commit per surface):**

1. `SyntheticSeries` (already trivial)
2. `ScalarConvert`
3. `Subquery`
4. `SortTransform`
5. `LabelTransform` (label_replace, label_join)
6. `ClampTransform`
7. `RangeFunction` (rate, increase, delta, deriv, changes, quantile_over_time, *_over_time)
8. `Aggregation` (including aggregation-range-fused for perf parity)
9. `HistogramProjection`
10. `HistogramFunction` (histogram_quantile, histogram_fraction, histogram_quantiles)
11. `Absent` (absent, absent_over_time)
12. `InfoJoin`
13. `BinaryVectorJoin` (the full vector-vector matching path — largest surface)
14. `ValueTransform`
15. **Final:** delete `NativeFragment`, `BuildFragment`, `native.Analyze`, Fragment branches.

**Acceptance Criteria:**
- [ ] `grep -r "NativeFragment" internal/promshim/` returns zero matches in non-test, non-comment lines (comments/logs allowed temporarily).
- [ ] `grep -r "sqlb.RawLit" internal/promshim/native/renderer/` returns zero matches — every SQL literal is an `emit/` helper call.
- [ ] Every lowering function dispatches on `logical.Node` and reads enrichment from `logical.Analysis`.
- [ ] Golden-SQL tests cover every PromQL shape (`internal/promshim/native/renderer/testdata/*.sql`). `go test -run TestLower... -update` regenerates on change.
- [ ] Compliance 538/539.
- [ ] Bench: per-query p50 within ±5% of Task 0 baseline; no systematic regression across matrix categories.
- [ ] Long-range bench (7d/30d/1y) green.
- [ ] `native.Analysis`, `native.LoweringInfo`, `native.NativeFragment`, `native.BuildFragment` all deleted.

**Verify (final):**
```bash
go test ./internal/promshim/... && \
scripts/run-compliance.sh && \
scripts/run-bench.sh --matrix && \
scripts/run-bench.sh --long-range all && \
! grep -r "NativeFragment\|BuildFragment\|sqlb.RawLit" internal/promshim/native/renderer/ --include='*.go'
```
Expected: tests pass; compliance 538/539; grep check returns zero output.

**Per-surface pattern (use this for every surface commit):**

- [ ] **Step 1: Add golden-SQL test for this surface**

For surface `aggregation`, create `internal/promshim/native/renderer/lower_aggregation_test.go`:

```go
package renderer

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"ch-observability/internal/promshim/logical"
	"github.com/prometheus/prometheus/promql/parser"
)

var update = flag.Bool("update", false, "rewrite golden .sql files")

var aggregationCases = []struct {
	name  string
	query string
}{
	{"sum_by_job", `sum by (job) (up)`},
	{"sum_without_instance", `sum without (instance) (rate(http_requests_total[5m]))`},
	{"topk_5_job", `topk(5, sum by (job) (up))`},
	{"count_values_status", `count_values("status", http_requests_total)`},
}

func TestLowerAggregationGolden(t *testing.T) {
	for _, tc := range aggregationCases {
		t.Run(tc.name, func(t *testing.T) {
			expr, err := parser.ParseExpr(tc.query)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			root, err := logical.ToLogical(expr)
			if err != nil {
				t.Fatalf("ToLogical: %v", err)
			}
			analysis := logical.Analyze(root)
			ctx := LoweringCtx{Config: testConfig(), Analysis: analysis, Params: defaultInstantParams()}
			rq, err := Lower(ctx, root)
			if err != nil {
				t.Fatalf("Lower: %v", err)
			}
			goldenPath := filepath.Join("testdata", tc.name+".sql")
			if *update {
				if err := os.WriteFile(goldenPath, []byte(rq.SQL), 0o644); err != nil {
					t.Fatalf("write golden: %v", err)
				}
				return
			}
			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("read golden (run with -update to create): %v", err)
			}
			if string(want) != rq.SQL {
				t.Errorf("SQL differs from golden %s\nwant:\n%s\ngot:\n%s", goldenPath, want, rq.SQL)
			}
		})
	}
}
```

Run to create goldens:
```bash
mkdir -p internal/promshim/native/renderer/testdata
go test ./internal/promshim/native/renderer/ -run TestLowerAggregation -update
```

Review each generated `testdata/*.sql` — eyeball it for obvious wrongness against the current `RenderFragment` output. Do not commit a golden that doesn't look right.

- [ ] **Step 2: Write the `lowerAggregation` function**

Create `internal/promshim/native/renderer/lower_aggregation.go`. Implementation translates the current body of `renderAggregationFragment` to consume `*logical.AggregationPlan` + `logical.NodeInfo` directly. Every `sqlb.RawLit{V: "..."}` becomes an `emit.*` call (adding helpers to `emit/` as needed).

Example skeleton:

```go
package renderer

import (
	"ch-observability/internal/promshim/emit"
	"ch-observability/internal/promshim/logical"
	"ch-observability/internal/promshim/native/sqlb"
	"ch-observability/internal/promshim/storage"
)

func lowerAggregation(ctx LoweringCtx, n *logical.AggregationPlan) (RenderedQuery, error) {
	info := ctx.Analysis.Info[n]
	if info == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: aggregation missing analysis")
	}
	child, err := Lower(ctx, n.Child)
	if err != nil {
		return RenderedQuery{}, err
	}
	// Build aggregation SQL via storage builders + emit/ helpers.
	sql, params, err := storage.BuildRangeAggregationOverRowsSubquerySQL(
		child.SQL, child.QueryParams,
		n.Op, n.Grouping, n.Without, n.ParamNumber, n.ParamString,
	)
	if err != nil {
		return RenderedQuery{}, err
	}
	return RenderedQuery{SQL: sql, QueryParams: params}, nil
}
```

Register in `lower.go`:

```diff
 switch n := node.(type) {
 case *logical.LeafExprPlan:
 	return lowerLeaf(ctx, n)
+case *logical.AggregationPlan:
+	return lowerAggregation(ctx, n)
```

- [ ] **Step 3: Delete Fragment-producing analysis branch for this kind**

In `internal/promshim/native/analysis.go` (or the relevant `analysis_*.go`), remove the case:

```diff
-case *logicalpkg.AggregationPlan:
-	// ...Fragment-building body...
```

If other Fragment-consuming code still needs aggregation enrichment, it will get it via `logical.Analyze` now.

- [ ] **Step 4: Re-run tests**

```bash
go test ./internal/promshim/... -run TestLowerAggregation
go test ./internal/promshim/native/renderer/ -run TestLower
scripts/run-compliance.sh
scripts/run-bench.sh --matrix
```
Expected: goldens match; compliance 538/539; bench within ±5%.

- [ ] **Step 5: Commit this surface**

```bash
git add internal/promshim/native/renderer/lower_aggregation.go \
         internal/promshim/native/renderer/lower_aggregation_test.go \
         internal/promshim/native/renderer/testdata/*.sql \
         internal/promshim/native/renderer/lower.go \
         internal/promshim/native/analysis.go \
         internal/promshim/emit/emit.go
git commit -m "Port aggregation surface from Fragment to Lower dispatch

Renderer now consumes *logical.AggregationPlan directly via lowerAggregation.
Fragment branch for AggregationPlan removed from native.analysis. Golden-SQL
tests under native/renderer/testdata/. emit/ gains <helpers added this surface>.

Part of Phase 3 porting (spec: 2026-04-23-logical-ir-phase1-3-design.md).
Compliance 538/539; bench within ±5%.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

- [ ] **Step 6: Repeat Steps 1–5 for every remaining surface in the order listed above.**

Each surface's commit stands alone. Do not bundle multiple surfaces.

- [ ] **Final Step: Delete NativeFragment and Fragment infrastructure**

Once every case in the `switch` inside `native/analysis.go`'s `walk` has been removed:

```bash
rm internal/promshim/native/builder.go
rm internal/promshim/native/analysis.go internal/promshim/native/analysis_*.go
# Leave native/sqlb/, native/renderer/, native/optimizer.go (the Fragment-level
# optimizer retires with its last consumer — may or may not need deletion at
# this point; verify via go build).
```

Delete Fragment types from `native/types.go`: keep only `RenderMode` and `OutputKind` (or move them to `renderer/`).

Verify:
```bash
go build ./...
! grep -rn "NativeFragment" internal/promshim/ --include='*.go'
! grep -rn "sqlb.RawLit" internal/promshim/native/renderer/ --include='*.go'
```
Expected: build green; both greps return zero.

Final commit:
```bash
git add -A
git commit -m "$(cat <<'EOF'
Delete NativeFragment and retire Fragment-based lowering path

Every PromQL surface now lowers via renderer.Lower directly on logical.Node.
Fragment types, BuildFragment, native.Analyze, and the Fragment-level
optimizer are gone. emit/ owns all SQL phrasing.

Closes Phase 3 of the logical-IR plan.
Compliance 538/539; bench within ±5% per-query, no systematic regression.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Self-Review Results

**Spec coverage:**
- 9-point diagnosis points 1, 2, 3, 6, 7 → addressed by Tasks 1–6. Points 4, 5, 8, 9 explicitly out of scope per spec.
- Shape C (enriched PromQL-mirror IR) → Task 3 (`NodeInfo` side-map).
- Pipeline `ToLogical → Analyze → Lower → emit → SQL` → Task 2 (ToLogical) + Task 3 (Analyze) + Task 4 (Lower) + Task 6 (every surface on emit/).
- Pass infrastructure with fixpoint runner → Task 5.
- Coexistence period (Phase 1 hierarchical router) → Task 4 Step 4.
- Golden-SQL test migration → Task 6 per-surface pattern.
- Phase 1/2/3 gates → compliance 538/539 + bench ±3%/±5% thresholds assert at each task.
- Node enrichment contract (Schema two-valued, TimeDomain, GroupingKey, LabelLineage, DropsMetric, TimeRequirements; Predicates deferred) → Task 3 Steps 1 and 3.
- R3 (`Predicates` deferred) honored — no task references it.
- R5 escape (satellite relational node) — not needed preemptively; flagged in spec for when a concrete pass demands it.
- R6 (hierarchical split) → Task 4 Step 4 explicit.
- R7 (tier 1 lands early) — independent; no plan action.
- R8 (half-ported OK) → Task 6's per-surface commit policy.

**Placeholder scan:**
- No `TBD`, `TODO`, "fill in later" patterns remain.
- Every code step shows the actual code or a concrete skeleton to adapt.
- Each surface in Task 6 follows the explicit 6-step pattern with exact commands.

**Type consistency:**
- `logical.Node` (the interface) used consistently after Task 1.
- `logical.NodeInfo` + `logical.Analysis` (fields: `Root Node`, `Info map[Node]*NodeInfo`) — consistent across Tasks 3–6.
- `renderer.LoweringCtx` fields (`Config`, `Analysis`, `Params`) — consistent in Tasks 4, 5, 6.
- `opt.Pass` signature matches across Tasks 5 definitions and usages.
- `emit.*` helpers referenced generically — actual helper set grows per surface in Task 6; that's by design per spec.

**Acceptance gate check:**
- Task 4 asserts byte-identical SQL for the three ported kinds via the differential test — real check that Lower is a correct replacement, not just a plausible one.
- Task 6 final gate greps for the absence of `NativeFragment` and `sqlb.RawLit` — enforces the spec's success criteria mechanically.

No rewrites needed.
