#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: ./scripts/run-sweep.sh [options]

One-command compliance/benchmark sweep helper. By default, runs compliance plus
a 7d sparse benchmark against the isolated benchmark stack and writes named
artifacts under harness/artifacts/sweeps/<run-name>/.

Common examples:
  ./scripts/run-sweep.sh --dry-run --estimate
  ./scripts/run-sweep.sh --setup --profile all --density sparse --target both
  ./scripts/run-sweep.sh --name pr-42-default
  ./scripts/run-sweep.sh --profile 7d --density dense --corpus-set processing --estimate
  ./scripts/run-sweep.sh --profile 7d --corpus-set optimization --skip-compliance
  ./scripts/run-sweep.sh --bench-status
  ./scripts/run-sweep.sh --bench-reset --yes

Data setup and seed policies:
  Normal runs default to --seed reuse and fail if selected benchmark data is
  missing. --setup defaults to --seed missing. Use --seed always only when you
  deliberately want to write selected data again.

Options:
  --bench-status                 Show isolated benchmark stack and seed-marker status.
  --bench-reset --yes            Stop the benchmark stack and delete benchmark volumes only.
  --setup                        Seed missing selected benchmark datasets, then exit.
  --run                          Run the selected sweep (default when no helper mode is selected).
  --name NAME, --run-id NAME     Artifact run name (default: timestamped sweep name).
  --dry-run                      Print planned work without side effects.
  --estimate                     Include rough data/disk/runtime estimates; implies --dry-run unless --execute is set.
  --execute                      Execute even when --estimate is set.
  --skip-compliance              Skip compliance pass.
  --skip-bench                   Skip benchmark pass.
  --shim-modes LIST              Bench modes, e.g. prefer,force_supported,off.
  --routing-policies LIST        Routing policies, e.g. strict,cost_shadow.
  --warmup-routing-policies LIST Run a benchmark warmup pass with these routing policies before measured bench reports.
  --cost-routing-local-families LIST
                                  Enable cost-prefer local families on the benchmark shim.
  --include-prom BOOL            Include Prometheus timing in v2 bench reports (default true).
  --memory {off|summary|detailed}
                                  Capture memory trade-off artifacts (default summary).
  --clickhouse-reference-profile NAME
                                  Reference profile label recorded in sweep artifacts (default default-benchmark-compose).
  --settings-profile NAME        promshim ClickHouse settings profile for benchmark containers (default default_safe).
  --corpus-set {native|processing|optimization|both}
                                  Benchmark corpus family (default: native; dense defaults to processing).
  --profile {7d|30d|1y|all}      Profile to inspect/seed (default for setup: all).
  --density {sparse|dense|all}   Dataset density (default for setup: sparse).
  --target {both|ch|prom}        Seed/check target (default: both).
  --seed {reuse|missing|always|never}
                                  Seed policy (normal default: reuse; --setup default: missing).
  --transport {native|http}      Benchmark promshim transport when stack is started.
  --yes                          Confirm destructive actions such as --bench-reset.
  -h, --help                     Show this help.

Benchmark stack endpoints:
  Prometheus: http://localhost:29190
  promshim:   http://localhost:29191
  ClickHouse: http://localhost:28124
  CH write:   http://localhost:29192/write
EOF
}

log() {
  printf '[%s] %s\n' "$(date +'%Y-%m-%d %H:%M:%S')" "$*"
}

fatal() {
  echo "Error: $*" >&2
  exit 1
}

REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
# shellcheck source=lib/run-lock.sh
source "${REPO_ROOT}/scripts/lib/run-lock.sh"

BENCH_DIR="${REPO_ROOT}/harness/bench"
BENCH_PROM_URL="http://localhost:29190"
BENCH_SHIM_URL="http://localhost:29191"
BENCH_CH_URL="http://localhost:28124"
BENCH_CH_WRITE_ENDPOINT="http://localhost:29192/write"
BENCH_PROM_WRITE_ENDPOINT="http://localhost:29190/api/v1/write"

