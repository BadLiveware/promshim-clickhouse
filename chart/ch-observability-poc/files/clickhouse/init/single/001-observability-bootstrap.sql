-- Bootstrap database for observability data.
-- Logs and traces land in OTel-native tables created by the ClickHouse exporter.
-- Metrics land in a ClickHouse TimeSeries table through Prometheus remote-write.

CREATE DATABASE IF NOT EXISTS {{ .Values.clickhouse.env.database }};

CREATE TABLE IF NOT EXISTS {{ .Values.clickhouse.env.database }}.{{ .Values.clickhouse.timeSeries.table }}
ENGINE = TimeSeries;
