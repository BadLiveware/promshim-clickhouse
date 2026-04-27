#!/usr/bin/env python3
"""
Dump every dashboard from a running Grafana instance and extract any
PromQL query found inside them.

Auth: cookie-only (e.g. for an IAP-fronted Grafana). Pass the cookie via
      --cookie or the GRAFANA_COOKIE env var. The value can be either a
      raw Cookie header ("a=1; b=2") or a single token, in which case it
      is sent as `GCP_IAAP_AUTH_TOKEN=<value>` (override the cookie name
      with --cookie-name).

Usage:
    GRAFANA_URL=https://grafana.example.com \
    GRAFANA_COOKIE="$(pbpaste)" \
        ./scripts/dump-grafana-promql.py [--out harness/artifacts/grafana/promql-dump]

Output layout (under --out, default harness/artifacts/grafana/promql-dump):
    dashboards/<folder>/<uid>__<slug>.json   raw dashboard JSON, one per file
    queries.jsonl                            one JSON object per query
    queries.csv                              same data, spreadsheet-friendly
"""

from __future__ import annotations

import argparse
import csv
import json
import os
import re
import sys
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path
from typing import Any, Iterable, Iterator


# ---------------------------------------------------------------------------
# HTTP
# ---------------------------------------------------------------------------

def build_cookie_header(raw: str, cookie_name: str) -> str:
    """Accept either a full Cookie header or a bare token value."""
    raw = raw.strip()
    if not raw:
        sys.exit("error: empty cookie value")
    # If it already looks like a Cookie header (contains '='), pass through.
    if "=" in raw:
        return raw
    return f"{cookie_name}={raw}"


