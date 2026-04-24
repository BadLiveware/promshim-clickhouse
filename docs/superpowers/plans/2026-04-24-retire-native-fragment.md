# NativeFragment Retirement Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers-extended-cc:subagent-driven-development (recommended) or superpowers-extended-cc:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Retire `NativeFragment` by porting the four remaining Fragment-body renderers and the scalar-involving `BinaryPlan` branch to read directly from `logical.Node`, then delete the Fragment machinery.

**Architecture:** Three-phase surface-by-surface migration. Phase A ports the four body renderers (histogram projection → histogram function → range function → aggregation) to consume `logical.Node` directly, threading tag-narrowing through `RenderParams` instead of mutating a Fragment tree. Phase B ports the scalar-involving `BinaryPlan` branch of `lowerBinary`. Phase C deletes `NativeFragment`, `FragmentKind*`, `native.BuildFragment`, the Fragment-producing walk in `analysis.go`, and related shims. Each commit keeps `scripts/run-compliance.sh` at 538/539 and runs `scripts/run-bench.sh` where relevant.

**Tech Stack:** Go 1.22+, `prometheus/prometheus` parser, ClickHouse SQL builders (`internal/promshim/storage`), promshim test harness (`scripts/run-compliance.sh`, `scripts/run-bench.sh`, `scripts/run-harness.sh`).

**Spec reference:** `docs/superpowers/specs/2026-04-24-retire-native-fragment-design.md`

---

## File Structure

### New files (created during this plan)

- `internal/promshim/native/renderer/narrow_tags.go` — pure functions returning tag-narrowing decisions for histogram children (`decideHistogramChildNarrowing`, `childAggregationUsesOnlyLETags`). Replaces `narrowHistogramAggregationChildTags` mutator.
- `internal/promshim/native/renderer/histogram_logical.go` — direct-render histogram projection and histogram function bodies (`renderHistogramProjectionLogical`, `renderHistogramFunctionLogical`). Split out from `histogram.go` (646 lines) to keep porting churn isolated from the Fragment body until Phase C deletion.
- `internal/promshim/native/renderer/range_logical.go` — direct-render range function body (`renderRangeFunctionLogical`), fused-helper logical ports (`canFuseRangeAggregationLogical`, `tryRenderFusedRangeAggregationLogical`). Split out from `range.go` (938 lines) for the same reason.
- `internal/promshim/native/renderer/aggregation_logical.go` — direct-render aggregation body (`renderAggregationLogical`), and source-dispatch logical helper (`renderAggregationSourceLogical`).
- `internal/promshim/native/renderer/lower_binary_scalar.go` — scalar-involving `BinaryPlan` direct-render (`lowerBinaryScalarInvolving`).

### Modified files

- `internal/promshim/native/renderer/renderer.go:14-23` — add `RequireFullTags bool` and `RequiredTagLabels []string` fields to `RenderParams`.
- `internal/promshim/native/renderer/lower_histogram_projection.go` — flip to `renderHistogramProjectionLogical`.
- `internal/promshim/native/renderer/lower_histogram_function.go` — flip to `renderHistogramFunctionLogical`.
- `internal/promshim/native/renderer/lower_range_function.go` — flip to `renderRangeFunctionLogical`.
- `internal/promshim/native/renderer/lower_aggregation.go` — flip to `renderAggregationLogical`.
- `internal/promshim/native/renderer/lower.go:146-155` — replace scalar-involving branch with `lowerBinaryScalarInvolving`.
- `internal/promshim/native/renderer/source.go` — deleted in Phase C (identity-wrap logic moves inline where needed).
- `internal/promshim/native/renderer/histogram.go` — deleted in Phase C.
- `internal/promshim/native/renderer/range.go` — deleted in Phase C.
- `internal/promshim/native/renderer/join.go` — `renderAggregationFragment` deleted in Phase C; `renderValueTransformFromSource` (pure helper, no Fragment dependency) survives.
- `internal/promshim/native/renderer/aggregation_range_fused.go` — deleted in Phase C.
- `internal/promshim/native/renderer/renderer.go` — `RenderFragment` / `renderFragment` deleted in Phase C.
- `internal/promshim/native/builder.go` — deleted in Phase C.
- `internal/promshim/native/types.go` — delete `NativeFragment`, `FragmentKind*`; rename `FragmentInfo` → `NodeInfo` and drop its `Fragment` field.
- `internal/promshim/native/analysis.go` — delete Fragment-producing walk (including UnaryPlan branch at lines 62-103); keep only `LabelLineage`, `TimeRequirements`, `SourcePromQL` computation.
- `internal/promshim/native/selector.go` — delete `RequireFullTags` / `RequiredTagLabels` fields on `SelectorSource`.
- `internal/promshim/local/native_subtree.go:140-165` — remove Fragment-dispatch fallback branch.
- `harness/bench/baseline.json` — refreshed at end of Phase A and end of Phase C.

---

## Conventions for this plan

### Imports (every new renderer file)

```go
package renderer

import (
	"fmt"

	logicalpkg "ch-observability/internal/promshim/logical"
	"ch-observability/internal/promshim/native"
	"ch-observability/internal/promshim/storage"
)
```

### Porting template (every body-renderer port)

Each of Phase A's four tasks follows the same template:

1. Create `renderXxxLogical(cfg storage.QueryConfig, n *logicalpkg.XxxPlan, params RenderParams) (renderedFragment, error)` in a new `<body>_logical.go` file. Mirror the corresponding `renderXxxFragment` structure but read from the logical node.
2. Within the logical renderer, build selectors via `native.BuildSelectorSource(leaf.Expr)`; use `nativeSelectorToStorage` (exists in `lower_leaf.go:91`) to convert to `storage.SelectorSource`. For tag-narrowing, set `storageSel.RequireFullTags` and `storageSel.RequiredTagLabels` from `params.RequireFullTags` / `params.RequiredTagLabels`.
3. For child renders, recurse via `Lower(childCtx, childNode)` with a fresh `RenderParams` that explicitly sets the narrowing fields (if applicable) and required bounds.
4. Compute required bounds via `logicalRangeRequiredBoundsForChild` (not the Fragment variant).
5. In the corresponding `lower_xxx.go`, flip the lowerer to call `renderXxxLogical(ctx.Config, n, ctx.Params)` then `finalizeRenderedFragment`. Remove the `native.BuildFragment` call.
6. Keep the old `renderXxxFragment` alive — it is still the Fragment-path fallback for nested renders that go through `RenderFragment`. It dies in Phase C.
7. Verify per task's verify block.

### Commit messages

All commits use conventional-commit style with a body explaining *why*:

```
port <body>: direct-render from logical.Node

<2-3 sentences explaining what this unlocks and the scope. Mention
tag-narrowing propagation via RenderParams where relevant.>
```

---

## Task 7: Add RenderParams narrowing fields and pure narrowing decider

**Goal:** Add `RequireFullTags` and `RequiredTagLabels` to `RenderParams` and extract the histogram-child narrowing decision into a pure function. No behavior change — existing Fragment-path call sites still mutate `SelectorSource` directly; new logical renderers will read from `RenderParams`.

**Files:**
- Modify: `internal/promshim/native/renderer/renderer.go:14-23` (add fields to `RenderParams`)
- Create: `internal/promshim/native/renderer/narrow_tags.go`
- Test: `internal/promshim/native/renderer/narrow_tags_test.go`

**Acceptance Criteria:**
- [ ] `RenderParams` has `RequireFullTags bool` and `RequiredTagLabels []string` fields.
- [ ] `decideHistogramChildNarrowing(child *logicalpkg.AggregationPlan) (requireFullTags bool, requiredLabels []string)` returns `(false, ["le", ...other-grouping])` when the child is a `sum by (...)` style aggregation with `Without=false`; returns `(true, nil)` otherwise.
- [ ] `childAggregationUsesOnlyLETags(child *logicalpkg.AggregationPlan) bool` returns true for aggregation plans with `Without=false` and grouping `== ["le"]`.
- [ ] New renderer tests pass; existing renderer and promshim tests unchanged.
- [ ] No call site changes yet — this is prep.

**Verify:** `go test ./internal/promshim/native/renderer/...` → PASS (all existing + new tests)

**Steps:**

- [ ] **Step 1: Add narrowing fields to `RenderParams`**

Edit `internal/promshim/native/renderer/renderer.go:14-23`:

```go
type RenderParams struct {
	Mode                native.RenderMode
	EvaluationTimeMS    int64
	StartMS             int64
	EndMS               int64
	StepMS              int64
	RequiredStartMS     int64
	RequiredEndMS       int64
	ResolveSourcePromQL func(parser.Expr) (string, error)

	// RequireFullTags and RequiredTagLabels propagate tag-narrowing from a
	// parent renderer (typically a histogram function or projection) down to
	// its descendant selectors. When the parent does not narrow, leave
	// RequireFullTags=true (the default) and RequiredTagLabels=nil. The
	// selector-render helpers merge these with any SelectorSource-level
	// narrowing set by the Fragment path.
	RequireFullTags   bool
	RequiredTagLabels []string
}
```

Note: default value of `RequireFullTags` is `false` in Go, which is wrong for "no narrowing." We need the default to mean "full tags required." The simplest fix is to invert the semantics and keep the name — the field as written says "true = require full tags." But the default zero-value is `false`, which would mean "narrowing allowed." To keep the zero-value safe, we flip to `NarrowTagsAllowed bool` OR we document that every constructor must set `RequireFullTags: true`. Go with the constructor-convention route to match the existing `SelectorSource.RequireFullTags` naming. Add a helper.

Actually, inspect `native.SelectorSource` to confirm the naming there first:

```bash
grep -n 'RequireFullTags' internal/promshim/native/selector.go
```

The field there is `RequireFullTags bool` with default-zero-false meaning "no full-tags requirement." Match that convention: `RequireFullTags=true` means "parent has *explicitly* declared that the selector must keep all tags." Default `false` means "no explicit requirement from parent; selector decides." This is safe because the selector's own `RequireFullTags` field is still set by `native.BuildSelectorSource` and ORs with the param. Document the merge rule in the helper (Step 3 below).

