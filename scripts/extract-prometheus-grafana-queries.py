#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import re
import sys
from collections import Counter
from pathlib import Path
from typing import Any, Iterable

DEFAULT_INPUT = Path("scratch/grafana-dashboards")
DEFAULT_OUTPUT = Path("scratch/grafana-prometheus-queries")

NON_PROMETHEUS_TYPES = {
    "graphite",
    "stackdriver",
    "cloud-monitoring-datasource",
    "grafana-bigquery-datasource",
    "loki",
    "elasticsearch",
    "grafana",
    "mysql",
    "postgres",
    "tempo",
    "zipkin",
    "jaeger",
    "datasource",
    "__expr__",
}

PROMETHEUS_NAME_HINTS = (
    "prom",
    "thanos",
    "mimir",
    "cortex",
    "victoria",
    "vmselect",
)

NON_PROMETHEUS_NAME_HINTS = (
    "graphite",
    "stackdriver",
    "gcp-monitoring",
    "cloud-monitoring",
    "bigquery",
    "loki",
    "tempo",
    "jaeger",
    "zipkin",
)

PROM_VARIABLE_PREFIXES = (
    "label_values(",
    "label_names(",
    "metrics(",
    "query_result(",
    "series_query(",
)

PROMQL_FUNCTION_HINTS = (
    "sum(",
    "avg(",
    "count(",
    "min(",
    "max(",
    "rate(",
    "irate(",
    "increase(",
    "delta(",
    "idelta(",
    "deriv(",
    "changes(",
    "histogram_quantile(",
    "histogram_count(",
    "histogram_sum(",
    "histogram_avg(",
    "label_replace(",
    "label_join(",
    "topk(",
    "bottomk(",
    "quantile(",
    "absent(",
    "absent_over_time(",
    "sum_over_time(",
    "avg_over_time(",
    "min_over_time(",
    "max_over_time(",
    "count_over_time(",
    "last_over_time(",
)

PROMQL_METRIC_RE = re.compile(r"(?<![$A-Za-z0-9_:])([A-Za-z_:][A-Za-z0-9_:]*)")
TEMPLATE_VAR_RE = re.compile(r"^\$\{([^}]+)\}$|^\$([A-Za-z_][A-Za-z0-9_]*)$")
REF_ONLY_EXPR_RE = re.compile(r"^[\s$A-Z0-9+\-*/%.(),]+$")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description=(
            "Extract Prometheus-only panel and variable queries from Grafana dashboard JSON files. "
            "Reads exported dashboards from scratch/grafana-dashboards by default and writes "
            "deduped query files plus NDJSON metadata."
        )
    )
    parser.add_argument(
        "inputs",
        nargs="*",
        help=(
            "Dashboard JSON file(s) or directory/directories to scan. "
            "Defaults to scratch/grafana-dashboards"
        ),
    )
    parser.add_argument(
        "--output-dir",
        default=str(DEFAULT_OUTPUT),
        help=f"Output directory (default: {DEFAULT_OUTPUT})",
    )
    parser.add_argument(
        "--strict",
        action="store_true",
        help=(
            "Keep only queries whose datasource resolves explicitly to Prometheus. "
            "Drop inferred Prometheus queries."
        ),
    )
    return parser.parse_args()


def load_json(path: Path) -> Any:
    with path.open("r", encoding="utf-8") as fh:
        return json.load(fh)


def iter_input_files(inputs: list[str]) -> Iterable[Path]:
    roots = [Path(item) for item in inputs] if inputs else [DEFAULT_INPUT]
    seen: set[Path] = set()
    for root in roots:
        if root.is_file():
            if root.suffix.lower() == ".json" and root.name != "index.json":
                resolved = root.resolve()
                if resolved not in seen:
                    seen.add(resolved)
                    yield root
            continue
        if not root.exists():
            raise FileNotFoundError(f"Input path not found: {root}")
        for path in sorted(root.rglob("*.json")):
            if path.name == "index.json":
                continue
            resolved = path.resolve()
            if resolved not in seen:
                seen.add(resolved)
                yield path


def sanitize_query_text(text: str | None) -> str:
    if text is None:
        return ""
    return text.strip()


