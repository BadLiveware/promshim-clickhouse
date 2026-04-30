#!/usr/bin/env python3
"""
Collect a conservative workload-shape profile from a Prometheus server.

The script is intentionally production-friendly by default:

* requests run sequentially;
* a delay is inserted between requests;
* long-window PromQL probes are opt-in;
* lookback windows are capped to 15d by default;
* high-cardinality probes are limited to a small top-k set.

Example:

    PROM_URL=https://prometheus.example.com \
      ./scripts/profile-prometheus-workload.py \
        --out harness/artifacts/prometheus-workload/profile-$(date +%Y%m%d)

Optional, still bounded PromQL probes:

    ./scripts/profile-prometheus-workload.py --url "$PROM_URL" \
      --include-promql --max-top-metrics 15 --density-metrics 5
"""

from __future__ import annotations

import argparse
import datetime as dt
import json
import math
import os
import re
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path
from typing import Any


DEFAULT_LABELS = ["job", "namespace", "instance"]
DEFAULT_WINDOWS = ["1h", "24h"]
HISTOGRAM_RE = re.compile(r"_bucket$")
COUNTER_RE = re.compile(r"(_total|_count|_sum)$")


class PromClient:
    def __init__(self, base: str, headers: dict[str, str], timeout: float, delay: float, out: Path):
        self.base = base.rstrip("/")
        self.headers = headers
        self.timeout = timeout
        self.delay = delay
        self.out = out
        self.requests: list[dict[str, Any]] = []
        self._last_request = 0.0

    def _sleep_if_needed(self) -> None:
        elapsed = time.monotonic() - self._last_request
        if self._last_request and elapsed < self.delay:
            time.sleep(self.delay - elapsed)

    def get_json(self, path: str, *, params: dict[str, str] | None = None, name: str) -> Any:
        self._sleep_if_needed()
        query = ""
        if params:
            query = "?" + urllib.parse.urlencode(params)
        url = self.base + path + query
        started = time.monotonic()
        status = "ok"
        error = ""
        try:
            req = urllib.request.Request(url, headers={**self.headers, "Accept": "application/json"})
            with urllib.request.urlopen(req, timeout=self.timeout) as resp:
                payload = json.load(resp)
        except urllib.error.HTTPError as e:
            body = e.read().decode("utf-8", "replace")[:800]
            status = "error"
            error = f"HTTP {e.code} {e.reason}: {body}"
            payload = {"status": "error", "error": error}
        except (urllib.error.URLError, TimeoutError) as e:
            status = "error"
            error = str(e)
            payload = {"status": "error", "error": error}
        elapsed = time.monotonic() - started
        self._last_request = time.monotonic()
        self.requests.append({
            "name": name,
            "path": path,
            "params": params or {},
            "elapsedSeconds": round(elapsed, 3),
            "status": status,
            "error": error,
        })
        return payload

    def query(self, expr: str, *, name: str) -> Any:
        return self.get_json("/api/v1/query", params={"query": expr}, name=name)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Collect a bounded Prometheus workload-shape profile for benchmark seed design.",
        formatter_class=argparse.ArgumentDefaultsHelpFormatter,
    )
    parser.add_argument("--url", default=os.environ.get("PROM_URL", ""), help="Prometheus base URL. May also use PROM_URL.")
    parser.add_argument("--out", default="", help="Output directory. Defaults to harness/artifacts/prometheus-workload/<timestamp>.")
    parser.add_argument("--timeout", type=float, default=90.0, help="Per-request timeout in seconds.")
    parser.add_argument("--delay", type=float, default=5.0, help="Delay between sequential requests in seconds.")
    parser.add_argument("--retention", default="15d", help="Retention cap used for window validation and summary text.")
    parser.add_argument("--metadata-limit", type=int, default=10000, help="Limit passed to /api/v1/metadata. Set 0 to skip metadata.")
    parser.add_argument("--include-promql", action="store_true", help="Run bounded PromQL top-k and density probes. Off by default.")
    parser.add_argument("--include-active-count", action="store_true", help="With --include-promql, run count({__name__!=\"\"}). This can be expensive.")
    parser.add_argument("--max-top-metrics", type=int, default=20, help="Top metric names to request and save when --include-promql is enabled.")
    parser.add_argument("--top-label", action="append", default=[], help="Label to profile with topk(count by(label)). Repeatable. Defaults to job, namespace, instance.")
    parser.add_argument("--max-top-label-values", type=int, default=20, help="Top label values per label when --include-promql is enabled.")
    parser.add_argument("--density-metrics", type=int, default=5, help="Number of top metrics to use for count_over_time density probes.")
    parser.add_argument("--density-window", action="append", default=[], help="Density windows. Repeatable. Defaults to 1h and 24h. Capped by --retention.")
    parser.add_argument("--cookie", default=os.environ.get("PROM_COOKIE", ""), help="Cookie header. May also use PROM_COOKIE.")
    parser.add_argument("--bearer-token", default=os.environ.get("PROM_BEARER_TOKEN", ""), help="Bearer token. May also use PROM_BEARER_TOKEN.")
    parser.add_argument("--basic-auth", default=os.environ.get("PROM_BASIC_AUTH", ""), help="Pre-encoded Basic auth value or user:pass. May also use PROM_BASIC_AUTH.")
    parser.add_argument("--insecure-note", action="store_true", help="Only records that TLS verification may be handled externally; urllib still uses default verification.")
    return parser.parse_args()


