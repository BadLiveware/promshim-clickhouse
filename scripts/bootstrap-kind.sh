#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: ./scripts/bootstrap-kind.sh [options]

Create (or reuse) a local kind cluster and deploy the v1 PoC Helm chart using `helm template` + `kubectl apply` (never `helm install`).

Options:
  --cluster-name <name>             Kind cluster name (default: ch-observability-poc)
  --namespace <name>                Kubernetes namespace (default: monitoring-v2)
  --release-name <name>             Helm release name (default: monitoring)
  --chart-path <path>               Main chart path (default: chart/ch-observability-poc)
  --k8s-monitoring-chart-path <path>
                                  Kubernetes monitoring chart path (default: chart/k8s-monitoring)
  --cnpg-chart-path <path>          CNPG helper chart path (default: chart/ch-observability-cnpg)
  --node-image <image>              kindest/node image (default: kindest/node:v1.34.3)
  --kubeconfig-output <path>        Write kind kubeconfig to this file (overwrites if exists).
                                  Defaults to ~/.kube/kind-${CLUSTER_NAME}.kubeconfig when omitted.
  --recreate                        Delete and recreate the kind cluster.
  --no-cnpg                         Skip deploying the CNPG helper chart (assumes external PostgreSQL).
  --clickhouse-cluster-name <name>   ClickHouse operator cluster name (default: monitoring).
  --keeper-name <name>              Keeper cluster name (default: <clickhouse-cluster-name>-keeper).
  --no-otel-operator                Skip OpenTelemetry Operator subchart install (assumes operator already exists).
  --no-smoke                        Skip post-deploy readiness/smoke validation.
  --otel-service-name <name>         Optional override for OTel service/collector name (default: otel-collector).
  -h, --help                        Show this help text.
EOF
}

log() {
  printf '[%s] %s\n' "$(date +'%Y-%m-%d %H:%M:%S')" "$1"
}

fatal() {
  echo "Error: $*" >&2
  exit 1
}

ensure_command() {
  local cmd="$1"
  if ! command -v "$cmd" >/dev/null 2>&1; then
    fatal "Required command not found: $cmd"
  fi
}

REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

CLUSTER_NAME="ch-observability-poc"
NAMESPACE="monitoring-v2"
RELEASE_NAME="monitoring"
CHART_PATH="${REPO_ROOT}/chart/ch-observability-poc"
K8S_MONITORING_CHART_PATH="${REPO_ROOT}/chart/k8s-monitoring"
CNPG_CHART_PATH="${REPO_ROOT}/chart/ch-observability-cnpg"
NODE_IMAGE="kindest/node:v1.34.3"
RECREATE=0
RUN_SMOKE=1
OPENTELEMETRY_OPERATOR_ENABLED=1
INSTALL_CNPG=1
KUBECONFIG_OUTPUT=""
CLICKHOUSE_CLUSTER_NAME="monitoring"
KEEPER_CLUSTER_NAME="${CLICKHOUSE_CLUSTER_NAME}-keeper"
OTEL_SERVICE_NAME="otel-collector"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --cluster-name)
      CLUSTER_NAME="$2"
      shift 2
      ;;
    --namespace)
      NAMESPACE="$2"
      shift 2
      ;;
    --release-name)
      RELEASE_NAME="$2"
      shift 2
      ;;
    --chart-path)
      CHART_PATH="$2"
      shift 2
      ;;
    --k8s-monitoring-chart-path)
      K8S_MONITORING_CHART_PATH="$2"
      shift 2
      ;;
    --cnpg-chart-path)
      CNPG_CHART_PATH="$2"
      shift 2
      ;;
    --node-image)
      NODE_IMAGE="$2"
      shift 2
      ;;
    --kubeconfig-output)
      KUBECONFIG_OUTPUT="$2"
      shift 2
      ;;
    --recreate)
      RECREATE=1
      shift
      ;;
    --no-cnpg)
      INSTALL_CNPG=0
      shift
      ;;
    --clickhouse-cluster-name)
      CLICKHOUSE_CLUSTER_NAME="$2"
      KEEPER_CLUSTER_NAME="${CLICKHOUSE_CLUSTER_NAME}-keeper"
      shift 2
      ;;
    --keeper-name)
      KEEPER_CLUSTER_NAME="$2"
      shift 2
      ;;
    --otel-service-name)
      OTEL_SERVICE_NAME="$2"
      shift 2
      ;;
    --no-otel-operator)
      OPENTELEMETRY_OPERATOR_ENABLED=0
      shift
      ;;
    --no-smoke)
      RUN_SMOKE=0
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      fatal "Unknown argument: $1"
      ;;
  esac
