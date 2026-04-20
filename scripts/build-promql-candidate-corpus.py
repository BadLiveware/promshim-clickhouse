#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import re
import sys
from collections import Counter, defaultdict
from pathlib import Path
from typing import Any, Iterable

DEFAULT_INPUT_DIR = Path("scratch/grafana-prometheus-queries")
DEFAULT_OUTPUT_DIR = Path("scratch/grafana-prometheus-candidates")

IDENT_RE = re.compile(r"[A-Za-z_:][A-Za-z0-9_:]*")
TEMPLATE_VAR_RE = re.compile(r"\$\{[^}]+\}|\$[A-Za-z_][A-Za-z0-9_]*")
STRING_RE = re.compile(r'"(?:\\.|[^"\\])*"')
DURATION_RE = re.compile(r"\b(?:[0-9]+(?:\.[0-9]+)?(?:ms|s|m|h|d|w|y))+\b")
NUMBER_RE = re.compile(r"(?<![A-Za-z_:])(?:\d+\.\d+|\d+)(?![A-Za-z_])")
SUBQUERY_RE = re.compile(r"\[[^\]]*:[^\]]*\]")
RANGE_RE = re.compile(r"\[[^\]]+\]")
COMPARISON_RE = re.compile(r"==|!=|>=|<=|>|<")
ARITHMETIC_RE = re.compile(r"\+|\-|\*|/|%|\^")

PRESERVED_IDENTIFIERS = {
    "sum",
    "count",
    "avg",
    "min",
    "max",
    "topk",
    "bottomk",
    "count_values",
    "quantile",
    "stddev",
    "stdvar",
    "group",
    "by",
    "without",
    "on",
    "ignoring",
    "group_left",
    "group_right",
    "fill",
    "fill_left",
    "fill_right",
    "bool",
    "offset",
    "and",
    "or",
    "unless",
    "rate",
    "irate",
    "increase",
    "delta",
    "idelta",
    "deriv",
    "changes",
    "resets",
    "histogram_quantile",
    "histogram_count",
    "histogram_sum",
    "histogram_avg",
    "histogram_fraction",
    "label_replace",
    "label_join",
    "absent",
    "absent_over_time",
    "sum_over_time",
    "avg_over_time",
    "min_over_time",
    "max_over_time",
    "count_over_time",
    "last_over_time",
    "quantile_over_time",
    "stddev_over_time",
    "stdvar_over_time",
    "label_values",
    "label_names",
    "metrics",
    "query_result",
    "series_query",
    "sort",
    "sort_desc",
    "scalar",
    "vector",
    "time",
    "start",
    "end",
}


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description=(
            "Group extracted Prometheus Grafana queries by heuristic query shape, classify query families, "
            "and emit a candidate harness corpus JSON."
        )
    )
    parser.add_argument(
        "--input-dir",
        default=str(DEFAULT_INPUT_DIR),
        help=f"Directory containing panel-targets.ndjson and variable-queries.ndjson (default: {DEFAULT_INPUT_DIR})",
    )
    parser.add_argument(
        "--output-dir",
        default=str(DEFAULT_OUTPUT_DIR),
        help=f"Output directory for candidate corpus artifacts (default: {DEFAULT_OUTPUT_DIR})",
    )
    parser.add_argument(
        "--min-occurrences",
        type=int,
        default=1,
        help="Keep only shapes seen at least this many times (default: 1)",
    )
    parser.add_argument(
        "--max-examples",
        type=int,
        default=5,
        help="Maximum example sources/query variants to keep per grouped candidate (default: 5)",
    )
    return parser.parse_args()


def normalize_query_text(text: str | None) -> str:
    return " ".join((text or "").split())


def iter_ndjson(path: Path) -> Iterable[dict[str, Any]]:
    with path.open("r", encoding="utf-8") as fh:
        for line in fh:
            line = line.strip()
            if not line:
                continue
            payload = json.loads(line)
            if isinstance(payload, dict):
                yield payload


def mask_string_number_duration_vars(text: str) -> str:
    text = STRING_RE.sub("<str>", text)
    text = TEMPLATE_VAR_RE.sub("<var>", text)
    text = DURATION_RE.sub("<dur>", text)
    text = NUMBER_RE.sub("<num>", text)
    return text


