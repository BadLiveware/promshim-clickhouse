-- Bootstrap database for OpenTelemetry data.
-- The OpenTelemetry ClickHouse exporter will create/maintain the OTEL tables
-- (otel_logs, otel_traces, otel_metrics_*) when create_schema=true.

CREATE DATABASE IF NOT EXISTS {{ .Values.clickhouse.env.database }};