def normalize_query_text(text: str | None) -> str:
    return " ".join(sanitize_query_text(text).split())


def template_var_name(value: Any) -> str | None:
    if not isinstance(value, str):
        return None
    match = TEMPLATE_VAR_RE.match(value.strip())
    if not match:
        return None
    return match.group(1) or match.group(2)


def looks_like_prometheus_variable_query(query: str) -> bool:
    stripped = query.strip()
    lowered = stripped.lower()
    if any(lowered.startswith(prefix) for prefix in PROM_VARIABLE_PREFIXES):
        return True
    return looks_like_promql_expr(stripped)


def looks_like_promql_expr(expr: str) -> bool:
    stripped = expr.strip()
    if not stripped:
        return False

    lowered = stripped.lower()
    if lowered.startswith(("select ", "fetch ", "from ")):
        return False
    if any(lowered.startswith(prefix) for prefix in PROM_VARIABLE_PREFIXES):
        return True
    if any(func in lowered for func in PROMQL_FUNCTION_HINTS):
        return True
    if "{" in stripped or "[" in stripped:
        return True
    if " by (" in lowered or " without (" in lowered or " on(" in lowered or " group_left" in lowered or " group_right" in lowered:
        return True
    if REF_ONLY_EXPR_RE.match(stripped) and not PROMQL_METRIC_RE.search(stripped.replace("$", "")):
        return False
    metric_match = PROMQL_METRIC_RE.search(stripped)
    if metric_match:
        token = metric_match.group(1)
        if token not in {"true", "false", "and", "or", "unless", "bool"}:
            return True
    return False


def merge_datasource(parent: Any, child: Any) -> Any:
    if isinstance(parent, dict) and isinstance(child, dict):
        merged = dict(parent)
        for key, value in child.items():
            if value is not None:
                merged[key] = value
        return merged
    return child if child is not None else parent


def build_datasource_hints(dashboard: dict[str, Any]) -> dict[str, str]:
    hints: dict[str, str] = {}

    for item in dashboard.get("__inputs") or []:
        if item.get("type") == "datasource" and item.get("name") and item.get("pluginId"):
            hints[str(item["name"])] = str(item["pluginId"])

    templating = (dashboard.get("templating") or {}).get("list") or []
    for variable in templating:
        if variable.get("type") != "datasource":
            continue
        query = variable.get("query")
        if not isinstance(query, str) or not query:
            continue
        name = variable.get("name")
        if isinstance(name, str) and name:
            hints[name] = query
        current = variable.get("current") or {}
        for key in ("value", "text"):
            current_value = current.get(key)
            if isinstance(current_value, str) and current_value:
                hints[current_value] = query
    return hints


def resolve_datasource(ds: Any, hints: dict[str, str]) -> tuple[str | None, str, str]:
    ds_repr = json.dumps(ds, sort_keys=True) if isinstance(ds, dict) else repr(ds)

    ds_type: str | None = None
    ds_uid: str | None = None
    if isinstance(ds, dict):
        raw_type = ds.get("type")
        raw_uid = ds.get("uid")
        ds_type = raw_type if isinstance(raw_type, str) and raw_type else None
        ds_uid = raw_uid if isinstance(raw_uid, str) and raw_uid else None
    elif isinstance(ds, str) and ds:
        ds_uid = ds
        if ds in {"prometheus", "graphite", "stackdriver", "loki", "thanos"}:
            ds_type = ds

    if ds_type == "prometheus":
        return "prometheus", "explicit_type", ds_repr
    if ds_type in NON_PROMETHEUS_TYPES:
        return ds_type, "explicit_type", ds_repr

    for candidate in (ds_uid, ds_type):
        if not candidate:
            continue
        var_name = template_var_name(candidate)
        if var_name and var_name in hints:
            return hints[var_name], "templated_hint", ds_repr
        if candidate in hints:
            return hints[candidate], "named_hint", ds_repr

    if ds_uid:
        lowered = ds_uid.lower()
        if any(hint in lowered for hint in NON_PROMETHEUS_NAME_HINTS):
            return "non_prometheus_name_hint", "name_hint", ds_repr
        if any(hint in lowered for hint in PROMETHEUS_NAME_HINTS):
            return "prometheus", "name_hint", ds_repr

    if ds_type == "thanos":
        return "prometheus", "name_hint", ds_repr

    return None, "unresolved", ds_repr