MODE=""
PROFILE=""
DENSITY=""
TARGET="both"
SEED_POLICY=""
TRANSPORT="native"
RUN_NAME=""
DRY_RUN=0
ESTIMATE=0
EXECUTE=0
SKIP_COMPLIANCE=0
SKIP_BENCH=0
SHIM_MODES="prefer,force_supported,off"
ROUTING_POLICIES="strict"
WARMUP_ROUTING_POLICIES=""
COST_ROUTING_LOCAL_FAMILIES=""
INCLUDE_PROM="true"
MEMORY_MODE="summary"
CLICKHOUSE_REFERENCE_PROFILE="${PROM_SHIM_BENCH_CLICKHOUSE_REFERENCE_PROFILE:-default-benchmark-compose}"
SETTINGS_PROFILE="${PROM_SHIM_CLICKHOUSE_SETTINGS_PROFILE:-default_safe}"
CORPUS_SET=""
YES=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --bench-status) MODE="status"; shift ;;
    --bench-reset)  MODE="reset"; shift ;;
    --setup)        MODE="setup"; shift ;;
    --run)          MODE="run"; shift ;;
    --name|--run-id) RUN_NAME="$2"; shift 2 ;;
    --dry-run)      DRY_RUN=1; shift ;;
    --estimate)     ESTIMATE=1; shift ;;
    --execute)      EXECUTE=1; shift ;;
    --skip-compliance) SKIP_COMPLIANCE=1; shift ;;
    --skip-bench)   SKIP_BENCH=1; shift ;;
    --shim-modes)   SHIM_MODES="$2"; shift 2 ;;
    --routing-policies) ROUTING_POLICIES="$2"; shift 2 ;;
    --warmup-routing-policies) WARMUP_ROUTING_POLICIES="$2"; shift 2 ;;
    --cost-routing-local-families) COST_ROUTING_LOCAL_FAMILIES="$2"; shift 2 ;;
    --include-prom) INCLUDE_PROM="$2"; shift 2 ;;
    --memory)       MEMORY_MODE="$2"; shift 2 ;;
    --clickhouse-reference-profile) CLICKHOUSE_REFERENCE_PROFILE="$2"; shift 2 ;;
    --settings-profile) SETTINGS_PROFILE="$2"; shift 2 ;;
    --corpus-set)   CORPUS_SET="$2"; shift 2 ;;
    --profile)      PROFILE="$2"; shift 2 ;;
    --density)      DENSITY="$2"; shift 2 ;;
    --target)       TARGET="$2"; shift 2 ;;
    --seed)         SEED_POLICY="$2"; shift 2 ;;
    --transport)    TRANSPORT="$2"; shift 2 ;;
    --yes)          YES=1; shift ;;
    -h|--help)      usage; exit 0 ;;
    *)              fatal "unknown argument: $1" ;;
  esac
done

case "$MODE" in
  status|reset|setup|run) ;;
  "") MODE="run" ;;
  *) fatal "unknown mode: $MODE" ;;
esac
case "$TARGET" in
  both|ch|prom) ;;
  *) fatal "--target must be both|ch|prom (got: $TARGET)" ;;
esac
case "$TRANSPORT" in
  native|http) ;;
  *) fatal "--transport must be native|http (got: $TRANSPORT)" ;;
esac
case "$CLICKHOUSE_REFERENCE_PROFILE" in
  default-benchmark-compose|promshim-ch-timeseries-reference-v1) ;;
  *) fatal "--clickhouse-reference-profile must be default-benchmark-compose|promshim-ch-timeseries-reference-v1 (got: $CLICKHOUSE_REFERENCE_PROFILE)" ;;
esac
if [[ -n "$CORPUS_SET" ]]; then
  case "$CORPUS_SET" in
    native|processing|optimization|both) ;;
    *) fatal "--corpus-set must be native|processing|optimization|both (got: $CORPUS_SET)" ;;
  esac
fi
if (( ESTIMATE == 1 && EXECUTE == 0 )); then
  DRY_RUN=1
fi

if [[ -z "$PROFILE" ]]; then
  if [[ "$MODE" == "run" ]]; then PROFILE="7d"; else PROFILE="all"; fi
fi
if [[ -z "$DENSITY" ]]; then
  if [[ "$MODE" == "status" ]]; then DENSITY="all"; else DENSITY="sparse"; fi
fi
if [[ -z "$SEED_POLICY" ]]; then
  if [[ "$MODE" == "setup" ]]; then SEED_POLICY="missing"; else SEED_POLICY="reuse"; fi
fi
if [[ -z "$CORPUS_SET" ]]; then
  if [[ "$DENSITY" == "dense" ]]; then CORPUS_SET="processing"; else CORPUS_SET="native"; fi
fi
case "$SEED_POLICY" in
  reuse|missing|always|never) ;;
  *) fatal "--seed must be reuse|missing|always|never (got: $SEED_POLICY)" ;;
esac
if [[ -z "$RUN_NAME" ]]; then
  RUN_NAME="sweep-$(date -u +%Y%m%dT%H%M%SZ)"
fi

profiles_for() {
  case "$1" in
    all) printf '%s\n' 7d 30d 1y ;;
    7d|30d|1y) printf '%s\n' "$1" ;;
    *) fatal "--profile must be 7d|30d|1y|all (got: $1)" ;;
  esac
}

densities_for() {
  case "$1" in
    all) printf '%s\n' sparse dense ;;
    sparse|dense) printf '%s\n' "$1" ;;
    *) fatal "--density must be sparse|dense|all (got: $1)" ;;
  esac
}

target_includes() {
  case "$1:$2" in
    both:*|prom:prom|ch:ch) return 0 ;;
    *) return 1 ;;
  esac
}