done

ensure_command kind
ensure_command kubectl
ensure_command helm
ensure_command docker
ensure_command curl

if [[ ! -d "$CHART_PATH" ]]; then
  fatal "Chart path not found: $CHART_PATH"
fi

if [[ ! -f "$CHART_PATH/Chart.yaml" ]]; then
  fatal "Invalid chart path (missing Chart.yaml): $CHART_PATH"
fi

if [[ ! -d "$K8S_MONITORING_CHART_PATH" ]]; then
  fatal "Kubernetes monitoring chart path not found: $K8S_MONITORING_CHART_PATH"
fi

if [[ ! -f "$K8S_MONITORING_CHART_PATH/Chart.yaml" ]]; then
  fatal "Invalid Kubernetes monitoring chart path (missing Chart.yaml): $K8S_MONITORING_CHART_PATH"
fi

if (( INSTALL_CNPG == 1 )); then
  if [[ ! -d "$CNPG_CHART_PATH" ]]; then
    fatal "CNPG helper chart path not found: $CNPG_CHART_PATH"
  fi

  if [[ ! -f "$CNPG_CHART_PATH/Chart.yaml" ]]; then
    fatal "Invalid CNPG chart path (missing Chart.yaml): $CNPG_CHART_PATH"
  fi
fi

if (( RECREATE == 1 )); then
  if kind get clusters | grep -Fxq "$CLUSTER_NAME"; then
    log "Recreate requested; deleting existing cluster '$CLUSTER_NAME'."
    kind delete cluster --name "$CLUSTER_NAME"
  fi
fi

KUBECTL_CONTEXT="kind-${CLUSTER_NAME}"

if ! kind get clusters | grep -Fxq "$CLUSTER_NAME"; then
  KIND_CONFIG=$(mktemp)
  trap 'rm -f "$KIND_CONFIG"' EXIT

  cat >"$KIND_CONFIG" <<EOF
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
  - role: control-plane
    image: ${NODE_IMAGE}
  - role: worker
    image: ${NODE_IMAGE}
  - role: worker
    image: ${NODE_IMAGE}
EOF

  log "Creating kind cluster '$CLUSTER_NAME' with 3 nodes."
  kind create cluster --name "$CLUSTER_NAME" --config "$KIND_CONFIG"
else
  log "Using existing cluster '$CLUSTER_NAME'."
fi

# Default kubeconfig path for this repository's kind bootstrap flow.
if [[ -z "$KUBECONFIG_OUTPUT" ]]; then
  KUBECONFIG_OUTPUT="$HOME/.kube/kind-${CLUSTER_NAME}.kubeconfig"
else
  KUBECONFIG_OUTPUT="${KUBECONFIG_OUTPUT/#\~/$HOME}"
fi
mkdir -p "$(dirname "$KUBECONFIG_OUTPUT")"
log "Writing kind kubeconfig to '$KUBECONFIG_OUTPUT'."
kind get kubeconfig --name "$CLUSTER_NAME" > "$KUBECONFIG_OUTPUT"
chmod 600 "$KUBECONFIG_OUTPUT"

log "Deploying Helm chart '$CHART_PATH' to namespace '$NAMESPACE' as release '$RELEASE_NAME' via 'helm template' + 'kubectl apply'."
kubectl config use-context "$KUBECTL_CONTEXT" >/dev/null
log "Using kube-context '$KUBECTL_CONTEXT'."