def walk_panels(panels: list[dict[str, Any]] | None, ancestors: list[str] | None = None) -> Iterable[tuple[dict[str, Any], list[str]]]:
    ancestors = ancestors or []
    for panel in panels or []:
        title = panel.get("title")
        title_text = title if isinstance(title, str) and title else "(untitled)"
        current_path = [*ancestors, title_text]
        yield panel, current_path
        nested = panel.get("panels")
        if isinstance(nested, list):
            yield from walk_panels(nested, current_path)


def extract_variable_query(variable: dict[str, Any]) -> str:
    query = variable.get("query")
    if isinstance(query, dict):
        for key in ("query", "expr", "target"):
            value = query.get(key)
            if isinstance(value, str) and value.strip():
                return value.strip()
    elif isinstance(query, str) and query.strip():
        return query.strip()
    definition = variable.get("definition")
    if isinstance(definition, str) and definition.strip():
        return definition.strip()
    return ""


def write_ndjson(path: Path, rows: list[dict[str, Any]]) -> None:
    with path.open("w", encoding="utf-8") as fh:
        for row in rows:
            fh.write(json.dumps(row, sort_keys=True))
            fh.write("\n")


def write_unique_queries(path: Path, queries: Iterable[str]) -> int:
    unique = sorted({normalize_query_text(query) for query in queries if normalize_query_text(query)})
    with path.open("w", encoding="utf-8") as fh:
        for query in unique:
            fh.write(query)
            fh.write("\n")
    return len(unique)