profile_end_time() {
  local profile="$1" density="$2"
  python3 - "$profile" "$density" <<'PY'
from datetime import datetime, timezone, timedelta
import sys
profile, density = sys.argv[1:3]
profiles = {
    "7d":  ("2026-03-22T21:45:42Z", timedelta(days=7)),
    "30d": ("2026-02-22T21:45:42Z", timedelta(days=30)),
    "1y":  ("2025-03-22T21:45:42Z", timedelta(days=365)),
}
end, duration = profiles[profile]
dt = datetime.fromisoformat(end.replace("Z", "+00:00"))
if density == "dense":
    dt = dt - duration - timedelta(days=1)
print(dt.astimezone(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"))
PY
}

compose() {
  local compose_args=("-f" "docker-compose.yml")
  if [[ "$CLICKHOUSE_REFERENCE_PROFILE" == "promshim-ch-timeseries-reference-v1" ]]; then
    compose_args+=("-f" "docker-compose.reference.yml")
  fi
  (cd "$BENCH_DIR" && PROM_SHIM_CLICKHOUSE_TRANSPORT="$TRANSPORT" PROM_SHIM_CLICKHOUSE_SETTINGS_PROFILE="$SETTINGS_PROFILE" PROM_SHIM_COST_ROUTING_LOCAL_FAMILIES="$COST_ROUTING_LOCAL_FAMILIES" PROM_SHIM_DISABLE_OPTIMIZED_IR="${PROM_SHIM_DISABLE_OPTIMIZED_IR:-}" PROM_SHIM_DISABLE_NATIVE_AGGREGATION_LABEL_PROJECTION="${PROM_SHIM_DISABLE_NATIVE_AGGREGATION_LABEL_PROJECTION:-}" PROM_SHIM_DISABLE_NATIVE_REPEATED_SUBEXPRESSION_REUSE="${PROM_SHIM_DISABLE_NATIVE_REPEATED_SUBEXPRESSION_REUSE:-}" PROM_SHIM_DISABLE_LOCAL_REPEATED_EXPRESSION_CACHE="${PROM_SHIM_DISABLE_LOCAL_REPEATED_EXPRESSION_CACHE:-}" docker compose "${compose_args[@]}" "$@")
}

start_bench_stack() {
  log "Starting isolated benchmark stack (transport=${TRANSPORT}; rebuilding buildable services if needed)."
  compose up -d --build
  wait_for_http "ClickHouse" "${BENCH_CH_URL}/ping" "-u" "default:otel"
  wait_for_ch_schema
  wait_for_http "Prometheus" "${BENCH_PROM_URL}/-/ready"
  wait_for_http "promshim" "${BENCH_SHIM_URL}/-/ready"
}

wait_for_http() {
  local name="$1" url="$2"
  shift 2
  local deadline=$(( $(date +%s) + 90 ))
  while (( $(date +%s) < deadline )); do
    if curl -sf "$@" -o /dev/null "$url"; then
      return 0
    fi
    sleep 1
  done
  fatal "$name did not become ready at $url"
}

wait_for_ch_schema() {
  local deadline=$(( $(date +%s) + 90 ))
  local sql="SELECT count() FROM system.tables WHERE database = 'observability' AND name = 'prometheus' FORMAT TabSeparated"
  local out
  while (( $(date +%s) < deadline )); do
    if out=$(curl -sfS -u default:otel --data-binary "$sql" "${BENCH_CH_URL}/?database=system" 2>/dev/null) && [[ "${out//$'\n'/}" != "0" ]]; then
      return 0
    fi
    sleep 1
  done
  fatal "ClickHouse benchmark schema did not become ready"
}

prom_marker_present() {
  local profile="$1" density="$2" eval_time="$3"
  python3 - "$BENCH_PROM_URL" "$profile" "$density" "$eval_time" <<'PY'
import json, sys, urllib.parse, urllib.request
base, profile, density, eval_time = sys.argv[1:5]
query = f'promshim_seed_info{{profile="{profile}",density="{density}"}}'
url = base + "/api/v1/query?" + urllib.parse.urlencode({"query": query, "time": eval_time})
try:
    with urllib.request.urlopen(url, timeout=5) as r:
        data = json.load(r)
except Exception:
    sys.exit(1)
if data.get("status") == "success" and data.get("data", {}).get("result"):
    sys.exit(0)
sys.exit(1)
PY
}

ch_marker_present() {
  local profile="$1" density="$2" eval_time="$3"
  local promql="promshim_seed_info{profile=\"${profile}\",density=\"${density}\"}"
  local sql
  sql=$(cat <<SQL
SELECT count()
FROM prometheusQuery('observability', 'prometheus', '${promql}', parseDateTimeBestEffort('${eval_time}'))
SETTINGS allow_experimental_time_series_table = 1
FORMAT TabSeparated
SQL
)
  local out
  if ! out=$(curl -sfS -u default:otel --data-binary "$sql" "${BENCH_CH_URL}/?database=observability" 2>/dev/null); then
    return 1
  fi
  [[ "${out//$'\n'/}" != "0" && -n "${out//$'\n'/}" ]]
}

marker_status() {
  local profile="$1" density="$2" target="$3" eval_time
  eval_time=$(profile_end_time "$profile" "$density")
  case "$target" in
    prom)
      if prom_marker_present "$profile" "$density" "$eval_time"; then echo present; else echo missing; fi
      ;;
    ch)
      if ch_marker_present "$profile" "$density" "$eval_time"; then echo present; else echo missing; fi
      ;;
  esac
}