CHART_SET_ARGS=()
if (( OPENTELEMETRY_OPERATOR_ENABLED == 0 )); then
  CHART_SET_ARGS=(--set opentelemetryoperator.enabled=false)
fi

KUBECTL_APPLY_ARGS=(--server-side --field-manager=ch-observability-bootstrap --force-conflicts)

log "Rendering Helm manifests and applying via kubectl."
# Namespace may not exist when we render/apply directly.
kubectl --context "$KUBECTL_CONTEXT" apply "${KUBECTL_APPLY_ARGS[@]}" -f <(cat <<EOF
apiVersion: v1
kind: Namespace
metadata:
  name: "$NAMESPACE"
EOF
)
CHART_RENDER="$(mktemp)"
helm template "$RELEASE_NAME" "$CHART_PATH" --namespace "$NAMESPACE" --include-crds "${CHART_SET_ARGS[@]}" >"$CHART_RENDER"

log "Including Kubernetes monitoring chart '$K8S_MONITORING_CHART_PATH' in render pass."
helm template "$RELEASE_NAME" "$K8S_MONITORING_CHART_PATH" --namespace "$NAMESPACE" --include-crds >>"$CHART_RENDER"

if (( INSTALL_CNPG == 1 )); then
  log "Including CNPG helper chart '$CNPG_CHART_PATH' in render pass."
  helm template "${RELEASE_NAME}-cnpg" "$CNPG_CHART_PATH" --namespace "$NAMESPACE" --include-crds >>"$CHART_RENDER"
else
  log "Skipping CNPG helper chart; Grafana DB is expected to be external."
fi

