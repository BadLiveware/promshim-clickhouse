#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import re
import sys
from collections import Counter
from pathlib import Path
from typing import Any

DEFAULT_INPUT = Path("scratch/grafana-prometheus-candidates/candidate-harness-corpus.json")
DEFAULT_OUTPUT_DIR = Path("scratch/grafana-harness-rewrite-drafts")

IDENT_RE = re.compile(r"[A-Za-z_:][A-Za-z0-9_:]*")
STRING_RE = re.compile(r'"(?:\\.|[^"\\])*"')
TEMPLATE_VAR_RE = re.compile(r"\$\{([^}:]+)(?::[^}]+)?\}|\$([A-Za-z_][A-Za-z0-9_]*)")
SELECTOR_BLOCK_RE = re.compile(r"\{([^{}]*)\}")
GROUPING_RE = re.compile(r"\b(by|without|on|ignoring|group_left|group_right)\s*\(([^()]*)\)")
LABEL_MATCHER_RE = re.compile(r"^\s*([A-Za-z_][A-Za-z0-9_]*)\s*(=~|!~|!=|=)\s*(.+?)\s*$")
RATE_SUBQUERY_UNSUPPORTED_RE = re.compile(
    r"\b(rate|irate|increase|delta|idelta|deriv|changes)\s*\([^)]*\[[^\]]*:[^\]]*\][^)]*\)",
    re.IGNORECASE,
)

HARNESS_METRICS: dict[str, dict[str, Any]] = {
    "harness_up": {
        "kind": "gauge",
        "description": "binary-ish status/health gauge",
    },
    "harness_queue_depth": {
        "kind": "gauge",
        "description": "generic queue/backlog/depth-style gauge",
    },
    "harness_requests_total": {
        "kind": "counter",
        "description": "generic monotonically increasing request/call counter",
    },
    "harness_request_duration_seconds_bucket": {
        "kind": "histogram_bucket",
        "description": "classic histogram bucket series",
    },
    "harness_request_duration_seconds_sum": {
        "kind": "histogram_sum",
        "description": "classic histogram sum series",
    },
    "harness_request_duration_seconds_count": {
        "kind": "histogram_count",
        "description": "classic histogram count series",
    },
    "harness_sparse_gauge": {
        "kind": "sparse_gauge",
        "description": "low-cadence sparse gauge",
    },
    "harness_disappearing_gauge": {
        "kind": "disappearing_gauge",
        "description": "series that disappears late in the seeded window",
    },
}

HARNESS_LABEL_VALUES = {
    "job": "api",
    "instance": "a",
    "namespace": "blue",
    "pod": "api-a",
    "service": "api",
    "le": "0.5",
}
HARNESS_LABELS = set(HARNESS_LABEL_VALUES.keys())

LABEL_ALIAS_MAP = {
    "job": "job",
    "instance": "instance",
    "namespace": "namespace",
    "pod": "pod",
    "service": "service",
    "le": "le",
    "service_name": "service",
    "app": "service",
    "application": "service",
    "workload": "service",
    "workload_name": "service",
    "http_client": "service",
    "horizontalpodautoscaler": "service",
    "exporter": "service",
    "receiver": "service",
    "component": "service",
    "kubernetes_namespace": "namespace",
    "k8s_namespace_name": "namespace",
    "namespace_name": "namespace",
    "k8s_pod_name": "pod",
    "pod_name": "pod",
    "node": "instance",
    "host": "instance",
    "hostname": "instance",
    "servername": "instance",
    "server": "instance",
    "cluster": "job",
    "cluster_name": "job",
    "redis_job": "job",
}