def shape_fingerprint(query: str) -> str:
    masked = mask_string_number_duration_vars(normalize_query_text(query))
    tokens: list[str] = []
    i = 0
    while i < len(masked):
        ch = masked[i]
        if ch.isspace():
            i += 1
            continue
        if masked.startswith("<str>", i):
            tokens.append("<str>")
            i += 5
            continue
        if masked.startswith("<var>", i):
            tokens.append("<var>")
            i += 5
            continue
        if masked.startswith("<dur>", i):
            tokens.append("<dur>")
            i += 5
            continue
        if masked.startswith("<num>", i):
            tokens.append("<num>")
            i += 5
            continue
        match = IDENT_RE.match(masked, i)
        if match:
            token = match.group(0)
            lowered = token.lower()
            tokens.append(lowered if lowered in PRESERVED_IDENTIFIERS else "<id>")
            i = match.end()
            continue
        if masked.startswith("==", i) or masked.startswith("!=", i) or masked.startswith(">=", i) or masked.startswith("<=", i):
            tokens.append(masked[i : i + 2])
            i += 2
            continue
        tokens.append(ch)
        i += 1
    return " ".join(tokens)


def extract_template_variables(query: str) -> list[str]:
    variables: set[str] = set()
    for match in TEMPLATE_VAR_RE.finditer(query):
        text = match.group(0)
        if text.startswith("${"):
            variables.add(text[2:-1])
        else:
            variables.add(text[1:])
    return sorted(variables)


def extract_metric_identifiers(query: str) -> list[str]:
    stripped = STRING_RE.sub("", query)
    stripped = TEMPLATE_VAR_RE.sub("", stripped)
    identifiers: set[str] = set()
    for match in IDENT_RE.finditer(stripped):
        token = match.group(0)
        lowered = token.lower()
        if lowered in PRESERVED_IDENTIFIERS:
            continue
        if token in {"true", "false"}:
            continue
        if "_" not in token and ":" not in token and token not in {"up"}:
            continue

        prev_non_ws = None
        next_non_ws = None

        prev_slice = stripped[: match.start()].rstrip()
        next_slice = stripped[match.end() :].lstrip()
        if prev_slice:
            prev_non_ws = prev_slice[-1]
        if next_slice:
            next_non_ws = next_slice[0]

        is_label_name = prev_non_ws in {"{", ","} and next_non_ws in {"=", "!", "~"}
        if is_label_name:
            continue

        identifiers.add(token)
    return sorted(identifiers)


def classify_query(query: str, kind: str) -> list[str]:
    q = normalize_query_text(query)
    masked = STRING_RE.sub("STRTOKEN", q)
    masked = TEMPLATE_VAR_RE.sub("VARTOKEN", masked)
    lower = masked.lower()
    families: set[str] = set()

    if kind == "templating_variable":
        families.add("templating_variable")
    else:
        families.add("panel_query")

    if lower.startswith(("label_values(", "label_names(", "metrics(", "query_result(", "series_query(")):
        families.add("metadata_variable")

    if "offset" in lower:
        families.add("offset_modifier")
    if "@" in masked:
        families.add("at_modifier")
    if SUBQUERY_RE.search(masked):
        families.add("subquery")
    elif RANGE_RE.search(masked):
        families.add("range_selector")

    if re.search(r"\b(sum|count|avg|min|max|topk|bottomk|count_values|quantile|stddev|stdvar|group)\b", lower):
        families.add("aggregation")

    if re.search(r"\b(rate|irate|increase|delta|idelta|deriv|changes|resets)\b", lower):
        families.add("rate_family")

    if re.search(r"\b(histogram_quantile|histogram_count|histogram_sum|histogram_avg|histogram_fraction)\b", lower) or "_bucket" in lower:
        families.add("histogram")

    if re.search(r"\b(label_join|label_replace)\b", lower):
        families.add("label_mutation")

    if any(token in lower for token in ("group_left", "group_right", "ignoring(", "on(", " fill(", "fill_left(", "fill_right(")):
        families.add("vector_matching")

    if re.search(r"\b(and|or|unless)\b", lower):
        families.add("set_operator")

    if re.search(r"\b(absent|absent_over_time)\b", lower):
        families.add("absence")

    if re.search(r"\b(sum_over_time|avg_over_time|min_over_time|max_over_time|count_over_time|last_over_time|quantile_over_time|stddev_over_time|stdvar_over_time)\b", lower):
        families.add("range_function")

    if " bool " in f" {lower} ":
        families.add("bool_modifier")
    if COMPARISON_RE.search(masked):
        families.add("comparison")
    if ARITHMETIC_RE.search(masked):
        families.add("binary_arithmetic")
    if "{" in masked or IDENT_RE.search(masked):
        families.add("selector")

    return sorted(families)


