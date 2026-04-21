#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import re
import sys
from collections import Counter
from pathlib import Path
from typing import Any

DEFAULT_SHORTLIST = Path("scratch/grafana-harness-promotion/top-panel-shortlist.json")
DEFAULT_OUTPUT = Path("harness/corpus/draft-grafana-top-panel-shortlist.json")
DEFAULT_METADATA_OUTPUT = Path("harness/corpus/draft-grafana-top-panel-shortlist.metadata.json")
DEFAULT_VARIANT_OUTPUT = Path("harness/corpus/draft-grafana-top-panel-shortlist.dataset-variants.json")
DEFAULT_VARIANT_METADATA_OUTPUT = Path("harness/corpus/draft-grafana-top-panel-shortlist.dataset-variants.metadata.json")
ALLOWED_TIERS = {"promote_now", "promote_next", "strong_candidate", "review_candidate", "long_tail", "blocked"}

QUERY_SPEC_KEYS = (
    "name",
    "endpoint",
    "query",
    "timeOffsetSeconds",
    "startOffsetSeconds",
    "endOffsetSeconds",
    "stepSeconds",
    "datasetVariants",
    "excludeDatasetVariants",
)

VALID_VECTOR_MATCHING_RE = re.compile(r"\b(?:on|ignoring)\s*\([^)]*\)\s*group_(?:left|right)(?:\s*\([^)]*\))?\b")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description=(
            "Render ranked Grafana-derived draft harness panel candidates into a QuerySpec JSON file under harness/corpus. "
            "This preserves duplicate candidates and only ensures output query names are unique."
        )
    )
    parser.add_argument(
        "--shortlist",
        default=str(DEFAULT_SHORTLIST),
        help=f"Ranked/top panel shortlist JSON (default: {DEFAULT_SHORTLIST})",
    )
    parser.add_argument(
        "--output",
        default=str(DEFAULT_OUTPUT),
        help=f"Output harness corpus JSON path (default: {DEFAULT_OUTPUT})",
    )
    parser.add_argument(
        "--metadata-output",
        default=str(DEFAULT_METADATA_OUTPUT),
        help=f"Metadata sidecar JSON path (default: {DEFAULT_METADATA_OUTPUT})",
    )
    parser.add_argument(
        "--variant-output",
        default=str(DEFAULT_VARIANT_OUTPUT),
        help=f"Optional variant-focused corpus JSON path (default: {DEFAULT_VARIANT_OUTPUT})",
    )
    parser.add_argument(
        "--variant-metadata-output",
        default=str(DEFAULT_VARIANT_METADATA_OUTPUT),
        help=f"Optional variant-focused metadata sidecar JSON path (default: {DEFAULT_VARIANT_METADATA_OUTPUT})",
    )
    parser.add_argument(
        "--limit",
        type=int,
        default=0,
        help="Optional max number of shortlist rows to render (0 means all)",
    )
    parser.add_argument(
        "--tiers",
        default="promote_now,promote_next,strong_candidate,review_candidate",
        help=(
            "Comma-separated tiers to include. Default: promote_now,promote_next,strong_candidate,review_candidate"
        ),
    )
    return parser.parse_args()


def read_json(path: Path) -> Any:
    with path.open("r", encoding="utf-8") as fh:
        return json.load(fh)


def write_json(path: Path, payload: Any) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("w", encoding="utf-8") as fh:
        json.dump(payload, fh, indent=2, sort_keys=True)
        fh.write("\n")


def unique_name(name: str, seen: Counter[str]) -> str:
    seen[name] += 1
    if seen[name] == 1:
        return name
    return f"{name}__{seen[name]}"


def is_runnable_query_text(query: str) -> bool:
    stripped = str(query or "").strip()
    if not stripped:
        return False
    if stripped.startswith("#"):
        return False
    if ("group_left" in stripped or "group_right" in stripped) and not VALID_VECTOR_MATCHING_RE.search(stripped):
        return False
    return True