# Operational retry loop: wait for CRDs that are part of this rendered chart, then re-apply.
mapfile -t CHART_CRDS < <(awk '
  $1 == "kind:" && $2 == "CustomResourceDefinition" {inCrd = 1; inMetadata = 0; next}
  inCrd && $1 == "metadata:" {inMetadata = 1; next}
  inCrd && inMetadata && $1 == "name:" {gsub(":", "", $2); print $2; inCrd = 0; inMetadata = 0}
' "$CHART_RENDER")
mapfile -t CRD_WAIT_ARGS < <(printf '%s\n' "${CHART_CRDS[@]}" | awk 'NF && !seen[$0]++ {print "crd/" $0}')

if ! kubectl --context "$KUBECTL_CONTEXT" apply "${KUBECTL_APPLY_ARGS[@]}" -f "$CHART_RENDER"; then
  log "Initial manifest apply hit ordering/type constraints. Waiting for CRDs and retrying..."

  for _ in $(seq 1 30); do
    MISSING=0
    for crd in "${CRD_WAIT_ARGS[@]}"; do
      if ! kubectl --context "$KUBECTL_CONTEXT" get "$crd" >/dev/null 2>&1; then
        MISSING=1
        break
      fi
    done

    if (( MISSING == 0 )); then
      break
    fi

    sleep 2
  done

  if (( ${#CRD_WAIT_ARGS[@]} > 0 )); then
    kubectl --context "$KUBECTL_CONTEXT" wait --for=condition=Established --timeout=120s "${CRD_WAIT_ARGS[@]}" || true
  fi

  if (( MISSING != 0 )); then
    fatal "Required CRDs did not become available before retry timeout."
  fi

  if (( INSTALL_CNPG == 1 )); then
    log "Waiting for CloudNativePG webhook/operator to become callable before retrying manifest apply."

    # Prefer selector-based waits to avoid brittle historical naming.
    if kubectl --context "$KUBECTL_CONTEXT" -n "$NAMESPACE" get deploy -l app.kubernetes.io/name=cloudnative-pg >/dev/null 2>&1; then
      kubectl --context "$KUBECTL_CONTEXT" -n "$NAMESPACE" wait --for=condition=available --timeout=180s deploy -l app.kubernetes.io/name=cloudnative-pg || true
    elif kubectl --context "$KUBECTL_CONTEXT" -n "$NAMESPACE" get deploy -l app.kubernetes.io/name=cloudnativepg >/dev/null 2>&1; then
      kubectl --context "$KUBECTL_CONTEXT" -n "$NAMESPACE" wait --for=condition=available --timeout=180s deploy -l app.kubernetes.io/name=cloudnativepg || true
    else
      log "CloudNativePG operator Deployment not found yet (legacy selector). Polling webhook endpoint directly."
    fi

    local_wait=0
    cnpg_webhook_ips=""
    while (( local_wait < 60 )); do
      cnpg_webhook_ips=$(kubectl --context "$KUBECTL_CONTEXT" -n "$NAMESPACE" get endpoints cnpg-webhook-service -o jsonpath='{range .subsets[*].addresses[*]}{.ip}{" "}{end}' 2>/dev/null || true)
      if [[ -n "${cnpg_webhook_ips// /}" ]]; then
        break
      fi
      sleep 2
      local_wait=$((local_wait + 2))
    done

    if [[ -z "${cnpg_webhook_ips// /}" ]]; then
      fatal "CNPG webhook endpoint is not ready yet."
    fi
  fi

  kubectl --context "$KUBECTL_CONTEXT" apply "${KUBECTL_APPLY_ARGS[@]}" -f "$CHART_RENDER" || fatal "Manifest apply failed after CRD wait/retry."
fi
rm -f "$CHART_RENDER"

# Ensure the rendered chart resources are fully reconciled before readiness checks.
kubectl --context "$KUBECTL_CONTEXT" -n "$NAMESPACE" wait --for=condition=ready pod -l app.kubernetes.io/component=grafana --timeout=600s
kubectl --context "$KUBECTL_CONTEXT" -n "$NAMESPACE" rollout status deploy/cloudbeaver --timeout=10m
if (( OPENTELEMETRY_OPERATOR_ENABLED == 1 )); then
  kubectl --context "$KUBECTL_CONTEXT" -n "$NAMESPACE" rollout status deploy -l app.kubernetes.io/name=opentelemetry-operator --timeout=10m || true
fi

if (( RUN_SMOKE == 0 )); then
  log "Smoke checks skipped."
  log "Done."
  exit 0
fi

log "Waiting for ClickHouse, Grafana, and CloudBeaver pods to become ready."
CLICKHOUSE_POD_SELECTOR="app=${CLICKHOUSE_CLUSTER_NAME}-clickhouse"
kubectl --context "$KUBECTL_CONTEXT" -n "$NAMESPACE" wait --for=condition=ready pod \
  -l "$CLICKHOUSE_POD_SELECTOR" \
  --timeout=600s
kubectl --context "$KUBECTL_CONTEXT" -n "$NAMESPACE" wait --for=condition=ready pod \
  -l app.kubernetes.io/component=grafana \
  --timeout=600s
kubectl --context "$KUBECTL_CONTEXT" -n "$NAMESPACE" wait --for=condition=ready pod \
  -l app.kubernetes.io/component=cloudbeaver \
  --timeout=600s

if kubectl --context "$KUBECTL_CONTEXT" -n "$NAMESPACE" get pod -l app.kubernetes.io/component=otel --no-headers >/dev/null 2>&1; then
  kubectl --context "$KUBECTL_CONTEXT" -n "$NAMESPACE" wait --for=condition=ready pod \
    -l app.kubernetes.io/component=otel \
    --timeout=600s || true
fi

CLICKHOUSE_POD="$(kubectl --context "$KUBECTL_CONTEXT" -n "$NAMESPACE" get pod -l "$CLICKHOUSE_POD_SELECTOR" -o jsonpath='{.items[0].metadata.name}' || true)"
if [[ -z "${CLICKHOUSE_POD}" ]]; then
  CLICKHOUSE_POD="$(kubectl --context "$KUBECTL_CONTEXT" -n "$NAMESPACE" get pod -o name | awk -F/ -v cluster="${CLICKHOUSE_CLUSTER_NAME}" '$2 ~ "^" cluster {print $2; exit}')"
fi

if [[ -n "${CLICKHOUSE_POD}" ]]; then
  log "Running ClickHouse readiness query against pod $CLICKHOUSE_POD."
  kubectl --context "$KUBECTL_CONTEXT" -n "$NAMESPACE" exec "$CLICKHOUSE_POD" -- clickhouse-client --query "SELECT 1" >/tmp/ch-observability-clickhouse-smoke.out
else
  log "⚠️ No ClickHouse pod label match found for readiness query; skipping smoke SQL check."
fi

log "CloudBeaver/ Grafana / ClickHouse services are running."

# Lightweight HTTP smoke checks via port-forward.
check_http_via_port_forward() {
  local svc="$1"
  local port="$2"
  local path="$3"
  local label="$4"
  local pf_pid

  kubectl --context "$KUBECTL_CONTEXT" -n "$NAMESPACE" port-forward "svc/$svc" "${port}:${port}" >"/tmp/ch-observability-pf-${svc}-${port}.log" 2>&1 &
  pf_pid=$!
  for _ in $(seq 1 30); do
    if curl -fsS "http://127.0.0.1:${port}${path}" >/dev/null; then
      kill "$pf_pid" 2>/dev/null || true
      wait "$pf_pid" 2>/dev/null || true
      log "✓ ${label} HTTP check passed."
      return 0
    fi
    sleep 1
  done
  kill "$pf_pid" 2>/dev/null || true
  wait "$pf_pid" 2>/dev/null || true
  log "⚠️ ${label} HTTP check did not respond in time; service may still be starting." >&2
  return 1
}

check_http_via_port_forward cloudbeaver 8978 "/status" "CloudBeaver"
check_http_via_port_forward grafana 3000 "/api/health" "Grafana"

if kubectl --context "$KUBECTL_CONTEXT" -n "$NAMESPACE" get svc -l "operator.opentelemetry.io/collector-service-type=base" --no-headers >/dev/null 2>&1; then
  OTEL_BASE_SERVICE_NAME="$(kubectl --context "$KUBECTL_CONTEXT" -n "$NAMESPACE" get svc -l "operator.opentelemetry.io/collector-service-type=base" -o jsonpath='{.items[0].metadata.name}')"
  OTEL_EXTENSION_SERVICE_NAME="$(kubectl --context "$KUBECTL_CONTEXT" -n "$NAMESPACE" get svc -l "operator.opentelemetry.io/collector-service-type=extension" -o jsonpath='{.items[0].metadata.name}')"

  # Verify control-plane health endpoint (health extension service exposes HTTP).
  check_http_via_port_forward "$OTEL_EXTENSION_SERVICE_NAME" 13133 "/" "OpenTelemetry Collector extension"
elif kubectl --context "$KUBECTL_CONTEXT" -n "$NAMESPACE" get svc "$OTEL_SERVICE_NAME" >/dev/null 2>&1; then
  check_http_via_port_forward "$OTEL_SERVICE_NAME" 13133 "/" "OpenTelemetry Collector"
fi

log "Kind bootstrap and deploy completed."
log "Grafana default creds: admin / admin"
log "CloudBeaver default creds: cbadmin / admin"
cat <<EOF

Useful port-forwards (if you want persistent access):
  kubectl --context "$KUBECTL_CONTEXT" -n "$NAMESPACE" port-forward svc/grafana 3000:3000
  kubectl --context "$KUBECTL_CONTEXT" -n "$NAMESPACE" port-forward svc/cloudbeaver 8978:8978
  kubectl --context "$KUBECTL_CONTEXT" -n "$NAMESPACE" get svc -l "operator.opentelemetry.io/collector-service-type=base" -o jsonpath='{.items[0].metadata.name}' | xargs -r -I{} kubectl --context "$KUBECTL_CONTEXT" -n "$NAMESPACE" port-forward svc/{} 4317:4317 4318:4318
  kubectl --context "$KUBECTL_CONTEXT" -n "$NAMESPACE" get svc -l "operator.opentelemetry.io/collector-service-type=extension" -o jsonpath='{.items[0].metadata.name}' | xargs -r -I{} kubectl --context "$KUBECTL_CONTEXT" -n "$NAMESPACE" port-forward svc/{} 13133:13133
EOF
