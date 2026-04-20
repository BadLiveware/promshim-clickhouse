#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import math
import sys
from collections import Counter
from pathlib import Path
from typing import Any

DEFAULT_CANDIDATE_CORPUS = Path("scratch/grafana-prometheus-candidates/candidate-harness-corpus.json")
DEFAULT_REWRITE_PROPOSALS = Path("scratch/grafana-harness-rewrite-drafts/rewrite-proposals.json")
DEFAULT_PANEL_DRAFTS = Path("scratch/grafana-harness-rewrite-drafts/draft-harness-query-candidates.json")
DEFAULT_METADATA_DRAFTS = Path("scratch/grafana-harness-rewrite-drafts/draft-metadata-candidates.json")
DEFAULT_OUTPUT_DIR = Path("scratch/grafana-harness-promotion")

PANEL_FAMILY_WEIGHTS = {
    "selector": 6,
    "aggregation": 8,
    "rate_family": 8,
    "histogram": 7,
    "range_function": 6,
    "vector_matching": 5,
    "set_operator": 4,
    "absence": 6,
    "subquery": 3,
    "comparison": 2,
    "binary_arithmetic": 2,
    "offset_modifier": 1,
    "at_modifier": 1,
}

METADATA_FAMILY_WEIGHTS = {
    "metadata_variable": 10,
    "selector": 3,
    "aggregation": 2,
    "histogram": 1,
    "comparison": 1,
}

REVIEW_NOTE_PENALTIES = {
    "metric_mapping_is_lossy_many_source_metrics_to_one_harness_metric": 10,
    "query_contains_unmapped_labels_after_initial_rewrite": 10,
    "query_contains_unresolved_grafana_variables_after_initial_rewrite": 10,
    "draft_runnable_query_required_dropping_unmapped_labels": 8,
    "rate_family_over_subquery_is_currently_explicitly_unsupported": 25,
}


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description=(
            "Rank draft harness rewrite candidates into a promote-first shortlist. "
            "This script intentionally preserves duplicates and repeated shapes; it only prioritizes them."
        )
    )
    parser.add_argument("--candidate-corpus", default=str(DEFAULT_CANDIDATE_CORPUS), help=f"Candidate corpus JSON (default: {DEFAULT_CANDIDATE_CORPUS})")
    parser.add_argument("--rewrite-proposals", default=str(DEFAULT_REWRITE_PROPOSALS), help=f"Rewrite proposals JSON (default: {DEFAULT_REWRITE_PROPOSALS})")
    parser.add_argument("--panel-drafts", default=str(DEFAULT_PANEL_DRAFTS), help=f"Panel draft candidates JSON (default: {DEFAULT_PANEL_DRAFTS})")
    parser.add_argument("--metadata-drafts", default=str(DEFAULT_METADATA_DRAFTS), help=f"Metadata draft candidates JSON (default: {DEFAULT_METADATA_DRAFTS})")
    parser.add_argument("--output-dir", default=str(DEFAULT_OUTPUT_DIR), help=f"Output directory (default: {DEFAULT_OUTPUT_DIR})")
    parser.add_argument("--top-panels", type=int, default=100, help="How many ranked panel candidates to copy into the top shortlist (default: 100)")
    parser.add_argument("--top-metadata", type=int, default=30, help="How many ranked metadata candidates to copy into the top shortlist (default: 30)")
    return parser.parse_args()


def read_json(path: Path) -> Any:
    with path.open("r", encoding="utf-8") as fh:
        return json.load(fh)


def write_json(path: Path, payload: Any) -> None:
    with path.open("w", encoding="utf-8") as fh:
        json.dump(payload, fh, indent=2, sort_keys=True)
        fh.write("\n")


def score_occurrence(count: int) -> float:
    if count <= 0:
        return 0.0
    return min(18.0, math.log2(count + 1) * 3.5)


def score_families(families: list[str], metadata: bool) -> float:
    weights = METADATA_FAMILY_WEIGHTS if metadata else PANEL_FAMILY_WEIGHTS
    return float(sum(weights.get(family, 0) for family in families))