- [ ] **Step 2: Write the narrowing-decider test (RED)**

Create `internal/promshim/native/renderer/narrow_tags_test.go`:

```go
package renderer

import (
	"testing"

	logicalpkg "ch-observability/internal/promshim/logical"
	"github.com/prometheus/prometheus/promql/parser"
)

func TestDecideHistogramChildNarrowing_sumByLe(t *testing.T) {
	child := &logicalpkg.AggregationPlan{
		Op:       parser.SUM,
		Grouping: []string{"le"},
		Without:  false,
	}
	requireFull, labels := decideHistogramChildNarrowing(child)
	if requireFull {
		t.Fatalf("expected requireFullTags=false for sum by (le), got true")
	}
	if len(labels) != 1 || labels[0] != "le" {
		t.Fatalf("expected labels=[le], got %v", labels)
	}
}

func TestDecideHistogramChildNarrowing_sumByLeAndJob(t *testing.T) {
	child := &logicalpkg.AggregationPlan{
		Op:       parser.SUM,
		Grouping: []string{"le", "job"},
		Without:  false,
	}
	requireFull, labels := decideHistogramChildNarrowing(child)
	if requireFull {
		t.Fatalf("expected requireFullTags=false for sum by (le,job), got true")
	}
	if len(labels) != 2 || labels[0] != "le" || labels[1] != "job" {
		t.Fatalf("expected labels=[le,job], got %v", labels)
	}
}

func TestDecideHistogramChildNarrowing_sumWithout(t *testing.T) {
	child := &logicalpkg.AggregationPlan{
		Op:       parser.SUM,
		Grouping: []string{"instance"},
		Without:  true,
	}
	requireFull, labels := decideHistogramChildNarrowing(child)
	if !requireFull {
		t.Fatalf("expected requireFullTags=true for sum without (instance), got false")
	}
	if labels != nil {
		t.Fatalf("expected labels=nil, got %v", labels)
	}
}

func TestDecideHistogramChildNarrowing_nilChild(t *testing.T) {
	requireFull, labels := decideHistogramChildNarrowing(nil)
	if !requireFull {
		t.Fatalf("expected requireFullTags=true for nil child")
	}
	if labels != nil {
		t.Fatalf("expected labels=nil, got %v", labels)
	}
}

func TestChildAggregationUsesOnlyLETags(t *testing.T) {
	cases := []struct {
		name     string
		plan     *logicalpkg.AggregationPlan
		expected bool
	}{
		{"sum by (le)", &logicalpkg.AggregationPlan{Op: parser.SUM, Grouping: []string{"le"}, Without: false}, true},
		{"sum by (le, job)", &logicalpkg.AggregationPlan{Op: parser.SUM, Grouping: []string{"le", "job"}, Without: false}, false},
		{"sum without (le)", &logicalpkg.AggregationPlan{Op: parser.SUM, Grouping: []string{"le"}, Without: true}, false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := childAggregationUsesOnlyLETags(tc.plan); got != tc.expected {
				t.Fatalf("got %v, want %v", got, tc.expected)
			}
		})
	}
}
```

Run: `go test ./internal/promshim/native/renderer/ -run TestDecideHistogramChildNarrowing -v`
Expected: FAIL with "undefined: decideHistogramChildNarrowing" / "undefined: childAggregationUsesOnlyLETags"

- [ ] **Step 3: Implement the deciders (GREEN)**

Create `internal/promshim/native/renderer/narrow_tags.go`:

```go
package renderer

import (
	logicalpkg "ch-observability/internal/promshim/logical"
)

// decideHistogramChildNarrowing returns the tag-narrowing decision for a
// histogram-function or histogram-projection child. If the child is a
// grouping aggregation (`sum by (...)` shape), the parent only needs the
// grouping labels from the underlying selector — the returned
// requiredLabels are the grouping labels (always including "le" for
// bucket histograms), and requireFullTags is false. Otherwise, the
// parent cannot narrow and requireFullTags is true.
//
// The returned requiredLabels slice is safe to hand to RenderParams
// (caller owns it).
func decideHistogramChildNarrowing(child *logicalpkg.AggregationPlan) (requireFullTags bool, requiredLabels []string) {
	if child == nil || child.Without || len(child.Grouping) == 0 {
		return true, nil
	}
	labels := make([]string, len(child.Grouping))
	copy(labels, child.Grouping)
	return false, labels
}

// childAggregationUsesOnlyLETags reports whether the child aggregation
// groups by exactly ["le"] with Without=false. The histogram renderer
// uses this to pick the "identity-tag rows" shortcut path that skips
// the full aggregation rewrite.
func childAggregationUsesOnlyLETags(child *logicalpkg.AggregationPlan) bool {
	if child == nil || child.Without {
		return false
	}
	return len(child.Grouping) == 1 && child.Grouping[0] == "le"
}
```

- [ ] **Step 4: Run tests (GREEN)**

Run: `go test ./internal/promshim/native/renderer/ -run 'TestDecideHistogramChildNarrowing|TestChildAggregationUsesOnlyLETags' -v`
Expected: PASS (all 6 subtests)

- [ ] **Step 5: Run full renderer suite — no regression**

Run: `go test ./internal/promshim/native/renderer/...`
Expected: PASS (no existing test broken; new fields on `RenderParams` are zero-value safe)

- [ ] **Step 6: Commit**

```bash
git add internal/promshim/native/renderer/renderer.go \
        internal/promshim/native/renderer/narrow_tags.go \
        internal/promshim/native/renderer/narrow_tags_test.go
git commit -m "$(cat <<'EOF'
prep: thread tag-narrowing through RenderParams

Add RequireFullTags and RequiredTagLabels to RenderParams and extract
the narrowing decision into a pure function. No behavior change — the
Fragment path still reads SelectorSource fields, but the new fields
are now available for direct-render bodies to propagate narrowing
parent→child without mutating a Fragment tree.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 8: Port histogram projection body to logical (Phase A1)

**Goal:** Direct-render `HistogramProjectionPlan` without building a Fragment. Locks the porting conventions that A2–A4 follow.

**Files:**
- Create: `internal/promshim/native/renderer/histogram_logical.go`
- Modify: `internal/promshim/native/renderer/lower_histogram_projection.go` (flip dispatch)
- Test: `internal/promshim/native/renderer/histogram_logical_test.go`

**Acceptance Criteria:**
- [ ] `renderHistogramProjectionLogical(cfg, n *logicalpkg.HistogramProjectionPlan, params RenderParams) (renderedFragment, error)` exists and produces byte-identical SQL vs. `renderHistogramProjectionFragment` for every query in the existing renderer test corpus.
- [ ] `lowerHistogramProjection` no longer calls `native.BuildFragment`.
- [ ] Tag-narrowing threads via `RenderParams` when the projection's child is a grouping aggregation.
- [ ] All five projection functions (`histogram_count`, `histogram_sum`, `histogram_avg`, `histogram_stddev`, `histogram_stdvar`) render correctly.

**Verify:**
- `go test ./internal/promshim/native/renderer/...` → PASS
- `scripts/run-compliance.sh` → 538/539 (allowlisted `topk-tie-break-ordering` only)
- `grep -c 'histogram_count\|histogram_sum\|histogram_avg\|histogram_stddev\|histogram_stdvar' harness/compliance/compliance-report.json` shows at least the existing count of successful histogram-projection cases.

**Steps:**

- [ ] **Step 1: Read the Fragment body**

Read `internal/promshim/native/renderer/histogram.go:17-227` (the `renderHistogramProjectionFragment` function and its helpers up to where `renderHistogramFunctionFragment` starts). Identify:
- Which child fragment kinds it handles (should be `FragmentKindLeafSource`, `FragmentKindAggregation` with a grouping shape).
- Where `narrowHistogramAggregationChildTags` is called (line 400 today — note this is inside `renderHistogramFunctionFragment`, not projection; projection has its own narrowing via `fragment.HistogramProjection.Source` selector fields).
- Which `storage.Build*` helpers get called (e.g., `renderClassicHistogramGroupsQuery`, `buildHistogramIdentityTagAggregationRowsSQL`).

- [ ] **Step 2: Write the byte-equality test harness (RED)**

Create `internal/promshim/native/renderer/histogram_logical_test.go`:

```go
package renderer

import (
	"context"
	"testing"

	logicalpkg "ch-observability/internal/promshim/logical"
	"ch-observability/internal/promshim/native"
	"ch-observability/internal/promshim/storage"
	"github.com/prometheus/prometheus/promql/parser"
)

// TestHistogramProjectionLogicalMatchesFragment renders a representative
// set of histogram_projection queries via both the Fragment path and
// the new logical path and asserts byte-identical SQL + params.
func TestHistogramProjectionLogicalMatchesFragment(t *testing.T) {
	queries := []string{
		`histogram_count(foo_bucket)`,
		`histogram_sum(foo_bucket)`,
		`histogram_avg(foo_bucket)`,
		`histogram_stddev(foo_bucket)`,
		`histogram_stdvar(foo_bucket)`,
		`histogram_count(sum by (le) (foo_bucket))`,
		`histogram_sum(sum by (le, job) (foo_bucket))`,
	}
	cfg := storage.QueryConfig{Database: "default", Table: "ts"}
	now := int64(1_700_000_000_000)
	for _, q := range queries {
		t.Run(q, func(t *testing.T) {
			expr, err := parser.ParseExpr(q)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			logical, err := logicalpkg.ToLogical(expr)
			if err != nil {
				t.Fatalf("ToLogical: %v", err)
			}
			analysis, err := logicalpkg.Analyze(logical)
			if err != nil {
				t.Fatalf("Analyze: %v", err)
			}
			proj, ok := logical.(*logicalpkg.HistogramProjectionPlan)
			if !ok {
				t.Fatalf("expected HistogramProjectionPlan, got %T", logical)
			}

			params := RenderParams{
				Mode:             native.RenderModeInstant,
				EvaluationTimeMS: now,
				RequiredStartMS:  now - 300_000,
				RequiredEndMS:    now,
			}

			fragment, err := native.BuildFragment(logical, nil)
			if err != nil {
				t.Fatalf("BuildFragment: %v", err)
			}
			wantSQL, wantParams, err := func() (string, map[string]string, error) {
				rq, err := RenderFragment(cfg, fragment, params)
				return rq.SQL, rq.QueryParams, err
			}()
			if err != nil {
				t.Fatalf("Fragment render: %v", err)
			}

			ctx := LoweringCtx{Config: cfg, Analysis: analysis, Params: params}
			got, err := lowerHistogramProjection(ctx, proj)
			if err != nil {
				t.Fatalf("lowerHistogramProjection: %v", err)
			}
			if got.SQL != wantSQL {
				t.Fatalf("SQL mismatch\nwant:\n%s\n\ngot:\n%s", wantSQL, got.SQL)
			}
			if !queryParamsEqual(got.QueryParams, wantParams) {
				t.Fatalf("query params mismatch\nwant: %v\ngot:  %v", wantParams, got.QueryParams)
			}
			_ = context.TODO
		})
	}
}

func queryParamsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
```

Run: `go test ./internal/promshim/native/renderer/ -run TestHistogramProjectionLogicalMatchesFragment -v`
Expected: initially PASSES because `lowerHistogramProjection` still taps `BuildFragment`. This test is the safety net for the next steps — once the lowerer flips, the test must still pass.

- [ ] **Step 3: Write `renderHistogramProjectionLogical` (structure)**

Create `internal/promshim/native/renderer/histogram_logical.go`:

```go
package renderer

import (
	"fmt"

	logicalpkg "ch-observability/internal/promshim/logical"
	"ch-observability/internal/promshim/native"
	"ch-observability/internal/promshim/storage"
)

// renderHistogramProjectionLogical renders a HistogramProjectionPlan
// directly from the logical tree without constructing a NativeFragment.
// It mirrors renderHistogramProjectionFragment structurally, producing
// byte-identical SQL.
//
// Supported functions (carried on n.Function): histogram_count,
// histogram_sum, histogram_avg, histogram_stddev, histogram_stdvar.
//
// Tag-narrowing: when n.Child is an AggregationPlan, the child's
// grouping labels determine the required tag set. The narrowing is
// threaded into the child render via RenderParams.RequiredTagLabels /
// RenderParams.RequireFullTags so descendant selectors honor it.
func renderHistogramProjectionLogical(cfg storage.QueryConfig, n *logicalpkg.HistogramProjectionPlan, params RenderParams) (renderedFragment, error) {
	if n == nil {
		return renderedFragment{}, fmt.Errorf("renderer: renderHistogramProjectionLogical called with nil")
	}

	// Compute narrowing from the logical child if it is an aggregation.
	childParams := params
	if aggChild, ok := n.Child.(*logicalpkg.AggregationPlan); ok {
		requireFull, labels := decideHistogramChildNarrowing(aggChild)
		childParams.RequireFullTags = requireFull
		childParams.RequiredTagLabels = labels
	}

	// Port of renderHistogramProjectionFragment body. The Fragment path
	// dispatched on fragment.HistogramProjection.Source.Kind — we
	// instead dispatch on the logical child kind. Every branch below
	// mirrors a Fragment branch.
	//
	// NOTE: this block is the verbatim structural translation of lines
	// 17-227 of histogram.go. Do not refactor during the port — byte
	// equality is the acceptance criterion. Refactors are a Phase C
	// concern if at all.
	switch child := n.Child.(type) {
	case *logicalpkg.LeafExprPlan:
		return renderHistogramProjectionFromLeaf(cfg, n, child, childParams)
	case *logicalpkg.AggregationPlan:
		return renderHistogramProjectionFromAggregation(cfg, n, child, childParams)
	default:
		return renderedFragment{}, fmt.Errorf("renderer: histogram_projection child kind %T not supported by direct-render", n.Child)
	}
}
```

- [ ] **Step 4: Implement `renderHistogramProjectionFromLeaf` and `renderHistogramProjectionFromAggregation`**

Within `histogram_logical.go`, port the leaf and aggregation branches of `renderHistogramProjectionFragment`. Mirror each `storage.Build*` call and each `sqlb.Select{...}` construction exactly. For the leaf branch:

```go
func renderHistogramProjectionFromLeaf(cfg storage.QueryConfig, n *logicalpkg.HistogramProjectionPlan, leaf *logicalpkg.LeafExprPlan, params RenderParams) (renderedFragment, error) {
	selector, err := native.BuildSelectorSource(leaf.Expr)
	if err != nil {
		return renderedFragment{}, fmt.Errorf("renderer: histogram_projection leaf selector analysis failed: %w", err)
	}
	if selector == nil {
		return renderedFragment{}, fmt.Errorf("renderer: histogram_projection leaf must be a selector, got delegated PromQL")
	}
	storageSel := nativeSelectorToStorage(selector)
	if !params.RequireFullTags && len(params.RequiredTagLabels) > 0 {
		storageSel.RequireFullTags = false
		storageSel.RequiredTagLabels = append([]string(nil), params.RequiredTagLabels...)
	}
	// Render the underlying bucket rows, then wrap with the projection function.
	// Port the body of renderHistogramProjectionFragment's leaf branch here —
	// see histogram.go:17-120 for the Fragment-side structure. Mirror every
	// storage helper call with the same arguments.
	return buildHistogramProjectionFromLeafSelector(cfg, n.Function, storageSel, params)
}

func renderHistogramProjectionFromAggregation(cfg storage.QueryConfig, n *logicalpkg.HistogramProjectionPlan, child *logicalpkg.AggregationPlan, params RenderParams) (renderedFragment, error) {
	// Port the body of the aggregation branch in renderHistogramProjectionFragment
	// (histogram.go:120-227). Use the ported canFuseRangeAggregationLogical/
	// tryRenderFusedRangeAggregationLogical helpers if applicable; otherwise
	// recurse via Lower on the aggregation child for its SQL and wrap.
	return buildHistogramProjectionFromAggregation(cfg, n.Function, child, params)
}
```

The two `buildHistogramProjectionFrom*` helpers are straight translations of the Fragment branches. Read the existing code, copy the structure, replace every `fragment.HistogramProjection.Source.<field>` reference with the corresponding logical-side value (`child.Grouping`, `child.Without`, `leaf.Expr`, `child.Source`, etc.), and replace every recursive `RenderFragment(cfg, subFragment, params)` with `Lower(childCtx, childNode)` + `finalizeRenderedFragment`.

- [ ] **Step 5: Flip `lowerHistogramProjection`**

Edit `internal/promshim/native/renderer/lower_histogram_projection.go`:

```go
func lowerHistogramProjection(ctx LoweringCtx, n *logicalpkg.HistogramProjectionPlan) (RenderedQuery, error) {
	if n == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: lowerHistogramProjection called with nil")
	}
	if ctx.Analysis == nil || ctx.Analysis.InfoFor(n) == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: histogram projection missing logical analysis")
	}
	rendered, err := renderHistogramProjectionLogical(ctx.Config, n, ctx.Params)
	if err != nil {
		return RenderedQuery{}, err
	}
	return finalizeRenderedFragment(rendered)
}
```

Remove the `native.BuildFragment` call and the `FragmentKindHistogramProjection` validation — both unneeded.

- [ ] **Step 6: Run the byte-equality test (should still PASS)**

Run: `go test ./internal/promshim/native/renderer/ -run TestHistogramProjectionLogicalMatchesFragment -v`
Expected: PASS for every query in the corpus. If any query diverges, fix the direct-render path; Fragment is ground truth.

- [ ] **Step 7: Run full renderer tests + promshim tests + compliance**

Run:
```bash
go test ./internal/promshim/native/renderer/...
go test ./internal/promshim/...
scripts/run-compliance.sh
```
Expected: all PASS; compliance 538/539.

- [ ] **Step 8: Commit**

```bash
git add internal/promshim/native/renderer/histogram_logical.go \
        internal/promshim/native/renderer/histogram_logical_test.go \
        internal/promshim/native/renderer/lower_histogram_projection.go
git commit -m "$(cat <<'EOF'
port histogram_projection: direct-render from logical.Node

lowerHistogramProjection now calls renderHistogramProjectionLogical,
which reads the HistogramProjectionPlan directly and threads
tag-narrowing through RenderParams rather than mutating a Fragment
tree. Byte-equal SQL vs. the Fragment path is enforced by a
side-by-side test over the compliance corpus shapes.

renderHistogramProjectionFragment stays alive for now — it is still
reachable from nested renders via RenderFragment. It dies in the
Phase C cleanup.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 9: Port histogram function body to logical (Phase A2)

**Goal:** Direct-render `HistogramQuantilePlan`, `HistogramFractionPlan`, `HistogramQuantilesPlan`. Reuse the narrowing + child-render helpers from Task 8.

**Files:**
- Modify: `internal/promshim/native/renderer/histogram_logical.go` (add `renderHistogramFunctionLogical` and function-specific helpers)
- Modify: `internal/promshim/native/renderer/lower_histogram_function.go` (flip dispatch)
- Test: `internal/promshim/native/renderer/histogram_logical_test.go` (add function corpus)

**Acceptance Criteria:**
- [ ] `renderHistogramFunctionLogical(cfg, n logicalpkg.Node, params RenderParams) (renderedFragment, error)` handles all three plan kinds.
- [ ] `lowerHistogramFunction` no longer calls `native.BuildFragment`.
- [ ] Byte-equality test covers `histogram_quantile(0.9, foo_bucket)`, `histogram_quantile(0.5, sum by (le) (rate(foo_bucket[5m])))`, `histogram_fraction(0.1, 0.2, foo_bucket)`, `histogram_quantiles("q1", 0.5, "q2", 0.9, foo_bucket)`.
- [ ] Tag-narrowing for `le` threads correctly through any aggregation child.

