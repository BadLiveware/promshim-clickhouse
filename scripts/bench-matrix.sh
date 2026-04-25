#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: ./scripts/bench-matrix.sh [options] [profile:path]...

Render a cross-profile Markdown matrix that traces each query `category`
across multiple bench profiles. Rows are one per category; columns are
grouped per profile (Prom p50, Native p50, N/P, F/N). Within a (category,
profile) bucket, we report the median across that profile's queries — so
`instant_rate_long` at 1y collapses three rate windows into one number.

If no positional args are given, defaults to:
  7d:harness/artifacts/bench-report-7d.json
  30d:harness/artifacts/bench-report-30d.json
  1y:harness/artifacts/bench-report-1y.json

Options:
  --sweep PATH    Read harness/artifacts/sweeps/<run-name>/manifest.json and
                 render a v2 sweep matrix across report/mode dimensions.
  --sort-by {np|fn|category|cat}
                 Sort direction across rows. np = max native/prom ratio
                 (slowest-vs-prom first, default). fn = max fallback/native
                 ratio (biggest local-evaluator losses first). category =
                 alphabetical by category.
  --per-query    Emit one row per query instead of collapsing by category.
                 Useful when comparing related queries inside a category
                 (e.g. rate_1d vs rate_7d vs rate_30d for instant_rate_long
                 at 1y). Rows sparse across profiles because query names
                 differ per window.
  -h, --help     Show this help text.

Example:
  ./scripts/bench-matrix.sh \
    7d:harness/artifacts/bench-report-7d.json \
    30d:harness/artifacts/bench-report-30d.json \
    1y:harness/artifacts/bench-report-1y.json
EOF
}

SORT_BY="np"
PER_QUERY=0
SWEEP_PATH=""
POSARGS=()
while [[ $# -gt 0 ]]; do
  case "$1" in
    --sweep)    SWEEP_PATH="$2"; shift 2 ;;
    --sort-by)  SORT_BY="$2"; shift 2 ;;
    --per-query) PER_QUERY=1; shift ;;
    -h|--help)  usage; exit 0 ;;
    *)          POSARGS+=("$1"); shift ;;
  esac
done

REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$REPO_ROOT"

if [[ -n "$SWEEP_PATH" ]]; then
  if [[ ! -f "$SWEEP_PATH" ]]; then
    echo "Error: sweep manifest not found: $SWEEP_PATH" >&2
    exit 1
  fi
  python3 - "$SWEEP_PATH" "$PER_QUERY" <<'PY'
import json, pathlib, statistics, sys
manifest_path = pathlib.Path(sys.argv[1])
per_query = sys.argv[2] == "1"
root = pathlib.Path.cwd()
manifest = json.loads(manifest_path.read_text())
rows = []
for report_meta in manifest.get("bench", {}).get("reports", []):
    report_path = root / report_meta["path"]
    if not report_path.exists():
        continue
    report = json.loads(report_path.read_text())
    labels = report.get("runLabels") or {}
    profile = report_meta.get("profile") or labels.get("profile") or "unknown"
    density = report_meta.get("density") or labels.get("density") or "unknown"
    transport = report_meta.get("transport") or labels.get("transport") or "unknown"
    corpus = pathlib.Path(report.get("corpusPath") or report_path.name).stem
    for row in report.get("rows") or []:
        prom = (row.get("prom") or {}).get("p50Ms")
        for mode, result in (row.get("shim") or {}).items():
            shim = result.get("p50Ms")
            strict_candidate = result.get("strictCandidate") or ""
            selected_candidate = result.get("selectedCandidate") or ""
            served_candidate = result.get("servedCandidate") or ""
            rows.append({
                "category": row.get("category") or "uncategorized",
                "query": row.get("name"),
                "profile": profile,
                "density": density,
                "transport": transport,
                "corpus": corpus,
                "mode": mode,
                "routingPolicy": result.get("routingPolicy") or "",
                "strategy": result.get("strategy") or "",
                "strictCandidate": strict_candidate,
                "selectedCandidate": selected_candidate,
                "servedCandidate": served_candidate,
                "candidateFlip": bool(strict_candidate and selected_candidate and strict_candidate != selected_candidate),
                "promP50Ms": prom,
                "shimP50Ms": shim,
                "ratio": (shim / prom) if prom and shim else None,
                "promBand": row.get("promBand") or "n/a",
            })

def r2(value):
    return "—" if value is None else f"{value:.2f}"
