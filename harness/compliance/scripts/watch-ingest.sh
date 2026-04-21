#!/usr/bin/env bash
set -euo pipefail

PROM_URL=${PROM_URL:-http://localhost:29090}
CH_URL=${CH_URL:-http://localhost:28123}
CH_AUTH=${CH_AUTH:-default:otel}
TS_TABLE=${TS_TABLE:-observability.prometheus}

echo "--- $(date -u +%H:%M:%S) ---"

curl -s "${PROM_URL}/metrics" | awk '
  /^prometheus_tsdb_head_samples_appended_total[ {]/    {printf "prom ingest:     %s\n", $NF}
  /^prometheus_remote_storage_samples_in_total/         {printf "prom rw sent:    %s\n", $2}
  /^prometheus_remote_storage_samples_pending/          {printf "prom rw pending: %s\n", $2}
  /^prometheus_remote_storage_samples_dropped_total/    {printf "prom rw dropped: %s\n", $2}
  /^prometheus_remote_storage_samples_failed_total/     {printf "prom rw failed:  %s\n", $2}
'

printf "clickhouse rows: "
curl -s -u "${CH_AUTH}" -G --data-urlencode "query=SELECT count() FROM timeSeriesData(${TS_TABLE})" "${CH_URL}/"
echo