VAR_DEFAULTS = {
    "__rate_interval": "5m",
    "__interval": "60s",
    "interval": "60s",
    "__interval_ms": "60000",
    "__range": "5m",
    "__range_s": "300",
    "__all": "api",
    "top": "2",
    "limit": "2",
    "k": "2",
    "percentile": "0.9",
    "quantile": "0.9",
    "metric": "rate",
    "metric:value": "rate",
    "suffix_total": "",
    "suffix_seconds": "",
    "grouping": "",
}


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description=(
            "Score candidate PromQL shapes against the seeded harness metrics, propose heuristic rewrites, "
            "and emit a first-draft harness candidate set."
        )
    )
    parser.add_argument(
        "--input",
        default=str(DEFAULT_INPUT),
        help=f"Candidate corpus JSON from build-promql-candidate-corpus.py (default: {DEFAULT_INPUT})",
    )
    parser.add_argument(
        "--output-dir",
        default=str(DEFAULT_OUTPUT_DIR),
        help=f"Output directory (default: {DEFAULT_OUTPUT_DIR})",
    )
    parser.add_argument(
        "--min-confidence",
        type=float,
        default=0.65,
        help="Minimum confidence for inclusion in draft harness query candidates (default: 0.65)",
    )
    parser.add_argument(
        "--max-candidates",
        type=int,
        default=0,
        help="Optional limit on number of proposal rows to process (0 means all)",
    )
    return parser.parse_args()


def normalize_query_text(text: str | None) -> str:
    return " ".join((text or "").split())


def read_json(path: Path) -> Any:
    with path.open("r", encoding="utf-8") as fh:
        return json.load(fh)


def split_top_level(text: str, delimiter: str = ",") -> list[str]:
    parts: list[str] = []
    current: list[str] = []
    depth_paren = 0
    depth_brace = 0
    depth_bracket = 0
    in_string = False
    escaped = False

    for ch in text:
        if escaped:
            current.append(ch)
            escaped = False
            continue
        if ch == "\\":
            current.append(ch)
            if in_string:
                escaped = True
            continue
        if ch == '"':
            current.append(ch)
            in_string = not in_string
            continue
        if not in_string:
            if ch == "(":
                depth_paren += 1
            elif ch == ")":
                depth_paren = max(0, depth_paren - 1)
            elif ch == "{":
                depth_brace += 1
            elif ch == "}":
                depth_brace = max(0, depth_brace - 1)
            elif ch == "[":
                depth_bracket += 1
            elif ch == "]":
                depth_bracket = max(0, depth_bracket - 1)
            elif ch == delimiter and depth_paren == 0 and depth_brace == 0 and depth_bracket == 0:
                parts.append("".join(current).strip())
                current = []
                continue
        current.append(ch)

    tail = "".join(current).strip()
    if tail:
        parts.append(tail)
    return parts