def main() -> int:
    args = parse_args()
    output_dir = Path(args.output_dir)
    output_dir.mkdir(parents=True, exist_ok=True)

    panel_rows: list[dict[str, Any]] = []
    variable_rows: list[dict[str, Any]] = []
    panel_queries: list[str] = []
    variable_queries: list[str] = []

    stats: Counter[str] = Counter()
    panel_reason_counts: Counter[str] = Counter()
    variable_reason_counts: Counter[str] = Counter()
    dashboards_seen: set[str] = set()

    try:
        files = list(iter_input_files(args.inputs))
    except FileNotFoundError as exc:
        print(f"Error: {exc}", file=sys.stderr)
        return 1

    if not files:
        print("Error: no dashboard JSON files found", file=sys.stderr)
        return 1

    for file_path in files:
        stats["files_scanned"] += 1
        try:
            dashboard = load_json(file_path)
        except Exception as exc:  # noqa: BLE001
            print(f"Warning: failed to parse {file_path}: {exc}", file=sys.stderr)
            stats["files_failed"] += 1
            continue

        if not isinstance(dashboard, dict):
            stats["files_skipped_non_dashboard_json"] += 1
            continue

        dashboard_uid = str(dashboard.get("uid") or file_path.stem)
        dashboards_seen.add(dashboard_uid)
        dashboard_title = str(dashboard.get("title") or file_path.stem)
        datasource_hints = build_datasource_hints(dashboard)

        for panel, panel_path in walk_panels(dashboard.get("panels")):
            panel_ds = panel.get("datasource")
            panel_title = panel_path[-1]
            panel_id = panel.get("id")
            for target in panel.get("targets") or []:
                stats["panel_targets_examined"] += 1
                expr = sanitize_query_text(target.get("expr"))
                if not expr:
                    stats["panel_targets_without_expr"] += 1
                    continue

                effective_ds = merge_datasource(panel_ds, target.get("datasource"))
                resolved_type, reason, ds_repr = resolve_datasource(effective_ds, datasource_hints)

                include = False
                confidence = "excluded"
                if resolved_type == "prometheus":
                    include = True
                    confidence = "explicit" if reason == "explicit_type" else "inferred_datasource"
                elif resolved_type in NON_PROMETHEUS_TYPES or resolved_type == "non_prometheus_name_hint":
                    include = False
                elif looks_like_promql_expr(expr):
                    include = not args.strict
                    confidence = "inferred_promql"
                    reason = "promql_shape"

                if not include:
                    stats["panel_targets_excluded"] += 1
                    continue

                row = {
                    "kind": "panel_target",
                    "dashboard_title": dashboard_title,
                    "dashboard_uid": dashboard_uid,
                    "file_path": str(file_path),
                    "panel_id": panel_id,
                    "panel_title": panel_title,
                    "panel_path": panel_path,
                    "ref_id": target.get("refId"),
                    "datasource": ds_repr,
                    "datasource_resolution": reason,
                    "confidence": confidence,
                    "query": expr,
                    "normalized_query": normalize_query_text(expr),
                }
                panel_rows.append(row)
                panel_queries.append(expr)
                panel_reason_counts[f"{confidence}:{reason}"] += 1
                stats["panel_targets_prometheus"] += 1

        for variable in (dashboard.get("templating") or {}).get("list") or []:
            stats["variables_examined"] += 1
            if variable.get("type") != "query":
                stats["variables_non_query_skipped"] += 1
                continue

            query = extract_variable_query(variable)
            if not query:
                stats["variables_without_query"] += 1
                continue

            resolved_type, reason, ds_repr = resolve_datasource(variable.get("datasource"), datasource_hints)
            include = False
            confidence = "excluded"
            if resolved_type == "prometheus":
                include = True
                confidence = "explicit" if reason == "explicit_type" else "inferred_datasource"
            elif resolved_type in NON_PROMETHEUS_TYPES or resolved_type == "non_prometheus_name_hint":
                include = False
            elif looks_like_prometheus_variable_query(query):
                include = not args.strict
                confidence = "inferred_promql"
                reason = "prometheus_variable_shape"

            if not include:
                stats["variables_excluded"] += 1
                continue

            row = {
                "kind": "templating_variable",
                "dashboard_title": dashboard_title,
                "dashboard_uid": dashboard_uid,
                "file_path": str(file_path),
                "variable_name": variable.get("name"),
                "variable_type": variable.get("type"),
                "datasource": ds_repr,
                "datasource_resolution": reason,
                "confidence": confidence,
                "query": query,
                "normalized_query": normalize_query_text(query),
            }
            variable_rows.append(row)
            variable_queries.append(query)
            variable_reason_counts[f"{confidence}:{reason}"] += 1
            stats["variables_prometheus"] += 1

    panel_ndjson = output_dir / "panel-targets.ndjson"
    variable_ndjson = output_dir / "variable-queries.ndjson"
    write_ndjson(panel_ndjson, panel_rows)
    write_ndjson(variable_ndjson, variable_rows)

    panel_unique_count = write_unique_queries(output_dir / "promql-panel-queries.txt", panel_queries)
    variable_unique_count = write_unique_queries(output_dir / "promql-variable-queries.txt", variable_queries)
    all_unique_count = write_unique_queries(output_dir / "promql-all.txt", [*panel_queries, *variable_queries])

    summary = {
        "input_paths": args.inputs or [str(DEFAULT_INPUT)],
        "strict": args.strict,
        "dashboards_scanned": len(dashboards_seen),
        "stats": dict(stats),
        "output_files": {
            "panel_targets": str(panel_ndjson),
            "variable_queries": str(variable_ndjson),
            "panel_query_list": str(output_dir / "promql-panel-queries.txt"),
            "variable_query_list": str(output_dir / "promql-variable-queries.txt"),
            "all_query_list": str(output_dir / "promql-all.txt"),
        },
        "unique_query_counts": {
            "panel": panel_unique_count,
            "variable": variable_unique_count,
            "all": all_unique_count,
        },
        "panel_reason_counts": dict(panel_reason_counts),
        "variable_reason_counts": dict(variable_reason_counts),
    }

    summary_path = output_dir / "summary.json"
    with summary_path.open("w", encoding="utf-8") as fh:
        json.dump(summary, fh, indent=2, sort_keys=True)
        fh.write("\n")

    print(f"Scanned {len(dashboards_seen)} dashboards from {len(files)} JSON files")
    print(f"Prometheus panel targets kept: {len(panel_rows)} ({panel_unique_count} unique queries)")
    print(f"Prometheus variable queries kept: {len(variable_rows)} ({variable_unique_count} unique queries)")
    print(f"Combined unique queries: {all_unique_count}")
    print(f"Summary: {summary_path}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