def rx(value):
    return "—" if value is None else f"{value:.1f}×"
print()
print(f"## Sweep benchmark matrix: {manifest.get('runName', manifest_path.parent.name)}")
print()
print(f"Manifest: `{manifest_path}`")
print()
if per_query:
    print("| Category | Query | Profile | Density | Transport | Corpus | Mode | Routing policy | Strategy | Strict candidate | Selected candidate | Served candidate | Candidate flip | Prom band | Prom p50 | Shim p50 | S/P |")
    print("|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---:|---:|---:|")
    for row in sorted(rows, key=lambda r: (r["category"], r["query"] or "", r["profile"], r["density"], r["mode"], r["routingPolicy"])):
        print(f"| {row['category']} | {row['query']} | {row['profile']} | {row['density']} | {row['transport']} | {row['corpus']} | {row['mode']} | {row['routingPolicy'] or 'n/a'} | {row['strategy']} | {row['strictCandidate'] or 'n/a'} | {row['selectedCandidate'] or 'n/a'} | {row['servedCandidate'] or 'n/a'} | {'yes' if row['candidateFlip'] else 'no'} | {row['promBand']} | {r2(row['promP50Ms'])} | {r2(row['shimP50Ms'])} | {rx(row['ratio'])} |")
else:
    buckets = {}
    for row in rows:
        key = (row["category"], row["profile"], row["density"], row["transport"], row["mode"], row["routingPolicy"])
        buckets.setdefault(key, []).append(row)
    print("| Category | Profile | Density | Transport | Mode | Routing policy | Count | Strategies | Candidate flips | Prom p50 med | Shim p50 med | S/P med | Target bands |")
    print("|---|---|---|---|---|---|---:|---|---:|---:|---:|---:|---|")
    for key, vals in sorted(buckets.items()):
        category, profile, density, transport, mode, routing_policy = key
        prom_vals = [v["promP50Ms"] for v in vals if v.get("promP50Ms") is not None]
        shim_vals = [v["shimP50Ms"] for v in vals if v.get("shimP50Ms") is not None]
        ratio_vals = [v["ratio"] for v in vals if v.get("ratio") is not None]
        strategies = ", ".join(f"{s}:{sum(1 for v in vals if v['strategy']==s)}" for s in sorted({v['strategy'] for v in vals}))
        bands = ", ".join(f"{b}:{sum(1 for v in vals if v['promBand']==b)}" for b in sorted({v['promBand'] for v in vals}))
        flips = sum(1 for v in vals if v['candidateFlip'])
        print(f"| {category} | {profile} | {density} | {transport} | {mode} | {routing_policy or 'n/a'} | {len(vals)} | {strategies} | {flips} | {r2(statistics.median(prom_vals) if prom_vals else None)} | {r2(statistics.median(shim_vals) if shim_vals else None)} | {rx(statistics.median(ratio_vals) if ratio_vals else None)} | {bands} |")
print()
PY
  exit 0
fi