def penalty_for_notes(notes: list[str]) -> float:
    total = 0.0
    for note in notes:
        total += REVIEW_NOTE_PENALTIES.get(note, 4)
    return total


def tier(score: float, confidence: float, notes: list[str], runnable_without_drops: bool, metadata: bool) -> str:
    if any("unsupported" in note for note in notes):
        return "blocked"
    if confidence >= 0.9 and not notes and runnable_without_drops:
        return "promote_now"
    if score >= 92 and confidence >= 0.8 and len(notes) <= 1:
        return "promote_next"
    if score >= 80:
        return "strong_candidate"
    if score >= 68:
        return "review_candidate"
    return "long_tail"


def rank_entries(
    drafts: list[dict[str, Any]],
    candidates: dict[str, dict[str, Any]],
    proposals: dict[str, dict[str, Any]],
    metadata: bool,
) -> list[dict[str, Any]]:
    ranked: list[dict[str, Any]] = []

    for draft in drafts:
        candidate_id = str(draft.get("sourceCandidateId") or "")
        candidate = candidates.get(candidate_id, {})
        proposal = proposals.get(candidate_id, {})

        confidence = float(draft.get("mappingConfidence") or 0.0)
        families = list(draft.get("families") or candidate.get("families") or [])
        occurrence_count = int(candidate.get("occurrenceCount") or 0)
        dashboard_count = int(candidate.get("dashboardCount") or 0)
        source_count = int(candidate.get("sourceCount") or 0)
        notes = list(draft.get("reviewNotes") or proposal.get("reviewNotes") or [])
        runnable_without_drops = bool(proposal.get("runnableWithoutDrops", False))
        runnable_with_drops = bool(proposal.get("runnableWithDrops", False))

        score = 0.0
        score += confidence * 100.0
        score += score_occurrence(occurrence_count)
        score += min(8.0, dashboard_count * 0.75)
        score += min(6.0, source_count * 0.15)
        score += score_families(families, metadata)
        if runnable_without_drops:
            score += 6.0
        elif runnable_with_drops:
            score += 2.0
        score -= penalty_for_notes(notes)

        entry = {
            "candidateId": candidate_id,
            "score": round(score, 3),
            "tier": tier(score, confidence, notes, runnable_without_drops, metadata),
            "mappingConfidence": confidence,
            "occurrenceCount": occurrence_count,
            "dashboardCount": dashboard_count,
            "sourceCount": source_count,
            "families": families,
            "reviewNotes": notes,
            "runnableWithoutDrops": runnable_without_drops,
            "runnableWithDrops": runnable_with_drops,
            "draft": draft,
            "candidate": {
                "representativeQuery": candidate.get("representativeQuery"),
                "retargetingHint": candidate.get("retargetingHint"),
                "shapeFingerprint": candidate.get("shapeFingerprint"),
                "metricIdentifiers": candidate.get("metricIdentifiers"),
                "templateVariables": candidate.get("templateVariables"),
                "exampleSources": candidate.get("exampleSources"),
                "topQueryVariants": candidate.get("topQueryVariants"),
            },
            "proposal": {
                "originalQuery": proposal.get("originalQuery"),
                "draftRunnableQuery": proposal.get("draftRunnableQuery"),
                "metricMappings": proposal.get("metricMappings"),
                "selectorLabelChanges": proposal.get("selectorLabelChanges"),
                "groupingLabelChanges": proposal.get("groupingLabelChanges"),
                "labelValuesChanges": proposal.get("labelValuesChanges"),
                "variableReplacements": proposal.get("variableReplacements"),
            },
        }
        ranked.append(entry)

    ranked.sort(
        key=lambda item: (
            -item["score"],
            -item["mappingConfidence"],
            -item["occurrenceCount"],
            item["candidateId"],
        )
    )

    for index, item in enumerate(ranked, start=1):
        item["rank"] = index

    return ranked


def summarize_tiers(items: list[dict[str, Any]]) -> dict[str, int]:
    return dict(Counter(item["tier"] for item in items))