def extract_metric_identifiers(query: str) -> list[str]:
    stripped = STRING_RE.sub("", query)
    stripped = TEMPLATE_VAR_RE.sub("", stripped)
    label_references = extract_used_labels(query)
    identifiers: list[str] = []
    seen: set[str] = set()

    preserved = {
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

    for match in IDENT_RE.finditer(stripped):
        token = match.group(0)
        lowered = token.lower()
        if lowered in preserved:
            continue
        if token in {"true", "false"}:
            continue
        if token in label_references:
            continue
        if "_" not in token and ":" not in token and token not in {"up"}:
            continue

        prev_non_ws = stripped[: match.start()].rstrip()
        next_non_ws = stripped[match.end() :].lstrip()
        prev_ch = prev_non_ws[-1] if prev_non_ws else None
        next_ch = next_non_ws[0] if next_non_ws else None
        if prev_ch in {"{", ","} and next_ch in {"=", "!", "~"}:
            continue

        if token not in seen:
            seen.add(token)
            identifiers.append(token)
    return identifiers


def classify_query(query: str, kind: str) -> set[str]:
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
    if re.search(r"\[[^\]]*:[^\]]*\]", masked):
        families.add("subquery")
    elif re.search(r"\[[^\]]+\]", masked):
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
    if re.search(r"==|!=|>=|<=|>|<", masked):
        families.add("comparison")
    if re.search(r"\+|\-|\*|/|%|\^", masked):
        families.add("binary_arithmetic")
    if "{" in masked or IDENT_RE.search(masked):
        families.add("selector")
    return families


def known_unsupported(query: str) -> list[str]:
    notes: list[str] = []
    if RATE_SUBQUERY_UNSUPPORTED_RE.search(query):
        notes.append("rate_family_over_subquery_is_currently_explicitly_unsupported")
    return notes


def tokenize_metric_name(metric: str) -> set[str]:
    tokenized = metric.lower().replace(":", "_")
    return {part for part in re.split(r"[^a-z0-9]+", tokenized) if part}


def score_metric_to_harness(metric: str, families: set[str]) -> list[dict[str, Any]]:
    tokens = tokenize_metric_name(metric)
    lowered = metric.lower()
    results: list[dict[str, Any]] = []

    for harness_metric in HARNESS_METRICS:
        score = 0.0
        reasons: list[str] = []

        if harness_metric == "harness_request_duration_seconds_bucket":
            if lowered.endswith("_bucket"):
                score += 10
                reasons.append("source_metric_has_bucket_suffix")
            if any(t in lowered for t in ("duration", "latency", "request_duration", "response_time", "response_latency", "seconds_bucket")):
                score += 4
                reasons.append("source_metric_looks_like_duration_histogram")
            if "histogram" in families:
                score += 4
                reasons.append("query_family_uses_histogram_semantics")
        elif harness_metric == "harness_request_duration_seconds_sum":
            if lowered.endswith("_sum"):
                score += 9
                reasons.append("source_metric_has_sum_suffix")
            if any(t in lowered for t in ("duration", "latency", "request_duration", "response_time")):
                score += 4
                reasons.append("source_metric_looks_like_duration_sum")
            if "histogram" in families:
                score += 3
                reasons.append("query_family_uses_histogram_semantics")
        elif harness_metric == "harness_request_duration_seconds_count":
            if lowered.endswith("_count"):
                score += 7
                reasons.append("source_metric_has_count_suffix")
            if any(t in lowered for t in ("duration", "latency", "request_duration", "response_time")):
                score += 4
                reasons.append("source_metric_looks_like_duration_count")
            if "histogram" in families:
                score += 3
                reasons.append("query_family_uses_histogram_semantics")
        elif harness_metric == "harness_requests_total":
            if lowered.endswith("_total"):
                score += 8
                reasons.append("source_metric_has_total_suffix")
            if any(token in tokens for token in ("request", "requests", "call", "calls", "query", "queries", "command", "commands", "transaction", "transactions", "fetch", "fetches", "processed", "events", "messages", "connections")):
                score += 3
                reasons.append("source_metric_looks_like_counter")
            if "rate_family" in families and "histogram" not in families:
                score += 4
                reasons.append("query_uses_rate_family_without_histogram")
        elif harness_metric == "harness_queue_depth":
            if any(token in tokens for token in ("queue", "queued", "waiting", "pending", "backlog", "depth", "active", "current", "memory", "load", "client", "clients", "connections", "replicas", "threads", "pool", "open")):
                score += 5
                reasons.append("source_metric_looks_like_gauge")
            if "selector" in families and "rate_family" not in families and "histogram" not in families:
                score += 1
                reasons.append("generic_gauge_fallback")
        elif harness_metric == "harness_up":
            if any(term in lowered for term in ("_up", "_ready", "_alive", "health", "healthy", "build_info", "probe_success", "status_ready", "_info", "availability")):
                score += 6
                reasons.append("source_metric_looks_like_health_or_info_gauge")
            if lowered == "up":
                score += 8
                reasons.append("source_metric_is_up")
        elif harness_metric == "harness_sparse_gauge":
            if "absence" in families:
                score += 2
                reasons.append("absence_family_can_use_sparse_fixture")
        elif harness_metric == "harness_disappearing_gauge":
            if "absence" in families:
                score += 3
                reasons.append("absence_family_prefers_disappearing_fixture")
            if "absent_over_time" in lowered:
                score += 2
                reasons.append("absent_over_time_prefers_disappearing_fixture")

        if score == 0 and harness_metric in {"harness_up", "harness_queue_depth", "harness_requests_total"}:
            if "rate_family" in families and harness_metric == "harness_requests_total":
                score += 1.5
                reasons.append("rate_family_generic_fallback")
            elif "histogram" not in families and harness_metric == "harness_queue_depth":
                score += 0.5
                reasons.append("generic_gauge_fallback")

        results.append({"harnessMetric": harness_metric, "score": score, "reasons": reasons})

    results.sort(key=lambda item: (-item["score"], item["harnessMetric"]))
    return results


def confidence_from_scores(scores: list[dict[str, Any]]) -> float:
    if not scores:
        return 0.0
    best = scores[0]["score"]
    second = scores[1]["score"] if len(scores) > 1 else 0.0
    if best <= 0:
        return 0.0
    if best >= 10:
        confidence = 0.95
    elif best >= 8:
        confidence = 0.85
    elif best >= 6:
        confidence = 0.75
    elif best >= 4:
        confidence = 0.65
    elif best >= 2:
        confidence = 0.55
    else:
        confidence = 0.4
    if second >= best - 1:
        confidence -= 0.1
    return max(0.0, min(1.0, confidence))


def choose_metric_mappings(metrics: list[str], families: set[str]) -> list[dict[str, Any]]:
    chosen: list[dict[str, Any]] = []
    for metric in metrics:
        scored = score_metric_to_harness(metric, families)
        best = scored[0]
        chosen.append(
            {
                "sourceMetric": metric,
                "chosenHarnessMetric": best["harnessMetric"],
                "confidence": confidence_from_scores(scored),
                "reasons": best["reasons"],
                "alternatives": scored[:3],
            }
        )
    return chosen


def replace_metrics(query: str, metric_mappings: list[dict[str, Any]]) -> str:
    rewritten = query
    for mapping in sorted(metric_mappings, key=lambda item: len(item["sourceMetric"]), reverse=True):
        source_metric = mapping["sourceMetric"]
        target_metric = mapping["chosenHarnessMetric"]
        rewritten = re.sub(
            rf"(?<![A-Za-z0-9_:]){re.escape(source_metric)}(?![A-Za-z0-9_:])",
            target_metric,
            rewritten,
        )
    return rewritten


def rewrite_selector_blocks(query: str, drop_unmapped: bool) -> tuple[str, list[dict[str, Any]], list[str]]:
    changes: list[dict[str, Any]] = []
    unresolved: list[str] = []

    def repl(match: re.Match[str]) -> str:
        inner = match.group(1)
        items = split_top_level(inner)
        rewritten_items: list[str] = []
        seen_matchers: dict[str, tuple[str, str]] = {}
        for item in items:
            label_match = LABEL_MATCHER_RE.match(item)
            if not label_match:
                rewritten_items.append(item)
                continue
            label, operator, value = label_match.groups()
            mapped = LABEL_ALIAS_MAP.get(label, label if label in HARNESS_LABELS else None)
            if mapped is None:
                unresolved.append(label)
                changes.append({"kind": "selector_label", "action": "dropped" if drop_unmapped else "unmapped", "source": label})
                if drop_unmapped:
                    continue
                rewritten_items.append(item)
                continue
            if mapped != label:
                changes.append({"kind": "selector_label", "action": "mapped", "source": label, "target": mapped})
            signature = (operator, value)
            previous = seen_matchers.get(mapped)
            if previous is not None:
                if previous == signature:
                    changes.append({"kind": "selector_label", "action": "deduped", "source": label, "target": mapped})
                else:
                    changes.append({"kind": "selector_label", "action": "conflict_dropped", "source": label, "target": mapped, "kept": {"operator": previous[0], "value": previous[1]}, "dropped": {"operator": operator, "value": value}})
                continue
            seen_matchers[mapped] = signature
            rewritten_items.append(f"{mapped}{operator}{value}")
        if not rewritten_items:
            return ""
        return "{" + ",".join(rewritten_items) + "}"

    rewritten = SELECTOR_BLOCK_RE.sub(repl, query)
    return rewritten, changes, sorted(set(unresolved))


def rewrite_grouping_clauses(query: str, drop_unmapped: bool) -> tuple[str, list[dict[str, Any]], list[str]]:
    changes: list[dict[str, Any]] = []
    unresolved: list[str] = []

    def repl(match: re.Match[str]) -> str:
        keyword = match.group(1)
        labels = [label.strip() for label in split_top_level(match.group(2)) if label.strip()]
        rewritten_labels: list[str] = []
        seen: set[str] = set()
        for label in labels:
            mapped = LABEL_ALIAS_MAP.get(label, label if label in HARNESS_LABELS else None)
            if mapped is None:
                unresolved.append(label)
                changes.append({"kind": "grouping_label", "action": "dropped" if drop_unmapped else "unmapped", "source": label, "keyword": keyword})
                if drop_unmapped:
                    continue
                mapped = label
            elif mapped != label:
                changes.append({"kind": "grouping_label", "action": "mapped", "source": label, "target": mapped, "keyword": keyword})
            if mapped not in seen:
                seen.add(mapped)
                rewritten_labels.append(mapped)
            else:
                changes.append({"kind": "grouping_label", "action": "deduped", "source": label, "target": mapped, "keyword": keyword})
        if not rewritten_labels:
            if keyword in {"group_left", "group_right"}:
                return keyword
            return ""
        return f"{keyword}({', '.join(rewritten_labels)})"

    rewritten = GROUPING_RE.sub(repl, query)
    rewritten = re.sub(r"\s{2,}", " ", rewritten).strip()
    rewritten = re.sub(r"\(\s+", "(", rewritten)
    rewritten = re.sub(r"\s+\)", ")", rewritten)
    return rewritten, changes, sorted(set(unresolved))


def rewrite_label_values(query: str, drop_unmapped: bool) -> tuple[str, list[dict[str, Any]], list[str]]:
    lower = query.lower()
    if not lower.startswith("label_values(") or not query.endswith(")"):
        return query, [], []

    inner = query[len("label_values(") : -1]
    args = split_top_level(inner)
    if len(args) < 2:
        return query, [], []

    changes: list[dict[str, Any]] = []
    unresolved: list[str] = []
    metric_expr = args[0]
    label_name = args[1].strip()
    mapped = LABEL_ALIAS_MAP.get(label_name, label_name if label_name in HARNESS_LABELS else None)
    if mapped is None:
        unresolved.append(label_name)
        changes.append({"kind": "label_values_label", "action": "dropped" if drop_unmapped else "unmapped", "source": label_name})
        if drop_unmapped:
            mapped = "job"
            changes.append({"kind": "label_values_label", "action": "fallback", "source": label_name, "target": mapped})
        else:
            mapped = label_name
    elif mapped != label_name:
        changes.append({"kind": "label_values_label", "action": "mapped", "source": label_name, "target": mapped})

    remainder = args[2:]
    rebuilt = [metric_expr, mapped, *remainder]
    return f"label_values({', '.join(rebuilt)})", changes, sorted(set(unresolved))


def default_for_variable(var_name: str) -> str | None:
    lowered = var_name.lower()
    if lowered in VAR_DEFAULTS:
        return VAR_DEFAULTS[lowered]
    if lowered.endswith(":value") and lowered.split(":", 1)[0] == "metric":
        return "rate"
    if "rate_interval" in lowered:
        return "5m"
    if lowered in {"__interval_ms", "interval_ms"}:
        return "60000"
    if "interval" in lowered:
        return "60s"
    if lowered in {"__range_s", "range_s"}:
        return "300"
    if "range" in lowered:
        return "5m"
    if any(token in lowered for token in ("percentile", "quantile", "quantilevar")):
        return "0.9"
    if any(token in lowered for token in ("top", "limit", "count")):
        return "2"
    if "namespace" in lowered:
        return HARNESS_LABEL_VALUES["namespace"]
    if any(token in lowered for token in ("service", "workload", "http_client", "client", "app")):
        return HARNESS_LABEL_VALUES["service"]
    if any(token in lowered for token in ("job", "cluster")):
        return HARNESS_LABEL_VALUES["job"]
    if any(token in lowered for token in ("instance", "node", "host", "server")):
        return HARNESS_LABEL_VALUES["instance"]
    if "pod" in lowered:
        return HARNESS_LABEL_VALUES["pod"]
    if lowered == "le":
        return HARNESS_LABEL_VALUES["le"]
    if lowered.startswith("ds_") or "datasource" in lowered:
        return None
    return None


def replace_grafana_variables(query: str) -> tuple[str, list[dict[str, Any]], list[str]]:
    replacements: list[dict[str, Any]] = []
    unresolved: list[str] = []

    def repl(match: re.Match[str]) -> str:
        whole = match.group(0)
        var_name = match.group(1) or match.group(2)
        default = default_for_variable(var_name)
        if default is None:
            unresolved.append(var_name)
            return whole
        replacements.append({"variable": var_name, "replacement": default})
        return default

    rewritten = TEMPLATE_VAR_RE.sub(repl, query)
    return rewritten, replacements, sorted(set(unresolved))


def extract_used_labels(query: str) -> set[str]:
    labels: set[str] = set()
    for block in SELECTOR_BLOCK_RE.finditer(query):
        for item in split_top_level(block.group(1)):
            label_match = LABEL_MATCHER_RE.match(item)
            if label_match:
                labels.add(label_match.group(1))
    for grouping in GROUPING_RE.finditer(query):
        for label in split_top_level(grouping.group(2)):
            label = label.strip()
            if label:
                labels.add(label)
    lower = query.lower()
    if lower.startswith("label_values(") and query.endswith(")"):
        inner = query[len("label_values(") : -1]
        args = split_top_level(inner)
        if len(args) >= 2:
            labels.add(args[1].strip())
    return labels


def overall_mapping_confidence(metric_mappings: list[dict[str, Any]], unresolved_labels: list[str], unresolved_vars: list[str], unsupported: list[str]) -> float:
    if not metric_mappings:
        return 0.0
    confidence = sum(item["confidence"] for item in metric_mappings) / len(metric_mappings)
    if unresolved_labels:
        confidence -= 0.15
    if unresolved_vars:
        confidence -= 0.15
    if unsupported:
        confidence -= 0.3
    return max(0.0, min(1.0, confidence))


def query_name(candidate_id: str, families: set[str]) -> str:
    parts = [candidate_id.replace("-", "_")]
    for family in ("metadata_variable", "histogram", "rate_family", "range_function", "subquery", "vector_matching", "absence", "aggregation", "selector"):
        if family in families:
            parts.append(family)
    return "draft_" + "_".join(parts)


def build_panel_query_spec(candidate_id: str, query: str, families: set[str], confidence: float, notes: list[str]) -> dict[str, Any]:
    spec = {
        "name": query_name(candidate_id, families),
        "sourceCandidateId": candidate_id,
        "endpoint": "query_range",
        "query": query,
        "startOffsetSeconds": 300,
        "endOffsetSeconds": 540,
        "stepSeconds": 60,
        "mappingConfidence": round(confidence, 3),
        "families": sorted(families),
        "reviewNotes": notes,
    }
    if "absence" in families:
        spec["endpoint"] = "query"
        spec["timeOffsetSeconds"] = 540
        spec.pop("startOffsetSeconds", None)
        spec.pop("endOffsetSeconds", None)
        spec.pop("stepSeconds", None)
    return spec


def build_metadata_candidate(candidate_id: str, query: str, confidence: float, notes: list[str]) -> dict[str, Any]:
    return {
        "name": query_name(candidate_id, {"metadata_variable"}),
        "sourceCandidateId": candidate_id,
        "query": query,
        "mappingConfidence": round(confidence, 3),
        "reviewNotes": notes,
        "suggestedEndpoint": "metadata_variable",
    }


def dedupe_preserve(items: list[str]) -> list[str]:
    seen: set[str] = set()
    result: list[str] = []
    for item in items:
        if item not in seen:
            seen.add(item)
            result.append(item)
    return result


def main() -> int:
    args = parse_args()
    if not 0 <= args.min_confidence <= 1:
        print("Error: --min-confidence must be between 0 and 1", file=sys.stderr)
        return 1

    input_path = Path(args.input)
    if not input_path.exists():
        print(f"Error: input file not found: {input_path}", file=sys.stderr)
        return 1

    payload = read_json(input_path)
    if not isinstance(payload, list):
        print(f"Error: expected candidate corpus array in {input_path}", file=sys.stderr)
        return 1

    output_dir = Path(args.output_dir)
    output_dir.mkdir(parents=True, exist_ok=True)

    rewrite_proposals: list[dict[str, Any]] = []
    draft_query_candidates: list[dict[str, Any]] = []
    draft_metadata_candidates: list[dict[str, Any]] = []
    stats: Counter[str] = Counter()

    candidates = payload[: args.max_candidates] if args.max_candidates and args.max_candidates > 0 else payload

    for candidate in candidates:
        candidate_id = str(candidate.get("id") or "candidate")
        kind = str(candidate.get("kind") or "unknown")
        original_query = normalize_query_text(candidate.get("representativeQuery"))
        if not original_query:
            stats["candidates_without_query"] += 1
            continue

        families = classify_query(original_query, kind)
        metrics = extract_metric_identifiers(original_query)
        metric_mappings = choose_metric_mappings(metrics, families)
        unsupported_notes = known_unsupported(original_query)

        rewritten = replace_metrics(original_query, metric_mappings)
        rewritten, selector_changes, unresolved_selector_labels = rewrite_selector_blocks(rewritten, drop_unmapped=False)
        rewritten, grouping_changes, unresolved_grouping_labels = rewrite_grouping_clauses(rewritten, drop_unmapped=False)
        rewritten, label_values_changes, unresolved_label_values = rewrite_label_values(rewritten, drop_unmapped=False)
        rewritten, variable_replacements, unresolved_vars = replace_grafana_variables(rewritten)

        unresolved_labels = dedupe_preserve(unresolved_selector_labels + unresolved_grouping_labels + unresolved_label_values)
        used_labels_after = sorted(extract_used_labels(rewritten))
        remaining_non_harness_labels = sorted(label for label in used_labels_after if label not in HARNESS_LABELS)

        if remaining_non_harness_labels:
            unresolved_labels = dedupe_preserve(unresolved_labels + remaining_non_harness_labels)

        draft_runnable = rewritten
        dropped_selector_changes: list[dict[str, Any]] = []
        dropped_grouping_changes: list[dict[str, Any]] = []
        dropped_label_values_changes: list[dict[str, Any]] = []
        runnable_with_drops = False

        if unresolved_labels:
            draft_runnable, dropped_selector_changes, _ = rewrite_selector_blocks(rewritten, drop_unmapped=True)
            draft_runnable, dropped_grouping_changes, _ = rewrite_grouping_clauses(draft_runnable, drop_unmapped=True)
            draft_runnable, dropped_label_values_changes, _ = rewrite_label_values(draft_runnable, drop_unmapped=True)
            draft_runnable = normalize_query_text(draft_runnable)

        unresolved_after_drop = [label for label in extract_used_labels(draft_runnable) if label not in HARNESS_LABELS]
        unresolved_vars_after_drop = [match.group(1) or match.group(2) for match in TEMPLATE_VAR_RE.finditer(draft_runnable)]
        runnable_without_drops = not unsupported_notes and not unresolved_labels and not unresolved_vars
        runnable_with_drops = not unsupported_notes and not unresolved_after_drop and not unresolved_vars_after_drop

        confidence = overall_mapping_confidence(metric_mappings, unresolved_labels, unresolved_vars, unsupported_notes)
        review_notes = dedupe_preserve(
            unsupported_notes
            + (["metric_mapping_is_lossy_many_source_metrics_to_one_harness_metric"] if len({m["sourceMetric"] for m in metric_mappings}) > len({m["chosenHarnessMetric"] for m in metric_mappings}) else [])
            + (["query_contains_unmapped_labels_after_initial_rewrite"] if unresolved_labels else [])
            + (["query_contains_unresolved_grafana_variables_after_initial_rewrite"] if unresolved_vars else [])
            + (["draft_runnable_query_required_dropping_unmapped_labels"] if runnable_with_drops and not runnable_without_drops else [])
        )

        proposal = {
            "candidateId": candidate_id,
            "kind": kind,
            "families": sorted(families),
            "originalQuery": original_query,
            "metricMappings": metric_mappings,
            "queryAfterMetricRewrite": normalize_query_text(replace_metrics(original_query, metric_mappings)),
            "queryAfterLabelRewrite": normalize_query_text(rewritten),
            "draftRunnableQuery": draft_runnable,
            "mappingConfidence": round(confidence, 3),
            "runnableWithoutDrops": runnable_without_drops,
            "runnableWithDrops": runnable_with_drops,
            "selectorLabelChanges": selector_changes,
            "groupingLabelChanges": grouping_changes,
            "labelValuesChanges": label_values_changes,
            "droppedSelectorLabelChanges": dropped_selector_changes,
            "droppedGroupingLabelChanges": dropped_grouping_changes,
            "droppedLabelValuesChanges": dropped_label_values_changes,
            "variableReplacements": variable_replacements,
            "unresolvedLabels": unresolved_labels,
            "unresolvedGrafanaVariables": unresolved_vars,
            "reviewNotes": review_notes,
        }
        rewrite_proposals.append(proposal)
        stats[f"proposal_kind:{kind}"] += 1

        if confidence < args.min_confidence:
            stats["proposal_below_confidence_threshold"] += 1
            continue
        if not runnable_with_drops:
            stats["proposal_not_runnable_after_rewrite"] += 1
            continue

        if kind == "panel_target":
            draft_query_candidates.append(
                build_panel_query_spec(candidate_id, draft_runnable, families, confidence, review_notes)
            )
            stats["draft_panel_candidates"] += 1
        elif kind == "templating_variable":
            draft_metadata_candidates.append(
                build_metadata_candidate(candidate_id, draft_runnable, confidence, review_notes)
            )
            stats["draft_metadata_candidates"] += 1

    summary = {
        "input": str(input_path),
        "outputDir": str(output_dir),
        "minConfidence": args.min_confidence,
        "processedCandidates": len(candidates),
        "stats": dict(stats),
        "proposalCount": len(rewrite_proposals),
        "draftPanelCandidateCount": len(draft_query_candidates),
        "draftMetadataCandidateCount": len(draft_metadata_candidates),
        "seedMetrics": HARNESS_METRICS,
        "seedLabelValues": HARNESS_LABEL_VALUES,
        "topDraftPanelCandidates": draft_query_candidates[:20],
        "topDraftMetadataCandidates": draft_metadata_candidates[:20],
    }

    proposals_json = output_dir / "rewrite-proposals.json"
    proposals_ndjson = output_dir / "rewrite-proposals.ndjson"
    panel_candidates_json = output_dir / "draft-harness-query-candidates.json"
    metadata_candidates_json = output_dir / "draft-metadata-candidates.json"
    summary_json = output_dir / "summary.json"

    with proposals_json.open("w", encoding="utf-8") as fh:
        json.dump(rewrite_proposals, fh, indent=2, sort_keys=True)
        fh.write("\n")
    with proposals_ndjson.open("w", encoding="utf-8") as fh:
        for row in rewrite_proposals:
            fh.write(json.dumps(row, sort_keys=True))
            fh.write("\n")
    with panel_candidates_json.open("w", encoding="utf-8") as fh:
        json.dump(draft_query_candidates, fh, indent=2, sort_keys=True)
        fh.write("\n")
    with metadata_candidates_json.open("w", encoding="utf-8") as fh:
        json.dump(draft_metadata_candidates, fh, indent=2, sort_keys=True)
        fh.write("\n")
    with summary_json.open("w", encoding="utf-8") as fh:
        json.dump(summary, fh, indent=2, sort_keys=True)
        fh.write("\n")

    print(f"Processed {len(candidates)} candidate shapes from {input_path}")
    print(f"Wrote {len(rewrite_proposals)} rewrite proposals to {proposals_json}")
    print(f"Draft panel candidates: {len(draft_query_candidates)}")
    print(f"Draft metadata candidates: {len(draft_metadata_candidates)}")
    print(f"Summary: {summary_json}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
