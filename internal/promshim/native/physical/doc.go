// Package physical centralizes ClickHouse-native physical-shape decisions.
//
// The package deliberately does not build SQL. It records and selects the
// physical strategies that renderer and storage code already know how to emit.
// Keeping these choices typed and testable makes later optimizer work easier
// without mixing it into logical PromQL rewrites.
//
// Current decision inventory:
//   - Range instant selectors default to ASOF joins. Parents may request a
//     bucketed argMax strategy; storage accepts it only for instant-vector
//     selectors whose sparse step timing can be phase-filtered.
//   - Range-window selector functions choose among the existing windowed-array
//     fallback, direct window joins for low-overlap windows, direct grouped
//     aggregates for supported functions, and the cumulative avg path for
//     high-overlap avg_over_time when enabled.
//   - Direct grouped aggregates keep their storage-owned sparse bucket variant
//     for already-supported non-overlap functions; callers still request the
//     direct aggregate builder rather than constructing SQL here.
//   - Native-grid range functions are limited to identity selector inputs,
//     zero-offset positive windows, enabled native-grid support, and the
//     validated function/window set.
//   - Fused range aggregations choose between native-grid sum aggregation,
//     sparse direct rate aggregation, and row-oriented range-window fallback
//     before invoking renderer/storage builders.
//   - Query settings are expressed as typed thread-cap preferences: set a known
//     max_threads policy, or preserve an explicit no-cap preference for shapes
//     where the cap regresses.
package physical