def build_headers(args: argparse.Namespace) -> dict[str, str]:
    headers: dict[str, str] = {"User-Agent": "promshim-workload-profiler/1"}
    if args.cookie:
        headers["Cookie"] = args.cookie
    if args.bearer_token:
        headers["Authorization"] = f"Bearer {args.bearer_token}"
    if args.basic_auth:
        import base64
        raw = args.basic_auth
        if ":" in raw and not raw.lower().startswith("basic "):
            raw = base64.b64encode(raw.encode("utf-8")).decode("ascii")
        headers["Authorization"] = raw if raw.lower().startswith("basic ") else f"Basic {raw}"
    return headers


def ensure_out(path: str) -> Path:
    if path:
        out = Path(path)
    else:
        stamp = dt.datetime.now(dt.timezone.utc).strftime("%Y%m%dT%H%M%SZ")
        out = Path("harness/artifacts/prometheus-workload") / stamp
    out.mkdir(parents=True, exist_ok=True)
    return out


def write_json(path: Path, data: Any) -> None:
    path.write_text(json.dumps(data, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def prom_result_vector(payload: Any) -> list[dict[str, Any]]:
    if not isinstance(payload, dict) or payload.get("status") != "success":
        return []
    data = payload.get("data") or {}
    result = data.get("result")
    return result if isinstance(result, list) else []


def metric_name_from_result(item: dict[str, Any]) -> str:
    metric = item.get("metric") or {}
    return str(metric.get("__name__") or metric.get("metric") or "")


def value_float(item: dict[str, Any]) -> float | None:
    value = item.get("value")
    if isinstance(value, list) and len(value) >= 2:
        try:
            return float(value[1])
        except (TypeError, ValueError):
            return None
    return None


def compact_top_values(payload: Any, label: str) -> list[dict[str, Any]]:
    out: list[dict[str, Any]] = []
    for item in prom_result_vector(payload):
        metric = item.get("metric") or {}
        out.append({"label": label, "value": str(metric.get(label, "")), "series": value_float(item)})
    return out


def classify_metric(name: str, metadata: dict[str, Any]) -> str:
    entries = metadata.get(name) or []
    for entry in entries if isinstance(entries, list) else []:
        typ = entry.get("type")
        if typ:
            return str(typ)
    if HISTOGRAM_RE.search(name):
        return "histogram_bucket"
    if COUNTER_RE.search(name):
        return "counter_or_histogram_part"
    return "unknown_or_gauge"


def duration_seconds(raw: str) -> int:
    m = re.fullmatch(r"(\d+)([smhdwy])", raw.strip())
    if not m:
        raise SystemExit(f"unsupported duration {raw!r}; use Prometheus-style integer s/m/h/d/w/y")
    n = int(m.group(1))
    unit = m.group(2)
    scale = {"s": 1, "m": 60, "h": 3600, "d": 86400, "w": 7 * 86400, "y": 365 * 86400}[unit]
    return n * scale


def cap_windows(windows: list[str], retention: str) -> list[str]:
    cap = duration_seconds(retention)
    kept = []
    for w in windows:
        if duration_seconds(w) <= cap:
            kept.append(w)
    return kept


def summarize(args: argparse.Namespace, out: Path, collected: dict[str, Any], metadata: dict[str, Any]) -> str:
    top_metrics = collected.get("top_metrics", [])
    metric_rows = []
    for item in top_metrics:
        name = metric_name_from_result(item)
        if not name:
            continue
        metric_rows.append({"name": name, "series": value_float(item), "type": classify_metric(name, metadata)})

    type_counts: dict[str, float] = {}
    for row in metric_rows:
        typ = row["type"]
        type_counts[typ] = type_counts.get(typ, 0.0) + float(row.get("series") or 0)

    lines = [
        "# Prometheus workload profile",
        "",
        "This is a bounded workload-shape capture for benchmark seed design. It is not a performance benchmark.",
        "",
        "## Safety settings",
        "",
        f"- Per-request timeout: `{args.timeout}s`",
        f"- Delay between requests: `{args.delay}s`",
        f"- Retention cap: `{args.retention}`",
        f"- PromQL probes enabled: `{str(args.include_promql).lower()}`",
        f"- Active count query enabled: `{str(args.include_active_count and args.include_promql).lower()}`",
        "",
        "## Files",
        "",
        "- `requests.json`: request timing/status log",
        "- `status-tsdb.json`: `/api/v1/status/tsdb` response when available",
        "- `metadata.json`: `/api/v1/metadata` response when requested",
        "- `promql/*.json`: optional bounded PromQL probe responses",
        "",
    ]

    tsdb = collected.get("status_tsdb_data") or {}
    head = tsdb.get("headStats") or {}
    if head:
        lines += [
            "## TSDB head stats",
            "",
            f"- Num series: `{head.get('numSeries', 'n/a')}`",
            f"- Chunk count: `{head.get('chunkCount', 'n/a')}`",
            f"- Min time: `{head.get('minTime', 'n/a')}`",
            f"- Max time: `{head.get('maxTime', 'n/a')}`",
            "",
        ]

    if metric_rows:
        lines += ["## Top metric names", "", "| Metric | Series | Type hint |", "|---|---:|---|"]
        for row in metric_rows:
            series = row["series"]
            series_s = "" if series is None else str(int(series) if math.isfinite(series) else series)
            lines.append(f"| `{row['name']}` | {series_s} | `{row['type']}` |")
        lines.append("")

    if type_counts:
        lines += ["## Top-metric type mix", "", "| Type hint | Series in top set |", "|---|---:|"]
        for typ, count in sorted(type_counts.items(), key=lambda kv: kv[1], reverse=True):
            lines.append(f"| `{typ}` | {int(count)} |")
        lines.append("")

    label_values = collected.get("top_label_values", {})
    if label_values:
        lines += ["## Top label values", ""]
        for label, rows in label_values.items():
            lines += [f"### `{label}`", "", "| Value | Series |", "|---|---:|"]
            for row in rows[: args.max_top_label_values]:
                series = row.get("series")
                series_s = "" if series is None else str(int(series) if math.isfinite(series) else series)
                lines.append(f"| `{row.get('value', '')}` | {series_s} |")
            lines.append("")

    density = collected.get("density", [])
    if density:
        lines += ["## Density probes", "", "These use quantiles over `count_over_time(metric[window])` for a few top metrics.", "", "| Metric | Window | p50 samples/series | p90 samples/series |", "|---|---|---:|---:|"]
        by_name: dict[str, dict[str, float | None]] = {}
        for row in density:
            p50 = row.get("p50")
            p90 = row.get("p90")
            lines.append(f"| `{row.get('metric')}` | `{row.get('window')}` | {'' if p50 is None else round(p50, 2)} | {'' if p90 is None else round(p90, 2)} |")
        lines.append("")

    lines += [
        "## Benchmark-seed interpretation",
        "",
        "Use this capture to choose a synthetic mix with separate knobs for active series at eval time, series seen over the window, scrape interval / samples per series, histogram bucket count, and churn. Avoid treating active-series cardinality alone as the benchmark density.",
        "",
    ]
    return "\n".join(lines)


def main() -> int:
    args = parse_args()
    if not args.url:
        print("error: pass --url or set PROM_URL", file=sys.stderr)
        return 2
    if args.max_top_metrics <= 0:
        print("error: --max-top-metrics must be positive", file=sys.stderr)
        return 2
    out = ensure_out(args.out)
    promql_dir = out / "promql"
    promql_dir.mkdir(exist_ok=True)
    client = PromClient(args.url, build_headers(args), args.timeout, args.delay, out)

    collected: dict[str, Any] = {}

    status = client.get_json("/api/v1/status/tsdb", name="status-tsdb")
    write_json(out / "status-tsdb.json", status)
    if isinstance(status, dict):
        collected["status_tsdb_data"] = status.get("data") or {}

    metadata: dict[str, Any] = {}
    if args.metadata_limit != 0:
        params = {"limit": str(args.metadata_limit)} if args.metadata_limit > 0 else None
        meta_payload = client.get_json("/api/v1/metadata", params=params, name="metadata")
        write_json(out / "metadata.json", meta_payload)
        if isinstance(meta_payload, dict) and meta_payload.get("status") == "success":
            metadata = meta_payload.get("data") or {}
    else:
        write_json(out / "metadata.json", {"status": "skipped"})

    if args.include_promql:
        if args.include_active_count:
            payload = client.query('count({__name__!=""})', name="active-series-count")
            write_json(promql_dir / "active-series-count.json", payload)
            collected["active_series_count"] = prom_result_vector(payload)

        top_metrics_expr = f'topk({args.max_top_metrics}, count by (__name__) ({{__name__!=""}}))'
        payload = client.query(top_metrics_expr, name="top-metrics")
        write_json(promql_dir / "top-metrics.json", payload)
        top_metrics = prom_result_vector(payload)
        collected["top_metrics"] = top_metrics

        labels = args.top_label or DEFAULT_LABELS
        top_label_values: dict[str, list[dict[str, Any]]] = {}
        for label in labels:
            if not re.fullmatch(r"[a-zA-Z_][a-zA-Z0-9_]*", label):
                print(f"warning: skipping invalid label name {label!r}", file=sys.stderr)
                continue
            expr = f'topk({args.max_top_label_values}, count by ({label}) ({{__name__!=""}}))'
            payload = client.query(expr, name=f"top-label-{label}")
            write_json(promql_dir / f"top-label-{label}.json", payload)
            top_label_values[label] = compact_top_values(payload, label)
        collected["top_label_values"] = top_label_values

        windows = cap_windows(args.density_window or DEFAULT_WINDOWS, args.retention)
        density_rows: list[dict[str, Any]] = []
        density_metrics = [metric_name_from_result(item) for item in top_metrics]
        density_metrics = [m for m in density_metrics if m][: args.density_metrics]
        for metric in density_metrics:
            if not re.fullmatch(r"[a-zA-Z_:][a-zA-Z0-9_:]*", metric):
                continue
            for window in windows:
                p50_expr = f'quantile(0.5, count_over_time({metric}[{window}]))'
                p90_expr = f'quantile(0.9, count_over_time({metric}[{window}]))'
                p50_payload = client.query(p50_expr, name=f"density-{metric}-{window}-p50")
                p90_payload = client.query(p90_expr, name=f"density-{metric}-{window}-p90")
                safe_metric = re.sub(r"[^a-zA-Z0-9_.:-]+", "_", metric)
                write_json(promql_dir / f"density-{safe_metric}-{window}-p50.json", p50_payload)
                write_json(promql_dir / f"density-{safe_metric}-{window}-p90.json", p90_payload)
                p50_values = prom_result_vector(p50_payload)
                p90_values = prom_result_vector(p90_payload)
                density_rows.append({
                    "metric": metric,
                    "window": window,
                    "p50": value_float(p50_values[0]) if p50_values else None,
                    "p90": value_float(p90_values[0]) if p90_values else None,
                })
        collected["density"] = density_rows
    else:
        write_json(promql_dir / "README.json", {"status": "skipped", "reason": "run with --include-promql to enable bounded PromQL probes"})

    write_json(out / "requests.json", client.requests)
    write_json(out / "workload-profile.json", collected)
    (out / "summary.md").write_text(summarize(args, out, collected, metadata), encoding="utf-8")
    print(f"wrote {out}")
    print(f"requests: {len(client.requests)}; see {out / 'requests.json'}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