show_status() {
  echo "Benchmark stack: ${BENCH_DIR}"
  echo "  Prometheus: ${BENCH_PROM_URL}"
  echo "  promshim:   ${BENCH_SHIM_URL}"
  echo "  ClickHouse: ${BENCH_CH_URL}"
  echo
  echo "Containers:"
  compose ps || true
  echo
  echo "Seed markers:"
  for p in $(profiles_for "$PROFILE"); do
    for d in $(densities_for "$DENSITY"); do
      local eval_time prom_state ch_state
      eval_time=$(profile_end_time "$p" "$d")
      if curl -sf -o /dev/null "${BENCH_PROM_URL}/-/ready"; then
        prom_state=$(marker_status "$p" "$d" prom || true)
      else
        prom_state="unknown(prometheus-down)"
      fi
      if curl -sf -u default:otel -o /dev/null "${BENCH_CH_URL}/ping"; then
        ch_state=$(marker_status "$p" "$d" ch || true)
      else
        ch_state="unknown(clickhouse-down)"
      fi
      printf '  %-3s %-6s eval=%s prom=%s ch=%s\n' "$p" "$d" "$eval_time" "${prom_state:-missing}" "${ch_state:-missing}"
    done
  done
}

seed_dataset() {
  local profile="$1" density="$2" target="$3"
  log "Seeding profile=${profile} density=${density} target=${target} into isolated benchmark stack."
  "${REPO_ROOT}/scripts/seed-long-range.sh" \
    --profile "$profile" \
    --density "$density" \
    --target "$target" \
    --ch-url "$BENCH_CH_URL" \
    --prom-url "$BENCH_PROM_URL" \
    --ch-endpoint "$BENCH_CH_WRITE_ENDPOINT" \
    --prom-endpoint "$BENCH_PROM_WRITE_ENDPOINT"
}

setup_selected() {
  start_bench_stack
  for p in $(profiles_for "$PROFILE"); do
    for d in $(densities_for "$DENSITY"); do
      local prom_state="skipped" ch_state="skipped" seed_target=""
      if [[ "$TARGET" == "both" || "$TARGET" == "prom" ]]; then
        prom_state=$(marker_status "$p" "$d" prom || true)
      fi
      if [[ "$TARGET" == "both" || "$TARGET" == "ch" ]]; then
        ch_state=$(marker_status "$p" "$d" ch || true)
      fi

      case "$SEED_POLICY" in
        never)
          log "Skipping seed checks/writes for profile=${p} density=${d} (--seed never)."
          continue
          ;;
        reuse)
          if { target_includes "$TARGET" prom && [[ "$prom_state" != "present" ]]; } || { target_includes "$TARGET" ch && [[ "$ch_state" != "present" ]]; }; then
            cat >&2 <<EOF
Missing benchmark dataset: profile=${p} density=${d} target=${TARGET}
Run:
  ./scripts/run-sweep.sh --setup --profile ${p} --density ${d} --target ${TARGET}
EOF
            exit 2
          fi
          log "Dataset already present: profile=${p} density=${d} target=${TARGET}."
          ;;
        missing)
          if [[ "$TARGET" == "both" ]]; then
            if [[ "$prom_state" != "present" && "$ch_state" != "present" ]]; then seed_target="both"
            elif [[ "$prom_state" != "present" ]]; then seed_target="prom"
            elif [[ "$ch_state" != "present" ]]; then seed_target="ch"
            fi
          elif [[ "$TARGET" == "prom" && "$prom_state" != "present" ]]; then seed_target="prom"
          elif [[ "$TARGET" == "ch" && "$ch_state" != "present" ]]; then seed_target="ch"
          fi
          if [[ -n "$seed_target" ]]; then
            seed_dataset "$p" "$d" "$seed_target"
          else
            log "Dataset already present: profile=${p} density=${d} target=${TARGET}."
          fi
          ;;
        always)
          seed_dataset "$p" "$d" "$TARGET"
          ;;
      esac
    done
  done
}