def retargeting_hint(kind: str, families: set[str], metric_identifiers: set[str]) -> str:
    if kind == "templating_variable":
        if "metadata_variable" in families:
            return "metadata_endpoint_candidate"
        return "manual_review_variable"
    if "absence" in families:
        return "absence_or_staleness_candidate"
    if "histogram" in families and "rate_family" in families:
        return "requires_histogram_counter_fixture"
    if "histogram" in families:
        return "requires_histogram_fixture"
    if "rate_family" in families:
        return "requires_counter_fixture"
    if "vector_matching" in families or "set_operator" in families:
        return "requires_multi_metric_fixture"
    if "subquery" in families or "range_function" in families or "range_selector" in families:
        return "requires_range_fixture"
    if "label_mutation" in families:
        return "label_mutation_candidate"
    if metric_identifiers:
        return "simple_metric_substitution_candidate"
    return "manual_review"


def sample_source(row: dict[str, Any]) -> dict[str, Any]:
    result = {
        "dashboard_title": row.get("dashboard_title"),
        "dashboard_uid": row.get("dashboard_uid"),
        "file_path": row.get("file_path"),
        "kind": row.get("kind"),
    }
    if row.get("kind") == "panel_target":
        result.update(
            {
                "panel_title": row.get("panel_title"),
                "panel_path": row.get("panel_path"),
                "panel_id": row.get("panel_id"),
                "ref_id": row.get("ref_id"),
            }
        )
    else:
        result.update(
            {
                "variable_name": row.get("variable_name"),
                "variable_type": row.get("variable_type"),
            }
        )
    return result


def write_json(path: Path, payload: Any) -> None:
    with path.open("w", encoding="utf-8") as fh:
        json.dump(payload, fh, indent=2, sort_keys=True)
        fh.write("\n")


def write_ndjson(path: Path, rows: Iterable[dict[str, Any]]) -> None:
    with path.open("w", encoding="utf-8") as fh:
        for row in rows:
            fh.write(json.dumps(row, sort_keys=True))
            fh.write("\n")