**Verify:**
- `go test ./internal/promshim/native/renderer/...` → PASS
- `scripts/run-compliance.sh` → 538/539

**Steps:**

- [ ] **Step 1: Read the Fragment body**

Read `internal/promshim/native/renderer/histogram.go:229-555` (the `renderHistogramFunctionFragment` function, including where it calls `narrowHistogramAggregationChildTags` at line 400 and `tryRenderHistogramChildRowsSQL` at line 598).

Key observation: the histogram-function body dispatches on `fragment.HistogramFunction.Function` (quantile / fraction / quantiles) and on the child fragment shape (leaf vs. aggregation, with or without a nested range). The port must preserve every branch.

- [ ] **Step 2: Write the byte-equality corpus**

Append to `internal/promshim/native/renderer/histogram_logical_test.go`:

```go
func TestHistogramFunctionLogicalMatchesFragment(t *testing.T) {
	queries := []string{
		`histogram_quantile(0.9, foo_bucket)`,
		`histogram_quantile(0.5, sum by (le) (rate(foo_bucket[5m])))`,
		`histogram_quantile(0.99, sum by (le, job) (rate(foo_bucket[1m])))`,
		`histogram_fraction(0.1, 0.2, foo_bucket)`,
		`histogram_fraction(0.1, 0.2, sum by (le) (rate(foo_bucket[5m])))`,
		`histogram_quantiles("q50", 0.5, "q90", 0.9, foo_bucket)`,
	}
	cfg := storage.QueryConfig{Database: "default", Table: "ts"}
	now := int64(1_700_000_000_000)
	for _, q := range queries {
		t.Run(q, func(t *testing.T) {
			expr, err := parser.ParseExpr(q)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			logical, err := logicalpkg.ToLogical(expr)
			if err != nil {
				t.Fatalf("ToLogical: %v", err)
			}
			analysis, err := logicalpkg.Analyze(logical)
			if err != nil {
				t.Fatalf("Analyze: %v", err)
			}

			params := RenderParams{
				Mode:             native.RenderModeInstant,
				EvaluationTimeMS: now,
				RequiredStartMS:  now - 600_000,
				RequiredEndMS:    now,
			}

			fragment, err := native.BuildFragment(logical, nil)
			if err != nil {
				t.Fatalf("BuildFragment: %v", err)
			}
			wantRQ, err := RenderFragment(cfg, fragment, params)
			if err != nil {
				t.Fatalf("Fragment render: %v", err)
			}

			ctx := LoweringCtx{Config: cfg, Analysis: analysis, Params: params}
			got, err := Lower(ctx, logical)
			if err != nil {
				t.Fatalf("Lower: %v", err)
			}
			if got.SQL != wantRQ.SQL {
				t.Fatalf("SQL mismatch for %q\nwant:\n%s\n\ngot:\n%s", q, wantRQ.SQL, got.SQL)
			}
			if !queryParamsEqual(got.QueryParams, wantRQ.QueryParams) {
				t.Fatalf("query params mismatch for %q\nwant: %v\ngot:  %v", q, wantRQ.QueryParams, got.QueryParams)
			}
		})
	}
}
```

Run: `go test ./internal/promshim/native/renderer/ -run TestHistogramFunctionLogicalMatchesFragment -v`
Expected: initially PASS (both paths go through Fragment). Same role as Task 8 Step 2 — safety net for the port.

- [ ] **Step 3: Add `renderHistogramFunctionLogical` to `histogram_logical.go`**

Structure mirrors Task 8's projection port:

```go
// renderHistogramFunctionLogical renders HistogramQuantilePlan /
// HistogramFractionPlan / HistogramQuantilesPlan directly from the
// logical tree. Tag-narrowing is threaded into child renders via
// RenderParams when the child is a grouping aggregation over buckets.
func renderHistogramFunctionLogical(cfg storage.QueryConfig, n logicalpkg.Node, params RenderParams) (renderedFragment, error) {
	if n == nil {
		return renderedFragment{}, fmt.Errorf("renderer: renderHistogramFunctionLogical called with nil")
	}
	switch hf := n.(type) {
	case *logicalpkg.HistogramQuantilePlan:
		return renderHistogramQuantileLogical(cfg, hf, params)
	case *logicalpkg.HistogramFractionPlan:
		return renderHistogramFractionLogical(cfg, hf, params)
	case *logicalpkg.HistogramQuantilesPlan:
		return renderHistogramQuantilesLogical(cfg, hf, params)
	default:
		return renderedFragment{}, fmt.Errorf("renderer: renderHistogramFunctionLogical unsupported kind %T", n)
	}
}
```

Then port the three per-function branches. Each one:
1. Computes narrowing from the child (using `decideHistogramChildNarrowing`) and sets `childParams`.
2. Dispatches on child kind (LeafExprPlan / AggregationPlan / RangeFunctionPlan / SubqueryPlan — copy exactly what the Fragment branch accepts).
3. For each child kind, builds the child-rows SQL via the appropriate `storage.Build*` helper or recurses via `Lower`.
4. Wraps with the histogram-function-specific computation (quantile, fraction, quantiles SQL shape).

Refer to `histogram.go:229-555` line by line for exact SQL structure. **Do not refactor during the port — every `sqlb.Select{...}` stays identical.**

- [ ] **Step 4: Flip `lowerHistogramFunction`**

Edit `internal/promshim/native/renderer/lower_histogram_function.go`:

```go
func lowerHistogramFunction(ctx LoweringCtx, n logicalpkg.Node) (RenderedQuery, error) {
	if n == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: lowerHistogramFunction called with nil")
	}
	if ctx.Analysis == nil || ctx.Analysis.InfoFor(n) == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: histogram function missing logical analysis")
	}
	rendered, err := renderHistogramFunctionLogical(ctx.Config, n, ctx.Params)
	if err != nil {
		return RenderedQuery{}, err
	}
	return finalizeRenderedFragment(rendered)
}
```

- [ ] **Step 5: Run tests + compliance**

Run:
```bash
go test ./internal/promshim/native/renderer/...
scripts/run-compliance.sh
```
Expected: all PASS; compliance 538/539.

- [ ] **Step 6: Commit**

```bash
git add internal/promshim/native/renderer/histogram_logical.go \
        internal/promshim/native/renderer/histogram_logical_test.go \
        internal/promshim/native/renderer/lower_histogram_function.go
git commit -m "$(cat <<'EOF'
port histogram_function: direct-render from logical.Node

Ports histogram_quantile, histogram_fraction, histogram_quantiles to
read HistogramQuantilePlan / HistogramFractionPlan /
HistogramQuantilesPlan directly. Narrowing threads through
RenderParams for descendant bucket selectors. Byte equality vs. the
Fragment path is enforced by a side-by-side corpus covering all three
functions with leaf, sum-by-le, sum-by-le-job, and range children.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 10: Port range function body + fused helper to logical (Phase A3)

**Goal:** Direct-render `RangeFunctionPlan`, `RatePlan`, `IncreasePlan`, `DeltaPlan`, `ChangesPlan`, `DerivPlan`, `QuantileOverTimePlan`. Port `canFuseRangeAggregationFragment` and `tryRenderFusedRangeAggregationFragment` to their logical variants so Task 11 can consume them.

**Files:**
- Create: `internal/promshim/native/renderer/range_logical.go`
- Modify: `internal/promshim/native/renderer/lower_range_function.go` (flip dispatch)
- Test: `internal/promshim/native/renderer/range_logical_test.go`

**Acceptance Criteria:**
- [ ] `renderRangeFunctionLogical(cfg, n logicalpkg.Node, params RenderParams) (renderedFragment, error)` handles all seven plan kinds covered by `lowerRangeFunction`.
- [ ] `canFuseRangeAggregationLogical(agg *logicalpkg.AggregationPlan) bool` returns true for the exact structural shape that `canFuseRangeAggregationFragment` returns true for (aggregation over a range-function child).
- [ ] `tryRenderFusedRangeAggregationLogical(cfg, agg *logicalpkg.AggregationPlan, params RenderParams) (renderedFragment, bool, error)` is available for Task 11.
- [ ] `lowerRangeFunction` no longer calls `native.BuildFragment`.
- [ ] Byte-equality corpus covers: all seven `*_over_time` functions, `rate`, `increase`, `delta`, `changes`, `deriv`, `quantile_over_time`, with both leaf and subquery children.

**Verify:**
- `go test ./internal/promshim/native/renderer/...` → PASS
- `scripts/run-compliance.sh` → 538/539
- `scripts/run-bench.sh --matrix` → no query >+10% over pre-task baseline (range is the biggest bench-weighted surface; this is the canary commit)

**Steps:**

- [ ] **Step 1: Read the Fragment body**

Read `internal/promshim/native/renderer/range.go` (938 lines). Identify the top-level dispatch, the seven function branches, the child-kind dispatch inside each branch, and the places where `rangeRequiredBoundsForChild` is called.

Also read `internal/promshim/native/renderer/aggregation_range_fused.go:12-129` — this is what A4 will call into.

- [ ] **Step 2: Write the byte-equality corpus**

Create `internal/promshim/native/renderer/range_logical_test.go`:

```go
package renderer

import (
	"testing"

	logicalpkg "ch-observability/internal/promshim/logical"
	"ch-observability/internal/promshim/native"
	"ch-observability/internal/promshim/storage"
	"github.com/prometheus/prometheus/promql/parser"
)