corpus_paths_for() {
  local profile="$1" density="$2" set="$3" suffix=""
  case "$profile" in
    7d|30d|1y) suffix="-${profile}" ;;
    *) fatal "unsupported bench profile for corpus: ${profile}" ;;
  esac
  if [[ "$set" == "native" || "$set" == "both" ]]; then
    echo "harness/corpus/bench-native-lowering${suffix}.json"
  fi
  if [[ "$set" == "processing" || "$set" == "both" ]]; then
    echo "harness/corpus/bench-processing${suffix}.json"
  fi
  if [[ "$set" == "optimization" ]]; then
    echo "harness/corpus/bench-optimization-tuning${suffix}.json"
  fi
}

estimate_samples() {
  local profile="$1" density="$2"
  python3 - "$profile" "$density" <<'PY'
import sys
profile, density = sys.argv[1:3]
profiles = {
    "7d": (7 * 24 * 3600, 15),
    "30d": (30 * 24 * 3600, 60),
    "1y": (365 * 24 * 3600, 300),
}
duration_seconds, step_seconds = profiles[profile]
points = duration_seconds // step_seconds
instances_per_job = 5 if density == "sparse" else (50 if profile == "1y" else 100)
jobs = 2
series_per_instance = 13
series = jobs * instances_per_job * series_per_instance
samples = series * points
print(f"series≈{series:,} points/series≈{points:,} samples≈{samples:,} disk≈{samples*60/1024**3:.1f}GiB-headroom")
PY
}

print_sweep_plan() {
  local artifact_dir="harness/artifacts/sweeps/${RUN_NAME}"
  echo "Sweep run: ${RUN_NAME}"
  echo "Artifacts: ${artifact_dir}"
  echo "Transport: ${TRANSPORT}"
  echo "Seed policy: ${SEED_POLICY}"
  echo "Compliance: $([[ $SKIP_COMPLIANCE == 1 ]] && echo skipped || echo enabled)"
  echo "Benchmark: $([[ $SKIP_BENCH == 1 ]] && echo skipped || echo enabled)"
  echo "Benchmark modes: ${SHIM_MODES}"
  echo "Routing policies: ${ROUTING_POLICIES}"
  echo "Warmup routing policies: ${WARMUP_ROUTING_POLICIES:-none}"
  echo "Cost routing local families: ${COST_ROUTING_LOCAL_FAMILIES:-none}"
  echo "Memory mode: ${MEMORY_MODE}"
  echo "ClickHouse reference profile: ${CLICKHOUSE_REFERENCE_PROFILE}"
  echo "promshim settings profile: ${SETTINGS_PROFILE}"
  echo
  echo "Datasets:"
  for p in $(profiles_for "$PROFILE"); do
    for d in $(densities_for "$DENSITY"); do
      printf '  %-3s %-6s eval=%s' "$p" "$d" "$(profile_end_time "$p" "$d")"
      if (( ESTIMATE == 1 )); then printf ' %s' "$(estimate_samples "$p" "$d")"; fi
      echo
    done
  done
  echo
  if (( SKIP_BENCH == 0 )); then
    echo "Benchmark corpora:"
    for p in $(profiles_for "$PROFILE"); do
      for d in $(densities_for "$DENSITY"); do
        while IFS= read -r corpus; do
          [[ -n "$corpus" ]] && printf '  %-3s %-6s %s\n' "$p" "$d" "$corpus"
        done < <(corpus_paths_for "$p" "$d" "$CORPUS_SET")
      done
    done
  fi
}

