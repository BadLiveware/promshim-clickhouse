#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
Usage: scripts/bench-artifact-summary.sh <sweep-dir|bench-report.json> [more paths...]

Summarize v2 promshim benchmark artifacts as JSON. For a sweep directory, all
bench-report*.json and memory-summary*.json files directly under the directory
are included.
USAGE
}

if [[ $# -lt 1 || "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

python3 - "$@" <<'PY'
import glob
import json
import os
import sys
from collections import Counter, defaultdict
from pathlib import Path

inputs = [Path(p) for p in sys.argv[1:]]
bench_reports = []
memory_reports = []
for path in inputs:
    if path.is_dir():
        bench_reports.extend(Path(p) for p in sorted(glob.glob(str(path / "bench-report*.json"))))
        memory_reports.extend(Path(p) for p in sorted(glob.glob(str(path / "memory-summary*.json"))))
    elif path.name.startswith("memory-summary"):
        memory_reports.append(path)
    else:
        bench_reports.append(path)

summary = {
    "benchReports": [],
    "memorySummaries": [],
    "totals": {
        "queryRows": 0,
        "resultRows": 0,
        "errors": 0,
        "missingLogComments": 0,
    },
    "settingsProfiles": {},
    "strategies": {},
    "routingDecisions": {},
    "servedCandidates": {},
    "modes": {},
}
settings = Counter()
strategies = Counter()
routing = Counter()
served = Counter()
modes = Counter()

for report in bench_reports:
    with report.open() as fh:
        data = json.load(fh)
    rows = data.get("rows") or []
    report_errors = 0
    result_rows = 0
    for row in rows:
        shim = row.get("shim") or {}
        for mode, result in shim.items():
            result_rows += 1
            modes[mode] += 1
            if result.get("error"):
                report_errors += 1
            settings[result.get("settingsProfile") or ""] += 1
            strategies[result.get("strategy") or ""] += 1
            routing[result.get("routingDecision") or ""] += 1
            served[result.get("servedCandidate") or ""] += 1
    summary["benchReports"].append({
        "path": str(report),
        "queryRows": len(rows),
        "resultRows": result_rows,
        "errors": report_errors,
        "schemaVersion": data.get("schemaVersion"),
        "memoryMode": data.get("memoryMode"),
    })
    summary["totals"]["queryRows"] += len(rows)
    summary["totals"]["resultRows"] += result_rows
    summary["totals"]["errors"] += report_errors

for report in memory_reports:
    with report.open() as fh:
        data = json.load(fh)
    missing = data.get("missingLogComments") or []
    errors = data.get("errors") or []
    query_log = data.get("clickHouseQueryLog") or []
    summary["memorySummaries"].append({
        "path": str(report),
        "queryLogRows": len(query_log),
        "missingLogComments": len(missing),
        "errors": errors,
    })
    summary["totals"]["missingLogComments"] += len(missing)

summary["settingsProfiles"] = dict(sorted(settings.items()))
summary["strategies"] = dict(sorted(strategies.items()))
summary["routingDecisions"] = dict(sorted(routing.items()))
summary["servedCandidates"] = dict(sorted(served.items()))
summary["modes"] = dict(sorted(modes.items()))
print(json.dumps(summary, indent=2, sort_keys=True))
PY
