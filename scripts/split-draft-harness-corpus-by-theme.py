#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import sys
from collections import Counter, defaultdict
from pathlib import Path
from typing import Any

DEFAULT_CORPUS = Path("harness/corpus/draft-grafana-top-panel-shortlist.json")
DEFAULT_METADATA = Path("harness/corpus/draft-grafana-top-panel-shortlist.metadata.json")
DEFAULT_OUTPUT_DIR = Path("harness/corpus/draft-grafana-top-panel-shortlist.themes")

GENERIC_FAMILIES = {"panel_query"}


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description=(
            "Split a rendered draft harness corpus into family/theme files using the metadata sidecar. "
            "Duplicates are preserved; the same query can appear in multiple themed files when it belongs to multiple families."
        )
    )
    parser.add_argument(
        "--corpus",
        default=str(DEFAULT_CORPUS),
        help=f"Rendered draft harness corpus JSON (default: {DEFAULT_CORPUS})",
    )
    parser.add_argument(
        "--metadata",
        default=str(DEFAULT_METADATA),
        help=f"Rendered draft metadata sidecar JSON (default: {DEFAULT_METADATA})",
    )
    parser.add_argument(
        "--output-dir",
        default=str(DEFAULT_OUTPUT_DIR),
        help=f"Output directory for themed files (default: {DEFAULT_OUTPUT_DIR})",
    )
    parser.add_argument(
        "--tiers",
        default="",
        help=(
            "Optional comma-separated tiers to include, e.g. promote_now,promote_next. "
            "Default: include all tiers present in metadata."
        ),
    )
    parser.add_argument(
        "--include-generic",
        action="store_true",
        help="Also emit generic families like panel_query",
    )
    parser.add_argument(
        "--include-misc",
        action="store_true",
        help="Also emit a misc.json file for entries with no non-generic families",
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


def safe_theme_name(name: str) -> str:
    return name.replace("_", "-")


def main() -> int:
    args = parse_args()

    corpus_path = Path(args.corpus)
    metadata_path = Path(args.metadata)
    output_dir = Path(args.output_dir)
    output_dir.mkdir(parents=True, exist_ok=True)
    for existing in output_dir.glob("*.json"):
        existing.unlink()

    if not corpus_path.exists():
        print(f"Error: corpus file not found: {corpus_path}", file=sys.stderr)
        return 1
    if not metadata_path.exists():
        print(f"Error: metadata file not found: {metadata_path}", file=sys.stderr)
        return 1

    corpus = read_json(corpus_path)
    metadata_payload = read_json(metadata_path)

    if not isinstance(corpus, list):
        print(f"Error: expected array in corpus file: {corpus_path}", file=sys.stderr)
        return 1
    if not isinstance(metadata_payload, dict) or not isinstance(metadata_payload.get("entries"), list):
        print(f"Error: expected metadata sidecar with entries[] in {metadata_path}", file=sys.stderr)
        return 1

    requested_tiers = {item.strip() for item in args.tiers.split(",") if item.strip()}

    metadata_by_name: dict[str, dict[str, Any]] = {}
    for item in metadata_payload["entries"]:
        if not isinstance(item, dict):
            continue
        name = item.get("name")
        if isinstance(name, str) and name:
            metadata_by_name[name] = item

    theme_queries: dict[str, list[dict[str, Any]]] = defaultdict(list)
    theme_metadata: dict[str, list[dict[str, Any]]] = defaultdict(list)
    theme_counts: Counter[str] = Counter()
    dataset_variant_counts: Counter[str] = Counter()
    skipped_missing_metadata = 0
    skipped_tier = 0
    misc_entries: list[tuple[dict[str, Any], dict[str, Any]]] = []

    for spec in corpus:
        if not isinstance(spec, dict):
            continue
        name = spec.get("name")
        if not isinstance(name, str) or not name:
            continue

        metadata = metadata_by_name.get(name)
        if metadata is None:
            skipped_missing_metadata += 1
            continue

        tier = metadata.get("tier")
        if requested_tiers and tier not in requested_tiers:
            skipped_tier += 1
            continue

        families = list(metadata.get("families") or [])
        if not args.include_generic:
            families = [family for family in families if family not in GENERIC_FAMILIES]

        if not families:
            misc_entries.append((spec, metadata))
            continue

        for dataset_variant in spec.get("datasetVariants") or []:
            dataset_variant_counts[dataset_variant] += 1

        for family in families:
            theme_queries[family].append(spec)
            theme_metadata[family].append(metadata)
            theme_counts[family] += 1

    written_files: list[str] = []
    for family in sorted(theme_queries.keys()):
        safe_name = safe_theme_name(family)
        query_path = output_dir / f"{safe_name}.json"
        metadata_sidecar_path = output_dir / f"{safe_name}.metadata.json"

        write_json(query_path, theme_queries[family])
        theme_dataset_variant_counts = Counter()
        for spec in theme_queries[family]:
            for dataset_variant in spec.get("datasetVariants") or []:
                theme_dataset_variant_counts[dataset_variant] += 1
        write_json(
            metadata_sidecar_path,
            {
                "theme": family,
                "queryCount": len(theme_queries[family]),
                "datasetVariantCounts": dict(theme_dataset_variant_counts),
                "entries": theme_metadata[family],
            },
        )
        written_files.append(str(query_path))
        written_files.append(str(metadata_sidecar_path))

    misc_written = False
    if args.include_misc and misc_entries:
        misc_query_path = output_dir / "misc.json"
        misc_metadata_path = output_dir / "misc.metadata.json"
        write_json(misc_query_path, [spec for spec, _ in misc_entries])
        write_json(
            misc_metadata_path,
            {
                "theme": "misc",
                "queryCount": len(misc_entries),
                "entries": [metadata for _, metadata in misc_entries],
            },
        )
        written_files.append(str(misc_query_path))
        written_files.append(str(misc_metadata_path))
        misc_written = True

    summary = {
        "corpus": str(corpus_path),
        "metadata": str(metadata_path),
        "outputDir": str(output_dir),
        "duplicatesPolicy": "preserved_entries_can_appear_in_multiple_theme_files",
        "requestedTiers": sorted(requested_tiers),
        "includeGeneric": args.include_generic,
        "includeMisc": args.include_misc,
        "inputCorpusCount": len(corpus),
        "skippedMissingMetadata": skipped_missing_metadata,
        "skippedTier": skipped_tier,
        "themeCounts": dict(theme_counts),
        "datasetVariantCounts": dict(dataset_variant_counts),
        "miscCount": len(misc_entries),
        "miscWritten": misc_written,
        "writtenFiles": written_files,
    }
    write_json(output_dir / "summary.json", summary)

    print(f"Split {len(corpus) - skipped_missing_metadata - skipped_tier} draft entries into {len(theme_queries)} themes")
    print(f"Summary: {output_dir / 'summary.json'}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