func TestRangeFunctionLogicalMatchesFragment(t *testing.T) {
	queries := []string{
		`rate(foo[5m])`,
		`increase(foo[1h])`,
		`delta(foo[10m])`,
		`changes(foo[5m])`,
		`deriv(foo[5m])`,
		`avg_over_time(foo[5m])`,
		`sum_over_time(foo[5m])`,
		`min_over_time(foo[5m])`,
		`max_over_time(foo[5m])`,
		`count_over_time(foo[5m])`,
		`stddev_over_time(foo[5m])`,
		`stdvar_over_time(foo[5m])`,
		`last_over_time(foo[5m])`,
		`present_over_time(foo[5m])`,
		`quantile_over_time(0.9, foo[5m])`,
		`predict_linear(foo[5m], 60)`,
		`holt_winters(foo[5m], 0.8, 0.9)`,
		`rate(sum by (x) (foo)[5m:30s])`,
	}
	cfg := storage.QueryConfig{Database: "default", Table: "ts"}
	now := int64(1_700_000_000_000)
	for _, q := range queries {
		t.Run(q, func(t *testing.T) {
			expr, err := parser.ParseExpr(q)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			logical, err := logicalpkg.ToLogical(expr)
			if err != nil {
				t.Fatalf("ToLogical: %v", err)
			}
			analysis, err := logicalpkg.Analyze(logical)
			if err != nil {
				t.Fatalf("Analyze: %v", err)
			}

			for _, mode := range []native.RenderMode{native.RenderModeInstant, native.RenderModeRange} {
				params := RenderParams{
					Mode:             mode,
					EvaluationTimeMS: now,
					StartMS:          now - 3_600_000,
					EndMS:            now,
					StepMS:           30_000,
					RequiredStartMS:  now - 3_900_000,
					RequiredEndMS:    now,
				}

				fragment, err := native.BuildFragment(logical, nil)
				if err != nil {
					t.Fatalf("BuildFragment: %v", err)
				}
				wantRQ, err := RenderFragment(cfg, fragment, params)
				if err != nil {
					t.Fatalf("Fragment render mode=%s: %v", mode, err)
				}

				ctx := LoweringCtx{Config: cfg, Analysis: analysis, Params: params}
				got, err := Lower(ctx, logical)
				if err != nil {
					t.Fatalf("Lower mode=%s: %v", mode, err)
				}
				if got.SQL != wantRQ.SQL {
					t.Fatalf("SQL mismatch mode=%s for %q\nwant:\n%s\n\ngot:\n%s", mode, q, wantRQ.SQL, got.SQL)
				}
				if !queryParamsEqual(got.QueryParams, wantRQ.QueryParams) {
					t.Fatalf("query params mismatch mode=%s for %q", mode, q)
				}
			}
		})
	}
}
```

- [ ] **Step 3: Create `range_logical.go` with the ported body**

Port `renderRangeFunctionFragment` into `renderRangeFunctionLogical`. This is the largest port. Structure:

```go
package renderer

import (
	"fmt"

	logicalpkg "ch-observability/internal/promshim/logical"
	"ch-observability/internal/promshim/native"
	"ch-observability/internal/promshim/storage"
)

func renderRangeFunctionLogical(cfg storage.QueryConfig, n logicalpkg.Node, params RenderParams) (renderedFragment, error) {
	if n == nil {
		return renderedFragment{}, fmt.Errorf("renderer: renderRangeFunctionLogical called with nil")
	}
	// Dispatch on the seven plan kinds (RangeFunctionPlan, RatePlan,
	// IncreasePlan, DeltaPlan, ChangesPlan, DerivPlan,
	// QuantileOverTimePlan) to determine the function name and any
	// function-specific parameters. This mirrors the top switch in
	// renderRangeFunctionFragment.
	functionName, child, functionParams, err := classifyRangeFunctionLogical(n)
	if err != nil {
		return renderedFragment{}, err
	}
	// Render the child rows SQL. Child may be LeafExprPlan or SubqueryPlan.
	return buildRangeFunctionFromChild(cfg, functionName, child, functionParams, params)
}

// classifyRangeFunctionLogical extracts (functionName, child, functionParams)
// from a range-function plan kind. Mirrors how the Fragment builder
// populates fragment.RangeFunction.{Name,Source,...}.
func classifyRangeFunctionLogical(n logicalpkg.Node) (string, logicalpkg.Node, native.RangeFunctionParams, error) { /* port */ }

// canFuseRangeAggregationLogical mirrors canFuseRangeAggregationFragment
// but inspects the logical AggregationPlan directly. Returns true iff
// the child is a range-function plan over a leaf (or scalar-involving
// source) and the aggregation op is one of SUM/AVG/COUNT/MIN/MAX.
func canFuseRangeAggregationLogical(agg *logicalpkg.AggregationPlan) bool { /* port */ }

// tryRenderFusedRangeAggregationLogical ports tryRenderFusedRangeAggregationFragment.
func tryRenderFusedRangeAggregationLogical(cfg storage.QueryConfig, agg *logicalpkg.AggregationPlan, params RenderParams) (renderedFragment, bool, error) { /* port */ }

// renderRangeFunctionRowsLogical ports renderRangeFunctionRowsSQL so the
// histogram-function renderer can reuse it from Task 9's histogram code.
// (Task 9 used the Fragment variant via tryRenderHistogramChildRowsSQL;
// after this commit, switch that caller to use the logical variant.)
func renderRangeFunctionRowsLogical(cfg storage.QueryConfig, n logicalpkg.Node, params RenderParams) (string, map[string]string, error) { /* port */ }
```

Each `/* port */` body is a direct structural translation of the corresponding Fragment-side function. For the largest ports, copy the Fragment body into the new function, then mechanically replace:

| Fragment expression | Logical replacement |
|---|---|
| `fragment.RangeFunction.Name` | `functionName` |
| `fragment.RangeFunction.Source` | `child` |
| `fragment.RangeFunction.RangeMS` | `rangeMSFromLogical(child)` (use the helper already available for logical range bounds) |
| `rangeRequiredBoundsForChild(fragment.RangeFunction.Source, ...)` | `logicalRangeRequiredBoundsForChild(child, ...)` |
| `RenderFragment(cfg, subFragment, subParams)` | `Lower(childCtx, childNode)` + `finalizeRenderedFragment` |
| `resolvedFragmentAnchorTimeMS(fragment, params)` | `logicalResolvedAnchorTimeMS(n, params)` |

- [ ] **Step 4: Flip `lowerRangeFunction`**

Edit `internal/promshim/native/renderer/lower_range_function.go`:

```go
func lowerRangeFunction(ctx LoweringCtx, n logicalpkg.Node) (RenderedQuery, error) {
	if n == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: lowerRangeFunction called with nil")
	}
	if ctx.Analysis == nil || ctx.Analysis.InfoFor(n) == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: range function missing logical analysis")
	}
	rendered, err := renderRangeFunctionLogical(ctx.Config, n, ctx.Params)
	if err != nil {
		return RenderedQuery{}, err
	}
	return finalizeRenderedFragment(rendered)
}
```

- [ ] **Step 5: Run tests + compliance + bench**

Run:
```bash
go test ./internal/promshim/native/renderer/...
go test ./internal/promshim/...
scripts/run-compliance.sh
scripts/run-bench.sh --matrix
```
Expected:
- all test PASS
- compliance 538/539
- bench matrix: no regression >+10% vs. pre-task baseline

If bench regresses, investigate before committing. Most likely causes: (a) a `Lower` call where a Fragment-direct call would have been cheaper, (b) an extra intermediate allocation in the logical dispatch, (c) genuinely different SQL (which would also fail the byte-equality test).

- [ ] **Step 6: Commit**

```bash
git add internal/promshim/native/renderer/range_logical.go \
        internal/promshim/native/renderer/range_logical_test.go \
        internal/promshim/native/renderer/lower_range_function.go
git commit -m "$(cat <<'EOF'
port range_function: direct-render from logical.Node

All seven range-function plan kinds (RangeFunctionPlan, RatePlan,
IncreasePlan, DeltaPlan, ChangesPlan, DerivPlan,
QuantileOverTimePlan) render directly from logical.Node with no
Fragment build. The fused range+aggregation helpers also port
(canFuseRangeAggregationLogical, tryRenderFusedRangeAggregationLogical)
so the aggregation port in the next commit can consume them.

Byte equality vs. the Fragment path holds across all seven functions
and both RenderModeInstant / RenderModeRange.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 11: Port aggregation body to logical (Phase A4, end of Phase A)

**Goal:** Direct-render `AggregationPlan`. Consume `canFuseRangeAggregationLogical` / `tryRenderFusedRangeAggregationLogical` from Task 10. End of Phase A — refresh bench baseline.

**Files:**
- Create: `internal/promshim/native/renderer/aggregation_logical.go`
- Modify: `internal/promshim/native/renderer/lower_aggregation.go` (flip dispatch)
- Test: `internal/promshim/native/renderer/aggregation_logical_test.go`
- Modify: `harness/bench/baseline.json` (refresh)

**Acceptance Criteria:**
- [ ] `renderAggregationLogical(cfg, n *logicalpkg.AggregationPlan, params RenderParams) (renderedFragment, error)` exists.
- [ ] `lowerAggregation` no longer calls `native.BuildFragment`.
- [ ] All aggregation ops render correctly: sum, avg, count, min, max, stddev, stdvar, topk, bottomk, quantile, group, count_values, with and without grouping, with and without fused range.
- [ ] Byte-equality corpus covers all 12 ops × (grouping-by, grouping-without, no-grouping) × (leaf child, range-fused child, binary-scalar child).
- [ ] Fused range+aggregation path goes through `tryRenderFusedRangeAggregationLogical`.
- [ ] `harness/bench/baseline.json` refreshed; all long-range profiles rerun.

**Verify:**
- `go test ./internal/promshim/native/renderer/...` → PASS
- `scripts/run-compliance.sh` → 538/539
- `scripts/run-bench.sh --matrix --long-range all` → no query >+10% over pre-Phase-A baseline

**Steps:**

- [ ] **Step 1: Read the Fragment body**

Read `internal/promshim/native/renderer/join.go:62-240` (the `renderAggregationFragment` function) and `internal/promshim/native/renderer/source.go:291-330` (`renderAggregationSource` — supports 3 Fragment source kinds).

- [ ] **Step 2: Write the byte-equality corpus**

Create `internal/promshim/native/renderer/aggregation_logical_test.go`:

```go
package renderer

import (
	"testing"

	logicalpkg "ch-observability/internal/promshim/logical"
	"ch-observability/internal/promshim/native"
	"ch-observability/internal/promshim/storage"
	"github.com/prometheus/prometheus/promql/parser"
)

func TestAggregationLogicalMatchesFragment(t *testing.T) {
	queries := []string{
		// bare aggregations
		`sum(foo)`,
		`avg(foo)`,
		`count(foo)`,
		`min(foo)`,
		`max(foo)`,
		`stddev(foo)`,
		`stdvar(foo)`,
		`group(foo)`,
		// grouping
		`sum by (x) (foo)`,
		`sum without (x) (foo)`,
		`avg by (x, y) (foo)`,
		// parameterized
		`topk(5, foo)`,
		`bottomk(3, foo)`,
		`quantile(0.9, foo)`,
		`count_values("value", foo)`,
		// fused range+aggregation
		`sum by (x) (rate(foo[5m]))`,
		`avg by (x) (rate(foo[1m]))`,
		`count by (x) (increase(foo[10m]))`,
		// scalar-binary child
		`sum(foo * 2)`,
		`sum by (x) (foo / 100)`,
	}
	cfg := storage.QueryConfig{Database: "default", Table: "ts"}
	now := int64(1_700_000_000_000)
	for _, q := range queries {
		t.Run(q, func(t *testing.T) {
			expr, err := parser.ParseExpr(q)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			logical, err := logicalpkg.ToLogical(expr)
			if err != nil {
				t.Fatalf("ToLogical: %v", err)
			}
			analysis, err := logicalpkg.Analyze(logical)
			if err != nil {
				t.Fatalf("Analyze: %v", err)
			}

			for _, mode := range []native.RenderMode{native.RenderModeInstant, native.RenderModeRange} {
				params := RenderParams{
					Mode:             mode,
					EvaluationTimeMS: now,
					StartMS:          now - 3_600_000,
					EndMS:            now,
					StepMS:           30_000,
					RequiredStartMS:  now - 3_900_000,
					RequiredEndMS:    now,
				}
				fragment, _ := native.BuildFragment(logical, nil)
				wantRQ, err := RenderFragment(cfg, fragment, params)
				if err != nil {
					t.Fatalf("Fragment render mode=%s: %v", mode, err)
				}
				ctx := LoweringCtx{Config: cfg, Analysis: analysis, Params: params}
				got, err := Lower(ctx, logical)
				if err != nil {
					t.Fatalf("Lower mode=%s: %v", mode, err)
				}
				if got.SQL != wantRQ.SQL {
					t.Fatalf("SQL mismatch mode=%s for %q\nwant:\n%s\n\ngot:\n%s", mode, q, wantRQ.SQL, got.SQL)
				}
			}
		})
	}
}
```

- [ ] **Step 3: Port `renderAggregationLogical`**

Create `internal/promshim/native/renderer/aggregation_logical.go`:

```go
package renderer

import (
	"fmt"

	logicalpkg "ch-observability/internal/promshim/logical"
	"ch-observability/internal/promshim/storage"
)

func renderAggregationLogical(cfg storage.QueryConfig, n *logicalpkg.AggregationPlan, params RenderParams) (renderedFragment, error) {
	if n == nil {
		return renderedFragment{}, fmt.Errorf("renderer: renderAggregationLogical called with nil")
	}
	// Fused path first — if the child is a range function and the fusion
	// shape applies, emit a fused query. Mirrors tryRenderFusedRangeAggregationFragment.
	if canFuseRangeAggregationLogical(n) {
		rf, ok, err := tryRenderFusedRangeAggregationLogical(cfg, n, params)
		if err != nil {
			return renderedFragment{}, err
		}
		if ok {
			return rf, nil
		}
	}
	// Non-fused path: render the child's rows, then wrap with the aggregation SQL.
	childRowsSQL, childRowsParams, err := renderAggregationSourceLogical(cfg, n.Source, params)
	if err != nil {
		return renderedFragment{}, err
	}
	return buildAggregationSQL(cfg, n, childRowsSQL, childRowsParams, params)
}

// renderAggregationSourceLogical dispatches on the child's logical kind,
// porting the Fragment switch in source.go:291 (renderAggregationSource)
// which supports LeafSource / UnarySourceExpr / BinaryScalarSourceExpr.
// Logical equivalents: LeafExprPlan, UnaryPlan (ADD/SUB only), BinaryPlan
// (scalar-involving only).
func renderAggregationSourceLogical(cfg storage.QueryConfig, src logicalpkg.Node, params RenderParams) (string, map[string]string, error) {
	switch child := src.(type) {
	case *logicalpkg.LeafExprPlan:
		// Port of the LeafSource branch.
		return renderAggregationLeafSource(cfg, child, params)
	case *logicalpkg.UnaryPlan:
		// Port of the UnarySourceExpr branch (ADD is identity; SUB negates the value).
		return renderAggregationUnarySource(cfg, child, params)
	case *logicalpkg.BinaryPlan:
		// Port of the BinaryScalarSourceExpr branch (scalar-involving).
		return renderAggregationBinaryScalarSource(cfg, child, params)
	default:
		return "", nil, fmt.Errorf("renderer: renderAggregationSourceLogical unsupported child kind %T", src)
	}
}
```

Port the three `renderAggregation*Source` branches and `buildAggregationSQL` as straight translations of `join.go:62-240` and `source.go:291-330`.

- [ ] **Step 4: Flip `lowerAggregation`**

Edit `internal/promshim/native/renderer/lower_aggregation.go`:

```go
func lowerAggregation(ctx LoweringCtx, n *logicalpkg.AggregationPlan) (RenderedQuery, error) {
	if n == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: lowerAggregation called with nil")
	}
	if ctx.Analysis == nil || ctx.Analysis.InfoFor(n) == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: aggregation missing logical analysis")
	}
	rendered, err := renderAggregationLogical(ctx.Config, n, ctx.Params)
	if err != nil {
		return RenderedQuery{}, err
	}
	return finalizeRenderedFragment(rendered)
}
```

- [ ] **Step 5: Run tests + compliance**

Run:
```bash
go test ./internal/promshim/native/renderer/...
go test ./internal/promshim/...
scripts/run-compliance.sh
```
Expected: all PASS; compliance 538/539.

- [ ] **Step 6: End-of-Phase-A bench verification + baseline refresh**

Run:
```bash
scripts/run-bench.sh --matrix
```
Expected: no query >+10% over the baseline committed in `448c635`'s ancestor.

If the matrix is clean, refresh the baseline:
```bash
scripts/run-bench.sh --matrix --update-baseline
scripts/seed-long-range.sh --profile 7d --target ch
scripts/seed-long-range.sh --profile 30d --target ch
scripts/seed-long-range.sh --profile 1y --target ch
scripts/run-bench.sh --long-range all --update-baseline
```

Verify no per-query regression >+10%. If regression appears, stop and investigate before committing baseline.

- [ ] **Step 7: Commit**

```bash
git add internal/promshim/native/renderer/aggregation_logical.go \
        internal/promshim/native/renderer/aggregation_logical_test.go \
        internal/promshim/native/renderer/lower_aggregation.go \
        harness/bench/baseline.json
git commit -m "$(cat <<'EOF'
port aggregation: direct-render from logical.Node

Last Phase-A body. renderAggregationLogical reads AggregationPlan
directly and dispatches fused range+aggregation through the logical
helper ported in the previous commit. All 12 aggregation ops (with
grouping, without grouping, or bare) over leaf / range / scalar-binary
children render identically to the Fragment path.

Also refreshes harness/bench/baseline.json for all long-range profiles
now that every Phase-A body is direct-rendered. No per-query
regression >+10%.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 12: Direct-render scalar-involving BinaryPlan (Phase B)

**Goal:** Remove the last `native.BuildFragment` call from `renderer.Lower`. After this task, every lowerer direct-renders.

**Files:**
- Create: `internal/promshim/native/renderer/lower_binary_scalar.go`
- Modify: `internal/promshim/native/renderer/lower.go:146-155` (replace scalar-involving branch)
- Test: `internal/promshim/native/renderer/lower_binary_scalar_test.go`

**Acceptance Criteria:**
- [ ] `lowerBinaryScalarInvolving(ctx, n *logicalpkg.BinaryPlan) (RenderedQuery, error)` handles scalar-on-LHS, scalar-on-RHS, and scalar-on-both-sides (if applicable) variants.
- [ ] `lower.go:146-155` no longer calls `native.BuildFragment` or `RenderFragment`.
- [ ] Byte-equality corpus covers each operator (`+ - * / %` plus comparison ops `== != < <= > >=`) combined with scalar literals and scalar-returning subexpressions.
- [ ] `grep -rn 'native\.BuildFragment' internal/promshim/native/renderer/` returns zero matches (confirms last `BuildFragment` consumer in renderer is gone).

**Verify:**
- `go test ./internal/promshim/native/renderer/...` → PASS
- `scripts/run-compliance.sh` → 538/539

**Steps:**

- [ ] **Step 1: Write the byte-equality corpus**

Create `internal/promshim/native/renderer/lower_binary_scalar_test.go`:

```go
package renderer

import (
	"testing"

	logicalpkg "ch-observability/internal/promshim/logical"
	"ch-observability/internal/promshim/native"
	"ch-observability/internal/promshim/storage"
	"github.com/prometheus/prometheus/promql/parser"
)

