# SQL builder audit

Current status after completing the planned retrofit tasks `#159`-`#163` from `.pi/sql-builder-plan.md`.

## Guarded migrated paths

These paths have been moved onto `internal/promshim/native/sqlb` for top-level query assembly and are now expected to stay off legacy SQL template emission:

- `internal/promshim/storage/join_sql.go`
- `internal/promshim/native/renderer.go` wrapper/query assembly paths
  - `wrapInstantSourceQuery(...)`
  - `wrapRangeSourceQuery(...)`
  - `buildInstantRangeFunctionSQL(...)`
  - `buildRangeFunctionOverSubqueryRangeSQL(...)`

Additionally, legacy `{value}` / `{tags}` wrapper substitution has been removed from the migrated source-wrapper paths and replaced with the shared source-template compiler in `internal/promshim/storage/sql.go`.

## Retrofit completed earlier in this slice

- `internal/promshim/native/sqlb/sqlb.go`
- `internal/promshim/storage/sql.go`
  - instant/range top-level query builders
  - aggregation builders and aggregation source wrappers
- `internal/promshim/storage/join_sql.go`
- `internal/promshim/native/renderer.go` wrapper/query assembly paths listed above

## Remaining SQL-emission debt

After the retrofit and low-risk cleanup slices completed so far, the migrated files are no longer carrying full SQL query-template emission debt.

What remains is much smaller and more localized:

- `internal/promshim/native/renderer.go`
  - one `fmt.Sprintf` remains only in a panic path for unexpected params from internal sqlb expression rendering
- `internal/promshim/storage/join_sql.go`
  - one `fmt.Sprintf` remains only in a panic path for unexpected params from internal sqlb expression rendering
- `internal/promshim/storage/selector_sql.go`
  - `fmt.Sprintf` remains only for stable placeholder-name generation (`<prefix>_matcher_<n>_(key|value)`) and a panic path for unexpected params from internal sqlb expression rendering
- `internal/promshim/storage/sql.go`
  - `fmt.Sprintf` remains only for stable placeholder-name generation (`selector_<n>_matcher_<m>_(key|value)`) in the labels/series helper matcher path

In other words, the remaining migrated-file `fmt.Sprintf` usage is now helper formatting, not full SQL template assembly.

## Guard scope landed in this slice

The source audit test in `internal/promshim/sql_builder_audit_test.go` now enforces the completed migrated surface more strongly:

- no legacy `strings.ReplaceAll(..., "{value}", ...)` / `"{tags}"` substitution anywhere under `internal/promshim`
- no `fmt.Sprintf`-built full SQL query templates in the completed migrated files:
  - `internal/promshim/storage/join_sql.go`
  - `internal/promshim/native/renderer.go`
  - `internal/promshim/storage/selector_sql.go`
  - `internal/promshim/storage/sql.go`
- no `fmt.Sprintf` at all in those migrated files anymore

This is still narrower than a repo-wide ban, but it now locks the fully migrated promshim SQL surface into the post-retrofit state instead of only guarding against the old full-template style.