def grafana_get(base: str, path: str, headers: dict[str, str]) -> Any:
    url = base.rstrip("/") + path
    req = urllib.request.Request(url, headers={**headers, "Accept": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            return json.load(resp)
    except urllib.error.HTTPError as e:
        body = e.read().decode("utf-8", "replace")[:400]
        sys.exit(f"error: GET {url} -> {e.code} {e.reason}\n{body}")
    except urllib.error.URLError as e:
        sys.exit(f"error: GET {url} -> {e.reason}")


def iter_dashboard_index(base: str, headers: dict[str, str]) -> Iterator[dict[str, Any]]:
    """Page through /api/search and yield every dashboard summary."""
    page = 1
    while True:
        items = grafana_get(
            base,
            f"/api/search?type=dash-db&limit=5000&page={page}",
            headers,
        )
        if not items:
            return
        yield from items
        if len(items) < 5000:
            return
        page += 1


# ---------------------------------------------------------------------------
# PromQL extraction
# ---------------------------------------------------------------------------

# Datasource type strings that mean "this target's expr is PromQL".
# Grafana sometimes records the type, sometimes just the uid/name.
PROM_TYPES = {"prometheus", "grafana-prometheus-datasource"}

# Heuristic for non-typed targets: things that look PromQL-ish.
PROMQL_HINTS = re.compile(
    r"\b(rate|irate|increase|sum|avg|max|min|count|histogram_quantile|"
    r"label_replace|topk|bottomk|by|without|on|group_left|group_right|"
    r"unless|and|or|offset)\s*\(|\{[^{}]*=~?\"",
    re.IGNORECASE,
)


def iter_panels(panel: dict[str, Any]) -> Iterator[dict[str, Any]]:
    """Walk a panel tree (rows / collapsed rows nest sub-panels)."""
    yield panel
    for child in panel.get("panels", []) or []:
        yield from iter_panels(child)


def is_prometheus_target(target: dict[str, Any], dashboard: dict[str, Any]) -> bool:
    ds = target.get("datasource")
    if isinstance(ds, dict):
        t = (ds.get("type") or "").lower()
        if t in PROM_TYPES:
            return True
        if t and t not in PROM_TYPES:
            # Explicitly typed as something else (loki, cloudwatch, ...).
            return False
    elif isinstance(ds, str) and ds:
        # Legacy: name-only datasource. Match obvious prom names.
        if "prom" in ds.lower():
            return True

    # Fall through: no datasource info on the target. Check the panel /
    # dashboard default. If still ambiguous, fall back to a syntax sniff.
    panel_ds = dashboard.get("__panel_ds")
    if isinstance(panel_ds, dict):
        if (panel_ds.get("type") or "").lower() in PROM_TYPES:
            return True

    expr = target.get("expr")
    return bool(expr) and bool(PROMQL_HINTS.search(expr))


def extract_queries(dashboard: dict[str, Any], meta: dict[str, Any]) -> Iterator[dict[str, Any]]:
    uid = dashboard.get("uid") or meta.get("uid")
    title = dashboard.get("title") or meta.get("title")
    folder = meta.get("folderTitle") or "General"

    # Panel queries.
    for panel in (dashboard.get("panels") or []):
        for p in iter_panels(panel):
            p_ds = p.get("datasource")
            tracked = {**dashboard, "__panel_ds": p_ds if isinstance(p_ds, dict) else None}
            for tgt in (p.get("targets") or []):
                expr = tgt.get("expr")
                if not expr:
                    continue
                if not is_prometheus_target(tgt, tracked):
                    continue
                yield {
                    "dashboard_uid": uid,
                    "dashboard_title": title,
                    "folder": folder,
                    "panel_id": p.get("id"),
                    "panel_title": p.get("title"),
                    "panel_type": p.get("type"),
                    "ref_id": tgt.get("refId"),
                    "legend": tgt.get("legendFormat"),
                    "interval": tgt.get("interval"),
                    "datasource": _ds_label(tgt.get("datasource") or p_ds),
                    "source": "panel",
                    "expr": expr,
                }

    # Template variable queries (dashboard variables of type=query against Prometheus).
    for var in ((dashboard.get("templating") or {}).get("list") or []):
        if (var.get("type") or "").lower() != "query":
            continue
        ds = var.get("datasource")
        if isinstance(ds, dict) and (ds.get("type") or "").lower() not in PROM_TYPES:
            continue
        q = var.get("query")
        if isinstance(q, dict):
            q = q.get("query") or q.get("expr")
        if not isinstance(q, str) or not q:
            continue
        yield {
            "dashboard_uid": uid,
            "dashboard_title": title,
            "folder": folder,
            "panel_id": None,
            "panel_title": None,
            "panel_type": "templating",
            "ref_id": var.get("name"),
            "legend": None,
            "interval": None,
            "datasource": _ds_label(ds),
            "source": "variable",
            "expr": q,
        }


def _ds_label(ds: Any) -> str | None:
    if isinstance(ds, dict):
        return ds.get("uid") or ds.get("name") or ds.get("type")
    if isinstance(ds, str):
        return ds
    return None


# ---------------------------------------------------------------------------
# IO
# ---------------------------------------------------------------------------

_SLUG_RE = re.compile(r"[^a-zA-Z0-9._-]+")


def slugify(s: str) -> str:
    return _SLUG_RE.sub("-", s).strip("-").lower() or "dashboard"


def write_dashboard(out: Path, meta: dict[str, Any], payload: dict[str, Any]) -> Path:
    folder = slugify(meta.get("folderTitle") or "general")
    uid = payload.get("uid") or meta.get("uid") or "no-uid"
    title = slugify(payload.get("title") or meta.get("title") or "dashboard")
    target = out / "dashboards" / folder
    target.mkdir(parents=True, exist_ok=True)
    path = target / f"{uid}__{title}.json"
    path.write_text(json.dumps(payload, indent=2, sort_keys=True))
    return path


def write_queries(out: Path, queries: Iterable[dict[str, Any]]) -> tuple[int, Path, Path]:
    jsonl = out / "queries.jsonl"
    csvf = out / "queries.csv"
    rows = list(queries)
    with jsonl.open("w") as f:
        for r in rows:
            f.write(json.dumps(r, ensure_ascii=False) + "\n")
    fields = [
        "dashboard_uid", "dashboard_title", "folder",
        "panel_id", "panel_title", "panel_type",
        "ref_id", "legend", "interval", "datasource",
        "source", "expr",
    ]
    with csvf.open("w", newline="") as f:
        w = csv.DictWriter(f, fieldnames=fields)
        w.writeheader()
        for r in rows:
            w.writerow({k: r.get(k) for k in fields})
    return len(rows), jsonl, csvf


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--url", default=os.environ.get("GRAFANA_URL"),
                    help="Grafana base URL (or GRAFANA_URL env)")
    ap.add_argument("--cookie", default=os.environ.get("GRAFANA_COOKIE"),
                    help="cookie used for auth (raw Cookie header or bare token); "
                         "or set GRAFANA_COOKIE")
    ap.add_argument("--cookie-name", default=os.environ.get("GRAFANA_COOKIE_NAME", "GCP_IAAP_AUTH_TOKEN"),
                    help="cookie name when --cookie is a bare token (default GCP_IAAP_AUTH_TOKEN)")
    ap.add_argument("--out", default="harness/artifacts/grafana/promql-dump", type=Path,
                    help="output directory (default harness/artifacts/grafana/promql-dump)")
    ap.add_argument("--folder", action="append", default=None,
                    help="restrict to one or more folder titles (repeatable)")
    args = ap.parse_args()

    if not args.url:
        sys.exit("error: --url or GRAFANA_URL required")
    if not args.cookie:
        sys.exit("error: --cookie or GRAFANA_COOKIE required")

    headers = {"Cookie": build_cookie_header(args.cookie, args.cookie_name)}
    args.out.mkdir(parents=True, exist_ok=True)

    folders_filter = set(args.folder) if args.folder else None

    print(f"==> listing dashboards from {args.url}", file=sys.stderr)
    index = list(iter_dashboard_index(args.url, headers))
    print(f"    found {len(index)} dashboards", file=sys.stderr)

    queries: list[dict[str, Any]] = []
    fetched = 0
    skipped = 0
    for meta in index:
        if folders_filter and (meta.get("folderTitle") or "General") not in folders_filter:
            skipped += 1
            continue
        uid = meta.get("uid")
        if not uid:
            continue
        payload = grafana_get(args.url, f"/api/dashboards/uid/{urllib.parse.quote(uid)}", headers)
        dashboard = payload.get("dashboard") or {}
        dash_meta = payload.get("meta") or {}
        merged_meta = {**meta, **dash_meta}
        path = write_dashboard(args.out, merged_meta, dashboard)
        fetched += 1
        for q in extract_queries(dashboard, merged_meta):
            queries.append(q)
        print(f"    [{fetched}/{len(index)}] {uid} -> {path.relative_to(args.out)}", file=sys.stderr)

    n, jsonl, csvf = write_queries(args.out, queries)
    print(f"==> wrote {fetched} dashboards (skipped {skipped}), {n} PromQL queries", file=sys.stderr)
    print(f"    {jsonl}", file=sys.stderr)
    print(f"    {csvf}", file=sys.stderr)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