func TestLowerBinaryScalarInvolvingMatchesFragment(t *testing.T) {
	queries := []string{
		// scalar-on-RHS
		`foo + 1`,
		`foo - 10`,
		`foo * 2`,
		`foo / 100`,
		`foo % 3`,
		`foo ^ 2`,
		`foo == 5`,
		`foo != 5`,
		`foo < 5`,
		`foo <= 5`,
		`foo > 5`,
		`foo >= 5`,
		// scalar-on-LHS
		`1 + foo`,
		`100 - foo`,
		`2 * foo`,
		// scalar subexpression
		`foo * (1 + 1)`,
		`foo + pi()`,
		`foo / scalar(sum(bar))`,
		// scalar-on-both-sides (produces a scalar result — may dispatch differently)
		`1 + 2`,
	}
	cfg := storage.QueryConfig{Database: "default", Table: "ts"}
	now := int64(1_700_000_000_000)
	for _, q := range queries {
		t.Run(q, func(t *testing.T) {
			expr, err := parser.ParseExpr(q)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			logical, err := logicalpkg.ToLogical(expr)
			if err != nil {
				t.Fatalf("ToLogical: %v", err)
			}
			analysis, err := logicalpkg.Analyze(logical)
			if err != nil {
				t.Fatalf("Analyze: %v", err)
			}

			for _, mode := range []native.RenderMode{native.RenderModeInstant, native.RenderModeRange} {
				params := RenderParams{
					Mode:             mode,
					EvaluationTimeMS: now,
					StartMS:          now - 3_600_000,
					EndMS:            now,
					StepMS:           30_000,
					RequiredStartMS:  now - 3_900_000,
					RequiredEndMS:    now,
				}
				fragment, _ := native.BuildFragment(logical, nil)
				wantRQ, err := RenderFragment(cfg, fragment, params)
				if err != nil {
					t.Fatalf("Fragment render mode=%s: %v", mode, err)
				}
				ctx := LoweringCtx{Config: cfg, Analysis: analysis, Params: params}
				got, err := Lower(ctx, logical)
				if err != nil {
					t.Fatalf("Lower mode=%s: %v", mode, err)
				}
				if got.SQL != wantRQ.SQL {
					t.Fatalf("SQL mismatch mode=%s for %q\nwant:\n%s\n\ngot:\n%s", mode, q, wantRQ.SQL, got.SQL)
				}
			}
		})
	}
}
```

- [ ] **Step 2: Implement `lowerBinaryScalarInvolving`**

Create `internal/promshim/native/renderer/lower_binary_scalar.go`:

```go
package renderer

import (
	"fmt"

	logicalpkg "ch-observability/internal/promshim/logical"
)

// lowerBinaryScalarInvolving handles BinaryPlan nodes where at least one
// side has DomainScalar. One side is vector-typed and rendered via
// Lower(childCtx, nonScalarChild); the other side folds to a scalar value
// or scalar-SQL fragment via the existing scalar-lowerers
// (lowerScalarLiteral / lowerScalarBuiltin / lowerScalarConvert). The
// final SQL is built via renderValueTransformFromSource, matching the
// Fragment path's BinaryScalarSourceExpr branch in source.go.
func lowerBinaryScalarInvolving(ctx LoweringCtx, n *logicalpkg.BinaryPlan) (RenderedQuery, error) {
	lhsInfo := ctx.Analysis.InfoFor(n.LHS)
	rhsInfo := ctx.Analysis.InfoFor(n.RHS)
	if lhsInfo == nil || rhsInfo == nil {
		return RenderedQuery{}, fmt.Errorf("renderer: binary scalar missing analysis")
	}
	lhsScalar := lhsInfo.TimeDomain == logicalpkg.DomainScalar
	rhsScalar := rhsInfo.TimeDomain == logicalpkg.DomainScalar

	// Both sides scalar: fold entirely via scalar lowerers. Fragment path
	// handles this by constant-folding the BinaryScalarSourceExpr; mirror
	// that here by producing a ScalarLiteral-shaped rendered query.
	if lhsScalar && rhsScalar {
		return renderScalarScalarBinary(ctx, n)
	}

	// Exactly one side scalar. Render the vector side via Lower, fold the
	// scalar side, then wrap with renderValueTransformFromSource.
	var vectorChild logicalpkg.Node
	var scalarChild logicalpkg.Node
	var scalarOnRHS bool
	if lhsScalar {
		scalarChild, vectorChild = n.LHS, n.RHS
		scalarOnRHS = false
	} else {
		vectorChild, scalarChild = n.LHS, n.RHS
		scalarOnRHS = true
	}

	vectorRQ, err := Lower(ctx, vectorChild)
	if err != nil {
		return RenderedQuery{}, err
	}
	scalarValueExpr, err := foldScalarChildToSQLExpr(ctx, scalarChild)
	if err != nil {
		return RenderedQuery{}, err
	}
	valueExpr := buildScalarBinaryValueExpr(n.Op, scalarValueExpr, scalarOnRHS)
	filterExpr := buildScalarBinaryFilterExpr(n.Op, scalarValueExpr, scalarOnRHS)
	dropsMetric := binaryOpDropsMetricName(n.Op)

	rendered, err := renderValueTransformFromSource(vectorRQ.SQL, vectorRQ.QueryParams, valueExpr, filterExpr, dropsMetric, ctx.Params)
	if err != nil {
		return RenderedQuery{}, err
	}
	return finalizeRenderedFragment(rendered)
}

// foldScalarChildToSQLExpr folds a scalar-typed child to a SQL expression.
// Mirrors how the Fragment builder constructs BinaryScalarSourceExpr's
// scalar side (a constant, a scalar-builtin result, or a scalar-convert
// result). Dispatch on the logical kind.
func foldScalarChildToSQLExpr(ctx LoweringCtx, scalarChild logicalpkg.Node) (string, error) { /* port */ }

// buildScalarBinaryValueExpr builds the "{value} <op> <scalar>" SQL
// fragment (or "<scalar> <op> {value}" when scalarOnRHS=false).
// Mirrors the Fragment builder's BinaryScalarSourceExpr value-expr
// construction. Handles arithmetic, power, modulo, and comparison ops.
func buildScalarBinaryValueExpr(op parser.ItemType, scalarExpr string, scalarOnRHS bool) string { /* port */ }

// buildScalarBinaryFilterExpr returns the filter expression for bool-mode
// comparisons; returns "" for arithmetic and non-bool comparisons.
func buildScalarBinaryFilterExpr(op parser.ItemType, scalarExpr string, scalarOnRHS bool) string { /* port */ }

// binaryOpDropsMetricName returns true if the binary op drops the
// __name__ label. Arithmetic ops do; comparison ops do not. Mirrors
// Fragment-side behavior.
func binaryOpDropsMetricName(op parser.ItemType) bool { /* port */ }

// renderScalarScalarBinary folds a binary where both sides are scalar
// into a scalar-literal rendered query. This matches how the Fragment
// builder constant-folds this case.
func renderScalarScalarBinary(ctx LoweringCtx, n *logicalpkg.BinaryPlan) (RenderedQuery, error) { /* port */ }
```

Port each `/* port */` by mirroring the corresponding Fragment-builder construction. Exact reference: `internal/promshim/native/analysis_binary.go:133-...` for `applyUnarySourceTransform` (the unary-SUB analogue) and the nearby binary-scalar transform builder.

- [ ] **Step 3: Flip `lower.go:146-155`**

Edit `internal/promshim/native/renderer/lower.go`:

```go
	if lhsInfo.TimeDomain == logicalpkg.DomainScalar || rhsInfo.TimeDomain == logicalpkg.DomainScalar {
		return lowerBinaryScalarInvolving(ctx, n)
	}
	return lowerBinaryVectorJoin(ctx, n)
```

- [ ] **Step 4: Run tests + compliance**

Run:
```bash
go test ./internal/promshim/native/renderer/...
go test ./internal/promshim/...
scripts/run-compliance.sh
```
Expected: all PASS; compliance 538/539.

- [ ] **Step 5: Verify no renderer BuildFragment call remains**

Run:
```bash
grep -rn 'native\.BuildFragment' internal/promshim/native/renderer/
```
Expected: zero matches (outside of deleted files — at this point there should be none).

- [ ] **Step 6: Commit**

```bash
git add internal/promshim/native/renderer/lower_binary_scalar.go \
        internal/promshim/native/renderer/lower_binary_scalar_test.go \
        internal/promshim/native/renderer/lower.go
git commit -m "$(cat <<'EOF'
port binary-scalar: direct-render from logical.Node

Last Fragment-tap in renderer.Lower. lowerBinary's scalar-involving
branch now dispatches to lowerBinaryScalarInvolving, which lowers the
vector side via Lower and folds the scalar side via the scalar
lowerers, matching the Fragment BinaryScalarSourceExpr path.

After this commit, no lowerer in renderer.Lower calls
native.BuildFragment. Phase C can now delete the Fragment machinery.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 13: Delete Fragment machinery (Phase C)

**Goal:** Remove `NativeFragment`, `FragmentKind*`, `native.BuildFragment`, the Fragment-producing walk in `analysis.go`, and all Fragment-body renderers. Rename `FragmentInfo` → `NodeInfo` and drop its `Fragment` field. Refresh bench baseline.

**Files:**
- Delete: `internal/promshim/native/builder.go`
- Delete: `internal/promshim/native/renderer/histogram.go`
- Delete: `internal/promshim/native/renderer/range.go`
- Delete: `internal/promshim/native/renderer/aggregation_range_fused.go`
- Delete: `internal/promshim/native/renderer/source.go`
- Modify: `internal/promshim/native/renderer/renderer.go` (remove `RenderFragment`, `renderFragment`)
- Modify: `internal/promshim/native/renderer/join.go` (remove `renderAggregationFragment`; keep `renderValueTransformFromSource`)
- Modify: `internal/promshim/native/types.go` (delete `NativeFragment`, `FragmentKind*`; rename `FragmentInfo` → `NodeInfo`; drop `Fragment` field)
- Modify: `internal/promshim/native/analysis.go` (delete Fragment-producing walk including UnaryPlan branch at lines 62-103)
- Modify: `internal/promshim/native/selector.go` (delete `SelectorSource.RequireFullTags` and `SelectorSource.RequiredTagLabels`)
- Modify: `internal/promshim/local/native_subtree.go:140-165` (remove Fragment-dispatch fallback)
- Modify: `internal/promshim/native/renderer/lower_leaf.go:91-102` (update `nativeSelectorToStorage` to read from `RenderParams` instead of the deleted selector fields — threaded via caller)
- Modify: `harness/bench/baseline.json` (final refresh)