def ordered_unique(items: list[str]) -> list[str]:
    seen: set[str] = set()
    out: list[str] = []
    for item in items:
        normalized = str(item).strip()
        if not normalized or normalized in seen:
            continue
        seen.add(normalized)
        out.append(normalized)
    return out


def infer_dataset_variants(row: dict[str, Any], draft: dict[str, Any]) -> tuple[list[str], list[str]]:
    candidate = row.get("candidate") or {}
    families = ordered_unique([*(row.get("families") or []), *(draft.get("families") or [])])
    family_set = {family for family in families}
    hint = str(candidate.get("retargetingHint") or "").strip()

    variants: list[str] = []
    reasons: list[str] = []

    def add(reason: str, *names: str) -> None:
        before = len(variants)
        variants.extend(names)
        deduped = ordered_unique(variants)
        variants[:] = deduped
        if len(variants) != before:
            reasons.append(reason)

    if hint in {"requires_counter_fixture", "requires_histogram_counter_fixture"} or "rate_family" in family_set:
        add("counter_reset_and_gap_coverage", "baseline", "resets_gaps")
    if hint in {"requires_range_fixture", "absence_or_staleness_candidate"} or "absence" in family_set or "range_function" in family_set:
        add("staleness_and_churn_coverage", "baseline", "churn_stale")
    if hint in {"requires_histogram_fixture", "requires_histogram_counter_fixture"} or "histogram" in family_set:
        add("histogram_burst_coverage", "baseline", "histogram_burst")

    return variants, reasons


def build_query_spec(row: dict[str, Any], draft: dict[str, Any], seen_names: Counter[str]) -> tuple[dict[str, Any], list[str]]:
    base_name = str(draft.get("name") or "draft_candidate")
    spec: dict[str, Any] = {
        "name": unique_name(base_name, seen_names),
        "endpoint": draft.get("endpoint"),
        "query": draft.get("query"),
    }
    for key in ("timeOffsetSeconds", "startOffsetSeconds", "endOffsetSeconds", "stepSeconds"):
        value = draft.get(key)
        if value is not None:
            spec[key] = value
    dataset_variants, variant_reasons = infer_dataset_variants(row, draft)
    if dataset_variants:
        spec["datasetVariants"] = dataset_variants
    return spec, variant_reasons