def main() -> int:
    args = parse_args()

    candidate_corpus_path = Path(args.candidate_corpus)
    rewrite_proposals_path = Path(args.rewrite_proposals)
    panel_drafts_path = Path(args.panel_drafts)
    metadata_drafts_path = Path(args.metadata_drafts)
    output_dir = Path(args.output_dir)
    output_dir.mkdir(parents=True, exist_ok=True)

    for path in (candidate_corpus_path, rewrite_proposals_path, panel_drafts_path, metadata_drafts_path):
        if not path.exists():
            print(f"Error: required input file not found: {path}", file=sys.stderr)
            return 1

    candidate_corpus = read_json(candidate_corpus_path)
    rewrite_proposals = read_json(rewrite_proposals_path)
    panel_drafts = read_json(panel_drafts_path)
    metadata_drafts = read_json(metadata_drafts_path)

    if not isinstance(candidate_corpus, list) or not isinstance(rewrite_proposals, list) or not isinstance(panel_drafts, list) or not isinstance(metadata_drafts, list):
        print("Error: one or more input JSON files had an unexpected shape", file=sys.stderr)
        return 1

    candidates_by_id = {str(item.get("id")): item for item in candidate_corpus if isinstance(item, dict) and item.get("id")}
    proposals_by_id = {str(item.get("candidateId")): item for item in rewrite_proposals if isinstance(item, dict) and item.get("candidateId")}

    ranked_panels = rank_entries(panel_drafts, candidates_by_id, proposals_by_id, metadata=False)
    ranked_metadata = rank_entries(metadata_drafts, candidates_by_id, proposals_by_id, metadata=True)

    top_panels = ranked_panels[: args.top_panels] if args.top_panels > 0 else ranked_panels
    top_metadata = ranked_metadata[: args.top_metadata] if args.top_metadata > 0 else ranked_metadata

    panel_ranked_path = output_dir / "ranked-panel-shortlist.json"
    metadata_ranked_path = output_dir / "ranked-metadata-shortlist.json"
    top_panel_path = output_dir / "top-panel-shortlist.json"
    top_metadata_path = output_dir / "top-metadata-shortlist.json"
    summary_path = output_dir / "summary.json"

    write_json(panel_ranked_path, ranked_panels)
    write_json(metadata_ranked_path, ranked_metadata)
    write_json(top_panel_path, top_panels)
    write_json(top_metadata_path, top_metadata)

    summary = {
        "inputs": {
            "candidateCorpus": str(candidate_corpus_path),
            "rewriteProposals": str(rewrite_proposals_path),
            "panelDrafts": str(panel_drafts_path),
            "metadataDrafts": str(metadata_drafts_path),
        },
        "outputDir": str(output_dir),
        "duplicatesPolicy": "preserved_intentionally_no_dedup_against_existing_corpus_or_within_shortlist",
        "panelCount": len(ranked_panels),
        "metadataCount": len(ranked_metadata),
        "topPanelsCount": len(top_panels),
        "topMetadataCount": len(top_metadata),
        "panelTierCounts": summarize_tiers(ranked_panels),
        "metadataTierCounts": summarize_tiers(ranked_metadata),
        "topPanels": [
            {
                "rank": item["rank"],
                "candidateId": item["candidateId"],
                "score": item["score"],
                "tier": item["tier"],
                "mappingConfidence": item["mappingConfidence"],
                "occurrenceCount": item["occurrenceCount"],
                "query": item["draft"].get("query"),
                "families": item["families"],
            }
            for item in top_panels[:20]
        ],
        "topMetadata": [
            {
                "rank": item["rank"],
                "candidateId": item["candidateId"],
                "score": item["score"],
                "tier": item["tier"],
                "mappingConfidence": item["mappingConfidence"],
                "occurrenceCount": item["occurrenceCount"],
                "query": item["draft"].get("query"),
                "families": item["families"],
            }
            for item in top_metadata[:20]
        ],
    }
    write_json(summary_path, summary)

    print(f"Ranked {len(ranked_panels)} panel draft candidates")
    print(f"Ranked {len(ranked_metadata)} metadata draft candidates")
    print(f"Top panel shortlist: {top_panel_path}")
    print(f"Top metadata shortlist: {top_metadata_path}")
    print(f"Summary: {summary_path}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