**Acceptance Criteria:**
- [ ] `grep -rn 'NativeFragment\|FragmentKind\|BuildFragment\|RenderFragment' internal/promshim/ --include='*.go'` returns zero matches.
- [ ] `native.Analysis` surface: `NodeInfo` struct with `LabelLineage`, `TimeRequirements`, `SourcePromQL`. No `Fragment` field.
- [ ] `native.SelectorSource` has no `RequireFullTags` / `RequiredTagLabels` fields (propagated via `RenderParams` only).
- [ ] `go build ./internal/promshim/...` clean.
- [ ] All tests (`go test ./internal/promshim/...`) pass.
- [ ] `scripts/run-compliance.sh` → 538/539.
- [ ] `scripts/run-bench.sh --matrix --long-range all` → no regression >+10%; fresh baseline committed.

**Verify:**
- `grep -rn 'NativeFragment\|FragmentKind\|BuildFragment\|RenderFragment' internal/promshim/ --include='*.go'` → zero matches
- `go build ./internal/promshim/...` → clean
- `go test ./internal/promshim/...` → PASS
- `scripts/run-compliance.sh` → 538/539
- `scripts/run-bench.sh --matrix --long-range all` → no regression >+10%

**Steps:**

- [ ] **Step 1: Grep gate — find external `FragmentInfo.Fragment` consumers**

Run:
```bash
grep -rn '\.Fragment\b' internal/promshim/ --include='*.go' | grep -v '_test.go\|renderer/\|native_subtree.go\|native/analysis.go\|native/builder.go\|native/types.go'
```
Expected: zero matches. If matches appear, port those callers to logical-direct before proceeding.

- [ ] **Step 2: Delete the Fragment-body renderer files**

```bash
git rm internal/promshim/native/renderer/histogram.go
git rm internal/promshim/native/renderer/range.go
git rm internal/promshim/native/renderer/aggregation_range_fused.go
git rm internal/promshim/native/renderer/source.go
```

Verify build breaks expectedly:
```bash
go build ./internal/promshim/native/renderer/... 2>&1 | head -40
```
Expected: references to deleted files. Fix iteratively in the next steps.

- [ ] **Step 3: Strip Fragment-body references from `renderer.go` and `join.go`**

Edit `internal/promshim/native/renderer/renderer.go` — delete `RenderFragment` (line ~30), `renderFragment` (line ~38), and the Fragment-receiving helper dispatch.

Edit `internal/promshim/native/renderer/join.go` — delete `renderAggregationFragment` (function around line 62). Keep `renderValueTransformFromSource` (line 241 — a pure helper taking SQL strings, no Fragment dependency) and any other pure helpers in the file.

If the file ends up empty after removals, delete it (`git rm`).

- [ ] **Step 4: Delete `native.BuildFragment`, `native.CloneFragment`, `NativeFragment` struct, `FragmentKind*` constants**

Delete `internal/promshim/native/builder.go`:
```bash
git rm internal/promshim/native/builder.go
```

Edit `internal/promshim/native/types.go`:
- Delete `NativeFragment` struct and all its fields.
- Delete all `FragmentKind*` constants.
- Delete `CloneFragment` (if defined here).
- Rename `FragmentInfo` → `NodeInfo`; remove its `Fragment` field. Keep its remaining fields (`LabelLineage`, `TimeRequirements`, `SourcePromQL`-related).

Update all references to `FragmentInfo` → `NodeInfo` across the codebase:
```bash
grep -rln 'FragmentInfo' internal/promshim/ --include='*.go' | xargs sed -i 's/FragmentInfo/NodeInfo/g'
```

- [ ] **Step 5: Strip the Fragment-producing walk from `analysis.go`**

Edit `internal/promshim/native/analysis.go`:
- Delete the UnaryPlan Fragment branch at lines 62-103.
- Delete every other branch that constructs a Fragment (each per-kind branch that sets `info.Fragment`). Keep branches that compute `info.LabelLineage`, `info.TimeRequirements`, `info.SourcePromQL`.
- After the edit, the analyzer walk should only populate the non-Fragment fields on `NodeInfo`.

Also update `internal/promshim/native/analysis_binary.go` — `applyUnarySourceTransform` and friends that build Fragment shapes go away; their LabelLineage/TimeRequirements contributions stay.

- [ ] **Step 6: Remove narrowing fields from `SelectorSource`**

Edit `internal/promshim/native/selector.go`:
- Delete `RequireFullTags` and `RequiredTagLabels` fields on `SelectorSource`.

Update `lower_leaf.go:91-102` — `nativeSelectorToStorage` no longer sets these from the selector; instead, a new caller-side convention sets them from `RenderParams`:

```go
func nativeSelectorToStorage(sel *native.SelectorSource) storage.SelectorSource {
	return storage.SelectorSource{
		Kind:       storage.SelectorKind(sel.Kind),
		MetricName: sel.MetricName,
		Matchers:   selectorEffectiveMatchers(sel),
		NeedTags:   selectorNeedsTags(sel),
		LookbackMS: sel.Lookback.Milliseconds(),
		OffsetMS:   sel.Offset.Milliseconds(),
	}
}

// applyRenderParamsNarrowing mutates a storage.SelectorSource to honor
// the tag-narrowing from RenderParams. Call this after nativeSelectorToStorage
// when the renderer has narrowing requirements (histogram projection/function).
func applyRenderParamsNarrowing(sel *storage.SelectorSource, params RenderParams) {
	if params.RequireFullTags {
		sel.RequireFullTags = true
		sel.RequiredTagLabels = nil
		return
	}
	if len(params.RequiredTagLabels) > 0 {
		sel.RequireFullTags = false
		sel.RequiredTagLabels = append([]string(nil), params.RequiredTagLabels...)
	}
}
```

Update every `renderXxxLogical` call site that previously set narrowing on the selector to call `applyRenderParamsNarrowing(&storageSel, params)` instead. Search for them:
```bash
grep -rn 'storageSel\.RequireFullTags\|storageSel\.RequiredTagLabels' internal/promshim/native/renderer/
```

- [ ] **Step 7: Remove Fragment-dispatch fallback from `native_subtree.go`**

Edit `internal/promshim/local/native_subtree.go:140-165`:

Before:
```go
rendered, err := renderer.Lower(ctx, p.Logical)
if err != nil {
    if renderer.IsUnsupportedByLower(err) {
        // Fragment fallback
        return renderer.RenderFragment(cfg, p.Fragment, renderParams)
    }
    return renderer.RenderedQuery{}, err
}
return rendered, nil
```

After:
```go
return renderer.Lower(ctx, p.Logical)
```

`IsUnsupportedByLower` stays — it is still meaningful for node kinds that `Lower` does not handle (the `default` case in the dispatch). Such queries now become hard errors instead of routing to Fragment, which is the correct behavior: unsupported kinds must be addressed in the lowerer, not papered over with a Fragment fallback.

Update callers that depended on the old fallback to surface a clearer error.

- [ ] **Step 8: Build clean, run tests**

Run:
```bash
go build ./internal/promshim/...
go test ./internal/promshim/...
```
Fix any remaining references iteratively until green.

- [ ] **Step 9: Run compliance + bench**

Run:
```bash
scripts/run-compliance.sh
scripts/run-bench.sh --matrix --long-range all
```
Expected:
- compliance 538/539
- bench: no query >+10% over pre-Phase-A baseline; every long-range profile completes.

Refresh baseline:
```bash
scripts/run-bench.sh --matrix --update-baseline
scripts/run-bench.sh --long-range all --update-baseline
```

- [ ] **Step 10: Final grep gate**

Run:
```bash
grep -rn 'NativeFragment\|FragmentKind\|BuildFragment\|RenderFragment' internal/promshim/ --include='*.go'
```
Expected: zero matches.

```bash
grep -rn '\.Fragment\b' internal/promshim/ --include='*.go' | grep -v '_test.go\|renderer/render'
```
Expected: zero matches (the only remaining `.Fragment` references should be in removed-but-still-pending-index state).

- [ ] **Step 11: Commit**

```bash
git add -A internal/promshim/ harness/bench/baseline.json
git commit -m "$(cat <<'EOF'
delete NativeFragment machinery

Every consumer of NativeFragment is now direct-render from
logical.Node. This commit deletes the Fragment types and their
builder:

- native.NativeFragment, FragmentKind*, native.BuildFragment,
  native.CloneFragment
- native/builder.go (entire file)
- renderer/histogram.go, renderer/range.go,
  renderer/aggregation_range_fused.go, renderer/source.go
- renderer.RenderFragment / renderer.renderFragment
- renderer.renderAggregationFragment
- native.SelectorSource.RequireFullTags /
  SelectorSource.RequiredTagLabels (moved to RenderParams)
- The Fragment-producing branches of native/analysis.go (including
  the UnaryPlan branch at lines 62-103)
- The Fragment-dispatch fallback in local/native_subtree.go

native.Analysis survives as a slim per-logical-node render-hint map
(NodeInfo with LabelLineage, TimeRequirements, SourcePromQL). Tier-2
lowering is now a pure logical→SQL transform.

Refreshes harness/bench/baseline.json. Compliance 538/539
(allowlisted topk-tie-break-ordering only).

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Success verification (end of plan)

After Task 13 commits, run:

```bash
grep -rn 'NativeFragment\|FragmentKind\|BuildFragment\|RenderFragment' internal/promshim/ --include='*.go'
# Expected: zero matches

go test ./internal/promshim/...
# Expected: PASS

scripts/run-compliance.sh
# Expected: 538/539 (topk-tie-break-ordering allowlisted)

scripts/run-bench.sh --matrix --long-range all
# Expected: no per-query regression >+10% over pre-Phase-A baseline

wc -l internal/promshim/native/*.go internal/promshim/native/renderer/*.go
# Expected: substantial reduction — histogram.go (646), range.go (938),
# join.go (438 → pure helpers only), source.go (380), builder.go (156),
# aggregation_range_fused.go (129) all gone or slimmed.
```

All six checks pass → retirement complete.