def main() -> int:
    args = parse_args()

    shortlist_path = Path(args.shortlist)
    output_path = Path(args.output)
    metadata_path = Path(args.metadata_output)
    variant_output_path = Path(args.variant_output) if args.variant_output else None
    variant_metadata_path = Path(args.variant_metadata_output) if args.variant_metadata_output else None

    if args.limit < 0:
        print("Error: --limit must be >= 0", file=sys.stderr)
        return 1

    tiers = [item.strip() for item in args.tiers.split(",") if item.strip()]
    if not tiers:
        print("Error: --tiers resolved to an empty set", file=sys.stderr)
        return 1
    unknown_tiers = [tier for tier in tiers if tier not in ALLOWED_TIERS]
    if unknown_tiers:
        print(f"Error: unknown tier(s): {', '.join(unknown_tiers)}", file=sys.stderr)
        return 1

    if not shortlist_path.exists():
        print(f"Error: shortlist file not found: {shortlist_path}", file=sys.stderr)
        return 1

    payload = read_json(shortlist_path)
    if not isinstance(payload, list):
        print(f"Error: expected shortlist array in {shortlist_path}", file=sys.stderr)
        return 1

    selected_rows: list[dict[str, Any]] = []
    for row in payload:
        if not isinstance(row, dict):
            continue
        if row.get("tier") not in tiers:
            continue
        selected_rows.append(row)
        if args.limit and len(selected_rows) >= args.limit:
            break

    seen_names: Counter[str] = Counter()
    query_specs: list[dict[str, Any]] = []
    metadata_rows: list[dict[str, Any]] = []
    skipped_invalid_rows: list[dict[str, Any]] = []

    for row in selected_rows:
        draft = row.get("draft")
        if not isinstance(draft, dict):
            continue
        if not is_runnable_query_text(str(draft.get("query") or "")):
            skipped_invalid_rows.append(
                {
                    "candidateId": row.get("candidateId"),
                    "name": draft.get("name"),
                    "query": draft.get("query"),
                    "reason": "obviously_unrunnable_query_text",
                }
            )
            continue
        spec, dataset_variant_reasons = build_query_spec(row, draft, seen_names)
        query_specs.append(spec)

        metadata_rows.append(
            {
                "name": spec["name"],
                "candidateId": row.get("candidateId"),
                "rank": row.get("rank"),
                "score": row.get("score"),
                "tier": row.get("tier"),
                "mappingConfidence": row.get("mappingConfidence"),
                "occurrenceCount": row.get("occurrenceCount"),
                "dashboardCount": row.get("dashboardCount"),
                "sourceCount": row.get("sourceCount"),
                "families": row.get("families"),
                "reviewNotes": row.get("reviewNotes"),
                "datasetVariants": spec.get("datasetVariants"),
                "datasetVariantReasons": dataset_variant_reasons,
                "draftQuery": draft.get("query"),
                "originalRepresentativeQuery": (row.get("candidate") or {}).get("representativeQuery"),
                "retargetingHint": (row.get("candidate") or {}).get("retargetingHint"),
                "shapeFingerprint": (row.get("candidate") or {}).get("shapeFingerprint"),
                "exampleSources": (row.get("candidate") or {}).get("exampleSources"),
                "topQueryVariants": (row.get("candidate") or {}).get("topQueryVariants"),
                "metricMappings": (row.get("proposal") or {}).get("metricMappings"),
                "selectorLabelChanges": (row.get("proposal") or {}).get("selectorLabelChanges"),
                "groupingLabelChanges": (row.get("proposal") or {}).get("groupingLabelChanges"),
                "labelValuesChanges": (row.get("proposal") or {}).get("labelValuesChanges"),
                "variableReplacements": (row.get("proposal") or {}).get("variableReplacements"),
            }
        )

    variant_query_specs = [spec for spec in query_specs if spec.get("datasetVariants")]
    variant_metadata_rows = [row for row in metadata_rows if row.get("datasetVariants")]
    dataset_variant_counts = Counter()
    for spec in query_specs:
        for dataset_variant in spec.get("datasetVariants", []):
            dataset_variant_counts[dataset_variant] += 1

    summary = {
        "shortlist": str(shortlist_path),
        "output": str(output_path),
        "metadataOutput": str(metadata_path),
        "duplicatesPolicy": "preserved_query_rows_unique_names_only",
        "includedTiers": tiers,
        "limit": args.limit,
        "selectedCount": len(selected_rows),
        "renderedCount": len(query_specs),
        "variantFocusedCount": len(variant_query_specs),
        "skippedInvalidCount": len(skipped_invalid_rows),
        "tierCounts": dict(Counter(row.get("tier") for row in selected_rows)),
        "datasetVariantCounts": dict(dataset_variant_counts),
        "nameCollisionCounts": {name: count for name, count in seen_names.items() if count > 1},
        "firstQueryNames": [spec["name"] for spec in query_specs[:20]],
    }

    write_json(output_path, query_specs)
    write_json(metadata_path, {"summary": summary, "entries": metadata_rows, "skippedInvalid": skipped_invalid_rows})
    if variant_output_path is not None:
        write_json(variant_output_path, variant_query_specs)
    if variant_metadata_path is not None:
        write_json(
            variant_metadata_path,
            {
                "summary": {
                    **summary,
                    "output": str(variant_output_path) if variant_output_path is not None else "",
                    "metadataOutput": str(variant_metadata_path) if variant_metadata_path is not None else "",
                    "renderedCount": len(variant_query_specs),
                    "datasetVariantCounts": dict(dataset_variant_counts),
                },
                "entries": variant_metadata_rows,
                "skippedInvalid": skipped_invalid_rows,
            },
        )

    print(f"Rendered {len(query_specs)} draft harness query specs to {output_path}")
    if variant_output_path is not None:
        print(f"Rendered {len(variant_query_specs)} variant-focused query specs to {variant_output_path}")
    print(f"Metadata sidecar: {metadata_path}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