def main() -> int:
    args = parse_args()
    if args.min_occurrences < 1:
        print("Error: --min-occurrences must be >= 1", file=sys.stderr)
        return 1
    if args.max_examples < 1:
        print("Error: --max-examples must be >= 1", file=sys.stderr)
        return 1

    input_dir = Path(args.input_dir)
    output_dir = Path(args.output_dir)
    output_dir.mkdir(parents=True, exist_ok=True)

    panel_path = input_dir / "panel-targets.ndjson"
    variable_path = input_dir / "variable-queries.ndjson"
    if not panel_path.exists() and not variable_path.exists():
        print(
            f"Error: neither {panel_path} nor {variable_path} exists. Run scripts/extract-prometheus-grafana-queries.py first.",
            file=sys.stderr,
        )
        return 1

    rows: list[dict[str, Any]] = []
    if panel_path.exists():
        rows.extend(iter_ndjson(panel_path))
    if variable_path.exists():
        rows.extend(iter_ndjson(variable_path))

    groups: dict[tuple[str, str], dict[str, Any]] = {}
    input_stats: Counter[str] = Counter()
    family_occurrences: defaultdict[str, Counter[str]] = defaultdict(Counter)
    family_groups: defaultdict[str, Counter[str]] = defaultdict(Counter)

    for row in rows:
        kind = str(row.get("kind") or "unknown")
        query = normalize_query_text(row.get("query"))
        if not query:
            input_stats["rows_without_query"] += 1
            continue

        input_stats[f"rows:{kind}"] += 1
        shape = shape_fingerprint(query)
        families = classify_query(query, kind)
        metric_identifiers = extract_metric_identifiers(query)
        template_variables = extract_template_variables(query)
        key = (kind, shape)

        if key not in groups:
            groups[key] = {
                "kind": kind,
                "shape_fingerprint": shape,
                "occurrence_count": 0,
                "query_counts": Counter(),
                "families": Counter(),
                "metric_identifiers": Counter(),
                "template_variables": Counter(),
                "dashboard_uids": set(),
                "source_keys": set(),
                "datasource_resolution_counts": Counter(),
                "confidence_counts": Counter(),
                "sources": [],
            }

        group = groups[key]
        group["occurrence_count"] += 1
        group["query_counts"][query] += 1
        group["dashboard_uids"].add(str(row.get("dashboard_uid") or ""))

        source_key = (
            row.get("dashboard_uid"),
            row.get("panel_title"),
            row.get("variable_name"),
            row.get("ref_id"),
            row.get("file_path"),
        )
        group["source_keys"].add(source_key)
        group["datasource_resolution_counts"][str(row.get("datasource_resolution") or "unknown")] += 1
        group["confidence_counts"][str(row.get("confidence") or "unknown")] += 1

        for family in families:
            group["families"][family] += 1
        for metric in metric_identifiers:
            group["metric_identifiers"][metric] += 1
        for variable in template_variables:
            group["template_variables"][variable] += 1

        source_info = sample_source(row)
        if len(group["sources"]) < args.max_examples:
            if source_info not in group["sources"]:
                group["sources"].append(source_info)

    candidates: list[dict[str, Any]] = []
    for key, group in groups.items():
        if group["occurrence_count"] < args.min_occurrences:
            continue

        representative_query, representative_count = sorted(
            group["query_counts"].items(), key=lambda item: (-item[1], len(item[0]), item[0])
        )[0]
        family_names = sorted(group["families"].keys())
        metric_names = [name for name, _ in group["metric_identifiers"].most_common(args.max_examples)]
        template_vars = [name for name, _ in group["template_variables"].most_common(args.max_examples)]
        hint = retargeting_hint(group["kind"], set(family_names), set(metric_names))

        candidate = {
            "kind": group["kind"],
            "shapeFingerprint": group["shape_fingerprint"],
            "representativeQuery": representative_query,
            "representativeQueryCount": representative_count,
            "occurrenceCount": group["occurrence_count"],
            "uniqueQueryVariantCount": len(group["query_counts"]),
            "dashboardCount": len([uid for uid in group["dashboard_uids"] if uid]),
            "sourceCount": len(group["source_keys"]),
            "families": family_names,
            "metricIdentifiers": metric_names,
            "templateVariables": template_vars,
            "retargetingHint": hint,
            "datasourceResolutionCounts": dict(group["datasource_resolution_counts"]),
            "confidenceCounts": dict(group["confidence_counts"]),
            "topQueryVariants": [
                {"query": query, "count": count}
                for query, count in group["query_counts"].most_common(args.max_examples)
            ],
            "exampleSources": group["sources"],
        }
        candidates.append(candidate)

        for family in family_names:
            family_groups[group["kind"]][family] += 1
            family_occurrences[group["kind"]][family] += group["occurrence_count"]

    candidates.sort(
        key=lambda item: (
            -item["occurrenceCount"],
            item["kind"],
            item["representativeQuery"],
        )
    )

    for index, candidate in enumerate(candidates, start=1):
        candidate["id"] = f"cand-{index:04d}"

    summary = {
        "input_dir": str(input_dir),
        "output_dir": str(output_dir),
        "min_occurrences": args.min_occurrences,
        "input_stats": dict(input_stats),
        "candidateCount": len(candidates),
        "candidateCountsByKind": dict(Counter(candidate["kind"] for candidate in candidates)),
        "familyGroupCounts": {kind: dict(counter) for kind, counter in family_groups.items()},
        "familyOccurrenceCounts": {kind: dict(counter) for kind, counter in family_occurrences.items()},
        "topCandidates": [
            {
                "id": candidate["id"],
                "kind": candidate["kind"],
                "occurrenceCount": candidate["occurrenceCount"],
                "families": candidate["families"],
                "representativeQuery": candidate["representativeQuery"],
            }
            for candidate in candidates[: min(20, len(candidates))]
        ],
    }

    corpus_json = output_dir / "candidate-harness-corpus.json"
    corpus_ndjson = output_dir / "candidate-harness-corpus.ndjson"
    summary_json = output_dir / "summary.json"

    write_json(corpus_json, candidates)
    write_ndjson(corpus_ndjson, candidates)
    write_json(summary_json, summary)

    print(f"Loaded {len(rows)} extracted Prometheus query rows from {input_dir}")
    print(f"Wrote {len(candidates)} candidate shapes to {corpus_json}")
    print(f"Summary: {summary_json}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