generate_sweep_artifacts() {
  local artifact_dir="$1" compliance_status="$2" bench_status="$3"
  python3 - "$REPO_ROOT" "$artifact_dir" "$RUN_NAME" "$PROFILE" "$DENSITY" "$TRANSPORT" "$SEED_POLICY" "$SHIM_MODES" "$ROUTING_POLICIES" "$WARMUP_ROUTING_POLICIES" "$COST_ROUTING_LOCAL_FAMILIES" "$INCLUDE_PROM" "$CORPUS_SET" "$compliance_status" "$bench_status" "$BENCH_PROM_URL" "$BENCH_SHIM_URL" "$BENCH_CH_URL" "$MEMORY_MODE" "$CLICKHOUSE_REFERENCE_PROFILE" "$SETTINGS_PROFILE" <<'PY'
import json, pathlib, subprocess, sys
from datetime import datetime, timezone

(repo, artifact_dir, run_name, profile, density, transport, seed_policy,
 shim_modes, routing_policies, warmup_routing_policies, cost_routing_local_families, include_prom, corpus_set, compliance_status, bench_status,
 prom_url, shim_url, ch_url, memory_mode, clickhouse_reference_profile, settings_profile) = sys.argv[1:22]
root = pathlib.Path(repo)
out_dir = root / artifact_dir
reports = []
memory_reports = []
memory_details = []
strategy_hist = {}
routing_policy_hist = {}
target_bands = {}
slow_rows = []
for mem_path in sorted(out_dir.glob("memory-summary*.json")):
    memory_reports.append(str(mem_path.relative_to(root)))
for detail_path in sorted(out_dir.glob("memory-detail*/manifest.json")):
    memory_details.append(str(detail_path.relative_to(root)))
for path in sorted(out_dir.glob("bench-report*.json")):
    try:
        data = json.loads(path.read_text())
    except Exception:
        continue
    if data.get("schemaVersion") != 2:
        continue
    rel = str(path.relative_to(root))
    labels = data.get("runLabels") or {}
    report_routing_policies = set()
    for row in data.get("rows") or []:
        for result in (row.get("shim") or {}).values():
            if result.get("routingPolicy"):
                report_routing_policies.add(result["routingPolicy"])
    reports.append({
        "path": rel,
        "corpusPath": data.get("corpusPath"),
        "profile": labels.get("profile"),
        "density": labels.get("density"),
        "transport": labels.get("transport"),
        "routingPolicies": sorted(report_routing_policies),
        "rowCount": len(data.get("rows") or []),
    })
    for key, value in (data.get("summary", {}).get("strategyHistogram") or {}).items():
        strategy_hist[key] = strategy_hist.get(key, 0) + value
    for row in data.get("rows") or []:
        band = row.get("promBand") or "n/a"
        target_bands[band] = target_bands.get(band, 0) + 1
        prom = (row.get("prom") or {}).get("p50Ms")
        for mode, result in (row.get("shim") or {}).items():
            p50 = result.get("p50Ms") or 0
            routing_policy = result.get("routingPolicy") or "unknown"
            routing_policy_hist[routing_policy] = routing_policy_hist.get(routing_policy, 0) + 1
            slow_rows.append({
                "query": row.get("name"),
                "category": row.get("category"),
                "mode": mode,
                "routingPolicy": routing_policy,
                "strategy": result.get("strategy"),
                "promP50Ms": prom,
                "shimP50Ms": p50,
                "shimPromRatio": (p50 / prom) if prom else None,
                "report": rel,
            })
slow_rows.sort(key=lambda r: (r.get("shimP50Ms") or 0), reverse=True)

def run_cmd_result(args, cwd=root):
    try:
        completed = subprocess.run(args, cwd=cwd, check=True, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    except FileNotFoundError as exc:
        return {"ok": False, "stdout": "", "stderr": str(exc), "returncode": None}
    except subprocess.CalledProcessError as exc:
        return {
            "ok": False,
            "stdout": (exc.stdout or "").strip(),
            "stderr": (exc.stderr or "").strip(),
            "returncode": exc.returncode,
        }
    return {"ok": True, "stdout": completed.stdout.strip(), "stderr": completed.stderr.strip(), "returncode": completed.returncode}

def run_cmd(args, cwd=root):
    result = run_cmd_result(args, cwd=cwd)
    return result["stdout"] if result["ok"] else None

def benchmark_stack_provenance():
    git_status = run_cmd(["git", "status", "--porcelain"]) or ""
    provenance = {
        "composeBuildRequested": bench_status != "skipped",
        "git": {
            "revision": run_cmd(["git", "rev-parse", "HEAD"]),
            "dirty": bool(git_status),
            "statusPorcelain": git_status.splitlines()[:50],
        },
        "promshim": {},
    }
    if bench_status == "skipped":
        return provenance
    bench_dir = root / "harness" / "bench"
    compose_args = ["docker", "compose", "-f", "docker-compose.yml"]
    if clickhouse_reference_profile == "promshim-ch-timeseries-reference-v1":
        compose_args += ["-f", "docker-compose.reference.yml"]
    provenance["composeFiles"] = compose_args[3::2]
    container_result = run_cmd_result(compose_args + ["ps", "-q", "promshim"], cwd=bench_dir)
    container_id = container_result["stdout"]
    if not container_result["ok"] or not container_id:
        provenance["promshim"].update({
            "available": False,
            "reason": "compose_ps_failed" if not container_result["ok"] else "container_not_found",
            "returncode": container_result["returncode"],
            "stderr": container_result["stderr"],
        })
        return provenance
    provenance["promshim"]["containerId"] = container_id
    inspect_result = run_cmd_result(["docker", "inspect", container_id])
    raw = inspect_result["stdout"]
    if not inspect_result["ok"] or not raw:
        provenance["promshim"].update({
            "available": False,
            "reason": "docker_inspect_failed",
            "returncode": inspect_result["returncode"],
            "stderr": inspect_result["stderr"],
        })
        return provenance
    try:
        info = json.loads(raw)[0]
    except Exception as exc:
        provenance["promshim"].update({"available": False, "reason": "docker_inspect_invalid_json", "error": str(exc)})
        return provenance
    config = info.get("Config") or {}
    provenance["promshim"].update({
        "available": True,
        "containerName": (info.get("Name") or "").lstrip("/"),
        "containerCreatedAt": info.get("Created"),
        "image": config.get("Image"),
        "imageId": info.get("Image"),
    })
    return provenance

manifest = {
    "schemaVersion": 1,
    "runName": run_name,
    "generatedAt": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
    "artifactDir": artifact_dir,
    "axes": {
        "profile": profile,
        "density": density,
        "transport": transport,
        "seedPolicy": seed_policy,
        "shimModes": [m for m in shim_modes.split(",") if m],
        "routingPolicies": [p for p in routing_policies.split(",") if p] or sorted(k for k in routing_policy_hist.keys() if k != "unknown"),
        "warmupRoutingPolicies": [p for p in warmup_routing_policies.split(",") if p],
        "costRoutingLocalFamilies": [f for f in cost_routing_local_families.split(",") if f],
        "includeProm": include_prom,
        "memoryMode": memory_mode,
        "clickHouseReferenceProfile": clickhouse_reference_profile,
        "promshimSettingsProfile": settings_profile,
        "corpusSet": corpus_set,
    },
    "endpoints": {"prometheus": prom_url, "promshim": shim_url, "clickhouse": ch_url},
    "benchmarkStack": benchmark_stack_provenance(),
    "compliance": {"status": compliance_status, "log": f"{artifact_dir}/compliance.log" if compliance_status != "skipped" else None},
    "bench": {"status": bench_status, "reports": reports, "memoryReports": memory_reports, "memoryDetailManifests": memory_details},
    "summaries": {"markdown": f"{artifact_dir}/summary.md", "json": f"{artifact_dir}/summary.json"},
}
summary = {
    "schemaVersion": 1,
    "runName": run_name,
    "complianceStatus": compliance_status,
    "benchStatus": bench_status,
    "reportCount": len(reports),
    "memoryReportCount": len(memory_reports),
    "memoryDetailCount": len(memory_details),
    "strategyHistogram": strategy_hist,
    "routingPolicyHistogram": routing_policy_hist,
    "targetBands": target_bands,
    "clickHouseReferenceProfile": clickhouse_reference_profile,
    "promshimSettingsProfile": settings_profile,
    "topSlowRows": slow_rows[:20],
}
out_dir.mkdir(parents=True, exist_ok=True)
(out_dir / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
(out_dir / "summary.json").write_text(json.dumps(summary, indent=2) + "\n")
lines = [
    f"# Sweep summary: {run_name}",
    "",
    f"- Artifacts: `{artifact_dir}`",
    f"- Compliance: `{compliance_status}`",
    f"- Benchmark: `{bench_status}`",
    f"- Reports: `{len(reports)}`",
    f"- Transport: `{transport}`",
    f"- Profiles: `{profile}`",
    f"- Density: `{density}`",
    f"- Modes: `{shim_modes}`",
    f"- Routing policies: `{routing_policies or ','.join(sorted(k for k in routing_policy_hist.keys() if k != 'unknown')) or 'n/a'}`",
    f"- Warmup routing policies: `{warmup_routing_policies or 'none'}`",
    f"- Cost routing local families: `{cost_routing_local_families or 'none'}`",
    f"- Memory mode: `{memory_mode}`",
    f"- ClickHouse reference profile: `{clickhouse_reference_profile}`",
    f"- promshim settings profile: `{settings_profile}`",
    f"- Memory summaries: `{len(memory_reports)}`",
    f"- Memory detail manifests: `{len(memory_details)}`",
    "",
    "## Strategy histogram",
    "",
]
if strategy_hist:
    for key, value in sorted(strategy_hist.items()):
        lines.append(f"- `{key}`: {value}")
else:
    lines.append("- No benchmark strategy data captured.")
lines += ["", "## Routing policy histogram", ""]
if routing_policy_hist:
    for key, value in sorted(routing_policy_hist.items()):
        lines.append(f"- `{key}`: {value}")
else:
    lines.append("- No routing policy data captured.")
lines += ["", "## Prometheus target bands", ""]
if target_bands:
    for key, value in sorted(target_bands.items()):
        lines.append(f"- `{key}`: {value}")
else:
    lines.append("- No target-band data captured.")
lines += ["", "## Top slow rows", "", "| Query | Mode | Routing policy | Strategy | Prom p50 ms | Shim p50 ms | S/P | Report |", "|---|---|---|---|---:|---:|---:|---|"]
for row in slow_rows[:10]:
    ratio = row.get("shimPromRatio")
    lines.append("| {query} | {mode} | {routing_policy} | {strategy} | {prom} | {shim} | {ratio} | `{report}` |".format(
        query=row.get("query") or "",
        mode=row.get("mode") or "",
        routing_policy=row.get("routingPolicy") or "",
        strategy=row.get("strategy") or "",
        prom=f"{row['promP50Ms']:.2f}" if row.get("promP50Ms") is not None else "—",
        shim=f"{row['shimP50Ms']:.2f}" if row.get("shimP50Ms") is not None else "—",
        ratio=f"{ratio:.1f}×" if ratio is not None else "—",
        report=row.get("report") or "",
    ))
(out_dir / "summary.md").write_text("\n".join(lines) + "\n")
print(f"Wrote {out_dir / 'manifest.json'}")
print(f"Wrote {out_dir / 'summary.md'}")
PY
}

run_sweep() {
  local artifact_dir="harness/artifacts/sweeps/${RUN_NAME}"
  local compliance_status="skipped" bench_status="skipped"
  print_sweep_plan
  if (( DRY_RUN == 1 )); then
    echo
    echo "Dry run only; no stack, seed, compliance, or benchmark commands executed."
    return 0
  fi
  mkdir -p "${REPO_ROOT}/${artifact_dir}"
  acquire_run_lock "sweep-${RUN_NAME}"
  if (( SKIP_COMPLIANCE == 0 )); then
    log "Running compliance pass for sweep ${RUN_NAME}."
    if "${REPO_ROOT}/scripts/run-compliance.sh" | tee "${REPO_ROOT}/${artifact_dir}/compliance.log"; then
      compliance_status="passed"
    else
      compliance_status="failed"
      generate_sweep_artifacts "$artifact_dir" "$compliance_status" "$bench_status"
      return 1
    fi
  fi
  if (( SKIP_BENCH == 0 )); then
    bench_status="passed"
    setup_selected
    for p in $(profiles_for "$PROFILE"); do
      for d in $(densities_for "$DENSITY"); do
        local eval_time
        eval_time=$(profile_end_time "$p" "$d")
        while IFS= read -r corpus; do
          [[ -n "$corpus" ]] || continue
          local stem artifact_name
          stem=$(basename "$corpus" .json)
          artifact_name="bench-report-${p}-${d}-${stem}.json"
          if [[ -n "$WARMUP_ROUTING_POLICIES" ]]; then
            log "Running warmup benchmark profile=${p} density=${d} corpus=${corpus} routing=${WARMUP_ROUTING_POLICIES}."
            if ! "${REPO_ROOT}/scripts/run-bench.sh" \
              --prom-url "$BENCH_PROM_URL" \
              --shim-url "$BENCH_SHIM_URL" \
              --artifact-dir "${artifact_dir}/warmup" \
              --artifact-name "warmup-report-${p}-${d}-${stem}.json" \
              --corpus "$corpus" \
              --eval-time "$eval_time" \
              --shim-modes "$SHIM_MODES" \
              --routing-policies "$WARMUP_ROUTING_POLICIES" \
              --include-prom false \
              --ch-url "$BENCH_CH_URL" \
              --memory off \
              --run-label "run=${RUN_NAME}" \
              --run-label "profile=${p}" \
              --run-label "density=${d}" \
              --run-label "transport=${TRANSPORT}" \
              --run-label "warmup=true" \
              --repeats 1 \
              --warmup 0 \
              --no-baseline; then
              bench_status="failed"
            fi
          fi
          log "Running benchmark profile=${p} density=${d} corpus=${corpus}."
          if ! "${REPO_ROOT}/scripts/run-bench.sh" \
            --prom-url "$BENCH_PROM_URL" \
            --shim-url "$BENCH_SHIM_URL" \
            --artifact-dir "$artifact_dir" \
            --artifact-name "$artifact_name" \
            --corpus "$corpus" \
            --eval-time "$eval_time" \
            --shim-modes "$SHIM_MODES" \
            --routing-policies "$ROUTING_POLICIES" \
            --include-prom "$INCLUDE_PROM" \
            --ch-url "$BENCH_CH_URL" \
            --memory "$MEMORY_MODE" \
            --run-label "run=${RUN_NAME}" \
            --run-label "profile=${p}" \
            --run-label "density=${d}" \
            --run-label "transport=${TRANSPORT}" \
            --no-baseline \
            --matrix; then
            bench_status="failed"
          fi
        done < <(corpus_paths_for "$p" "$d" "$CORPUS_SET")
      done
    done
  fi
  generate_sweep_artifacts "$artifact_dir" "$compliance_status" "$bench_status"
  [[ "$bench_status" != "failed" ]]
}

case "$MODE" in
  status)
    show_status
    ;;
  reset)
    acquire_run_lock "bench-stack"
    if (( YES != 1 )); then
      fatal "--bench-reset deletes isolated benchmark volumes; pass --yes to confirm"
    fi
    log "Stopping isolated benchmark stack and deleting benchmark volumes only."
    compose down -v
    ;;
  setup)
    acquire_run_lock "bench-stack"
    setup_selected
    ;;
  run)
    run_sweep
    ;;
esac