if (( ${#POSARGS[@]} == 0 )); then
  POSARGS=(
    "7d:harness/artifacts/bench-report-7d.json"
    "30d:harness/artifacts/bench-report-30d.json"
    "1y:harness/artifacts/bench-report-1y.json"
  )
fi

if ! command -v jq >/dev/null 2>&1; then
  echo "Error: jq is required." >&2
  exit 1
fi

PROFILES=()
PATHS=()
for arg in "${POSARGS[@]}"; do
  if [[ "$arg" != *:* ]]; then
    echo "Error: expected profile:path, got: $arg" >&2
    exit 1
  fi
  PROFILES+=("${arg%%:*}")
  PATHS+=("${arg#*:}")
done

for p in "${PATHS[@]}"; do
  if [[ ! -f "$p" ]]; then
    echo "Error: report not found: $p" >&2
    exit 1
  fi
done

case "$SORT_BY" in
  np|fn|cat|category) : ;;
  *) echo "Error: unknown --sort-by: $SORT_BY" >&2; exit 1 ;;
esac

# Assemble {profile: report} once.
jq_input='{'
for i in "${!PROFILES[@]}"; do
  [[ $i -gt 0 ]] && jq_input+=','
  jq_input+="\"${PROFILES[$i]}\": $(cat "${PATHS[$i]}")"
done
jq_input+='}'

PROFILES_JSON=$(printf '%s\n' "${PROFILES[@]}" | jq -R . | jq -s .)

header_line="| Category |"
align_line="|---|"
if (( PER_QUERY == 1 )); then
  header_line="| Category | Query |"
  align_line="|---|---|"
fi
for p in "${PROFILES[@]}"; do
  header_line+=" Prom p50 (${p}) | Native p50 (${p}) | N/P (${p}) | F/N (${p}) |"
  align_line+="---:|---:|---:|---:|"
done

echo
if (( PER_QUERY == 1 )); then
  echo "## Cross-profile native-SQL vs Prometheus matrix (per-query)"
else
  echo "## Cross-profile native-SQL vs Prometheus matrix (by category)"
fi
echo
echo "Profiles: ${PROFILES[*]}. N/P = native_p50 / prom_p50 (< 1 means"
echo "native beat Prom). F/N = fallback_p50 / native_p50 (< 1 means the"
echo "local-evaluator fallback is faster than lowering — a \"don't lower"
echo "this\" signal). Millisecond values are p50 medians across repeats;"
if (( PER_QUERY == 0 )); then
  echo "when a (category, profile) bucket holds multiple queries, the cell"
  echo "shows the median of that bucket (so cells are comparable even when"
  echo "profiles expose different queries in the same category)."
fi
echo
echo "$header_line"
echo "$align_line"

echo "$jq_input" | jq -r \
  --argjson profiles "$PROFILES_JSON" \
  --arg sortBy "$SORT_BY" \
  --argjson perQuery "$PER_QUERY" '
  def r2: if . == null or . == 0 then "—" else (. * 100 | round / 100 | tostring) end;
  def r1: if . == null or . == 0 then "—" else ((. * 10 | round / 10 | tostring) + "×") end;
  def med: if length == 0 then null
           else sort as $s | ($s | length) as $n
                | if ($n % 2) == 1 then $s[($n/2|floor)]
                  else (($s[$n/2 - 1] + $s[$n/2]) / 2) end end;

  # Flatten all rows, annotating each with its profile label.
  [ to_entries[] as $e | $e.value.rows[]? | . + {profile: $e.key} ] as $rows

  | (if $perQuery == 1 then
       # Group by (category, name). Each cell is a single row.
       $rows
       | group_by(((.category // "uncategorized") + "\u0000" + .name))
       | map({
           category: (.[0].category // "uncategorized"),
           name: .[0].name,
           byProfile: (map({key: .profile, value: {
             promP50Ms: .promP50Ms, nativeP50Ms: .nativeP50Ms,
             nativePromRatio: .nativePromRatio, fallbackNativeRatio: .fallbackNativeRatio
           }}) | from_entries),
           sortNP: (map(.nativePromRatio // 0) | max),
           sortFN: (map(.fallbackNativeRatio // 0) | max)
         })
     else
       # Group by category only, then bucket each category into profiles
       # and take the median of each metric within a (category, profile).
       $rows
       | group_by(.category // "uncategorized")
       | map(
           . as $g
           | {
               category: ($g[0].category // "uncategorized"),
               byProfile: (
                 $g
                 | group_by(.profile)
                 | map({
                     key: .[0].profile,
                     value: {
                       promP50Ms: ([.[].promP50Ms // empty] | med),
                       nativeP50Ms: ([.[].nativeP50Ms // empty] | med),
                       nativePromRatio: ([.[].nativePromRatio // empty] | med),
                       fallbackNativeRatio: ([.[].fallbackNativeRatio // empty] | med)
                     }
                   })
                 | from_entries
               ),
               sortNP: ([$g[].nativePromRatio // 0] | max),
               sortFN: ([$g[].fallbackNativeRatio // 0] | max)
             }
         )
     end)

  | (if $sortBy == "cat" or $sortBy == "category" then
       sort_by(.category + "|" + (.name // ""))
     elif $sortBy == "fn" then
       sort_by(.sortFN) | reverse
     else
       sort_by(.sortNP) | reverse
     end)

  | .[]
  | . as $g
  | (if $perQuery == 1 then "| \($g.category) | \($g.name) |"
     else "| \($g.category) |" end) +
    ([ $profiles[] as $p
       | ($g.byProfile[$p] // {}) as $r
       | " \(($r.promP50Ms // null) | r2) | \(($r.nativeP50Ms // null) | r2) | \(($r.nativePromRatio // null) | r1) | \(($r.fallbackNativeRatio // null) | r1) |"
     ] | join(""))
'

echo
