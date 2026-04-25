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

PROFILE_EVENT_FIELDS = [
    "readRows",
    "readBytes",
    "resultRows",
    "selectedRows",
    "selectedBytes",
    "readCompressedBytes",
    "functionExecute",
    "memoryTrackerUsage",
]


def sanitize_log_comment_part(value):
    value = str(value or "").strip()
    if not value:
        return "unknown"
    out = []
    for ch in value:
        if ch.isalnum() or ch in "_-.":
            out.append(ch)
        else:
            out.append("_")
    return "".join(out)


def expected_log_comment(query_name, result_key, result):
    mode = result.get("nativeLoweringMode") or result_key.split("@", 1)[0] or "prom"
    policy = result.get("routingPolicy") or ""
    comment = "promshim-bench query=" + sanitize_log_comment_part(query_name) + " mode=" + sanitize_log_comment_part(mode)
    if policy.strip():
        comment += " policy=" + sanitize_log_comment_part(policy)
    return comment


def proof_from_matches(matches):
    if not matches:
        return {"attributionStatus": "missing"}
    if len(matches) != 1:
        return {
            "attributionStatus": "ambiguous",
            "matchCount": len(matches),
            "memoryReports": sorted({m["memoryReport"] for m in matches}),
        }
    match = matches[0]
    row = match["row"]
    proof = {
        "attributionStatus": "matched",
        "memoryReport": match["memoryReport"],
        "queryCount": row.get("queryCount", 0),
        "queryDurationP50Ms": row.get("queryDurationP50Ms", 0),
        "queryDurationP90Ms": row.get("queryDurationP90Ms", 0),
        "queryDurationMaxMs": row.get("queryDurationMaxMs", 0),
        "memoryP50Bytes": row.get("memoryP50Bytes", 0),
        "memoryP95Bytes": row.get("memoryP95Bytes", 0),
        "memoryMaxBytes": row.get("memoryMaxBytes", 0),
    }
    events = {field: row.get(field, 0) for field in PROFILE_EVENT_FIELDS if row.get(field, 0) != 0}
    if events:
        proof["profileEvents"] = events
    return proof

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
        "proofMatched": 0,
        "proofMissing": 0,
        "proofAmbiguous": 0,
    },
    "settingsProfiles": {},
    "strategies": {},
    "routingDecisions": {},
    "servedCandidates": {},
    "modes": {},
    "clickHouseProofSignatures": [],
}
settings = Counter()
strategies = Counter()
routing = Counter()
served = Counter()
modes = Counter()
proof_statuses = Counter()

memory_by_log_comment = defaultdict(list)
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
    for row in query_log:
        comment = row.get("logComment") or ""
        if comment:
            memory_by_log_comment[comment].append({"memoryReport": str(report), "row": row})

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
            log_comment = expected_log_comment(row.get("name"), mode, result)
            proof = proof_from_matches(memory_by_log_comment.get(log_comment, []))
            proof_statuses[proof["attributionStatus"]] += 1
            summary["clickHouseProofSignatures"].append({
                "benchReport": str(report),
                "query": row.get("name"),
                "endpoint": row.get("endpoint"),
                "category": row.get("category") or "",
                "mode": mode,
                "nativeLoweringMode": result.get("nativeLoweringMode") or mode.split("@", 1)[0],
                "routingPolicy": result.get("routingPolicy") or "",
                "strategy": result.get("strategy") or "",
                "settingsProfile": result.get("settingsProfile") or "",
                "servedCandidate": result.get("servedCandidate") or "",
                "logComment": log_comment,
                "proof": proof,
            })
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

summary["totals"]["proofMatched"] = proof_statuses.get("matched", 0)
summary["totals"]["proofMissing"] = proof_statuses.get("missing", 0)
summary["totals"]["proofAmbiguous"] = proof_statuses.get("ambiguous", 0)
summary["settingsProfiles"] = dict(sorted(settings.items()))
summary["strategies"] = dict(sorted(strategies.items()))
summary["routingDecisions"] = dict(sorted(routing.items()))
summary["servedCandidates"] = dict(sorted(served.items()))
summary["modes"] = dict(sorted(modes.items()))
print(json.dumps(summary, indent=2, sort_keys=True))
PY
