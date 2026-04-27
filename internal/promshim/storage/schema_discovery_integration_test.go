package storage

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/prometheus/model/labels"
)

func TestSchemaDiscoveryIntegrationNonDefaultIDType(t *testing.T) {
	client := requireHTTPIntegrationClient(t)
	defer func() { _ = client.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	const database = "observability"
	table := "schema_discovery_id_type_it"
	tableRef := "`" + escapeIdentifier(database) + "`.`" + escapeIdentifier(table) + "`"
	execIntegrationSQL(t, ctx, client, "DROP TABLE IF EXISTS "+tableRef+" SYNC")
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		execIntegrationSQL(t, cleanupCtx, client, "DROP TABLE IF EXISTS "+tableRef+" SYNC")
	})
	execIntegrationSQL(t, ctx, client, "CREATE TABLE "+tableRef+" (id UInt64, timestamp DateTime64(3), value Float64) ENGINE = TimeSeries")

	idType, err := DiscoverTimeSeriesIDType(ctx, client, QueryConfig{Database: database, Table: table})
	if err != nil {
		t.Fatalf("DiscoverTimeSeriesIDType: %v", err)
	}
	if idType != "UInt64" {
		t.Fatalf("id type = %q, want UInt64", idType)
	}
}

func TestSchemaDiscoveryIntegrationPromotedTagsAndIDType(t *testing.T) {
	client := requireHTTPIntegrationClient(t)
	defer func() { _ = client.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	const database = "observability"
	table := "schema_discovery_it"
	tableRef := "`" + escapeIdentifier(database) + "`.`" + escapeIdentifier(table) + "`"
	execIntegrationSQL(t, ctx, client, "DROP TABLE IF EXISTS "+tableRef+" SYNC")
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		execIntegrationSQL(t, cleanupCtx, client, "DROP TABLE IF EXISTS "+tableRef+" SYNC")
	})
	execIntegrationSQL(t, ctx, client, "CREATE TABLE "+tableRef+" ENGINE = TimeSeries SETTINGS tags_to_columns = {'job':'job', 'instance':'instance', 'renamed':'renamed_col'}")

	cfg := QueryConfig{Database: database, Table: table}
	idType, err := DiscoverTimeSeriesIDType(ctx, client, cfg)
	if err != nil {
		t.Fatalf("DiscoverTimeSeriesIDType: %v", err)
	}
	if idType != "UUID" {
		t.Fatalf("id type = %q, want UUID", idType)
	}

	promoted, err := DiscoverPromotedTagColumns(ctx, client, cfg)
	if err != nil {
		t.Fatalf("DiscoverPromotedTagColumns: %v", err)
	}
	for _, label := range []string{"instance", "job"} {
		if _, ok := promoted[label]; !ok {
			t.Fatalf("expected promoted label %q in %#v", label, promoted)
		}
	}
	if _, ok := promoted["renamed_col"]; ok {
		t.Fatalf("non-identity tags_to_columns entry should not be discovered as a Prometheus label: %#v", promoted)
	}
	if len(promoted) != 2 {
		t.Fatalf("promoted labels = %#v, want exactly instance and job", promoted)
	}

	transport := requireNativeDriverTransport(t)
	defer func() { _ = transport.Close() }()
	nativeClient := &Client{
		transportKind: TransportNative,
		transport:     transport,
		settingsProfile: SettingsProfileConfig{
			Name: SettingsProfileNone,
		},
	}

	tableUUID := queryIntegrationString(t, ctx, client, "SELECT toString(uuid) AS value FROM system.tables WHERE database = "+sqlStringLiteral(database)+" AND name = "+sqlStringLiteral(table)+" LIMIT 1\nFORMAT JSONEachRow")
	if tableUUID == "" {
		t.Fatalf("expected ClickHouse table UUID for %s.%s", database, table)
	}
	firstID := "00000000-0000-0000-0000-000000000001"
	secondID := "00000000-0000-0000-0000-000000000002"
	const sampleMS int64 = 1700000000000
	tagsTableRef := "`" + escapeIdentifier(database) + "`.`" + escapeIdentifier(".inner_id.tags."+tableUUID) + "`"
	dataTableRef := "`" + escapeIdentifier(database) + "`.`" + escapeIdentifier(".inner_id.data."+tableUUID) + "`"
	execIntegrationSQL(t, ctx, client, "INSERT INTO "+tagsTableRef+" (id, metric_name, job, instance, renamed_col, tags, min_time, max_time) VALUES (toUUID("+sqlStringLiteral(firstID)+"), 'up', 'api', 'i1', 'renamed-value', map('pod','p1'), fromUnixTimestamp64Milli(1700000000000), fromUnixTimestamp64Milli(1700000000000))")
	execIntegrationSQL(t, ctx, client, "INSERT INTO "+tagsTableRef+" (id, metric_name, instance, renamed_col, tags, min_time, max_time) VALUES (toUUID("+sqlStringLiteral(secondID)+"), 'up', 'i2', 'renamed-value-2', map('pod','p2'), fromUnixTimestamp64Milli(1700000000000), fromUnixTimestamp64Milli(1700000000000))")
	execIntegrationSQL(t, ctx, client, "INSERT INTO "+dataTableRef+" (id, timestamp, value) VALUES (toUUID("+sqlStringLiteral(firstID)+"), fromUnixTimestamp64Milli(1700000000000), 7), (toUUID("+sqlStringLiteral(secondID)+"), fromUnixTimestamp64Milli(1700000000000), 9)")

	jobMatcher, err := labels.NewMatcher(labels.MatchEqual, "job", "api")
	if err != nil {
		t.Fatalf("job matcher: %v", err)
	}
	selector := selectorSourceFromMatchers("up", []*labels.Matcher{jobMatcher}, 5*time.Minute, 0, SelectorKindInstantVector)
	sql, params, err := BuildInstantSelectorQuerySQL(QueryConfig{Database: database, Table: table, PromotedTagColumns: promoted}, selector, sampleMS-5*60*1000, sampleMS)
	if err != nil {
		t.Fatalf("BuildInstantSelectorQuerySQL: %v", err)
	}
	if !strings.Contains(sql, "src.`job` = {instant_matcher_1_value:String}") {
		t.Fatalf("expected promoted matcher to use direct column, got %q", sql)
	}
	samples, err := nativeClient.QueryInstantSamples(ctx, QueryRequest{SQL: sql, Params: params, Purpose: QueryPurposeInstant, Format: ResultFormatJSONEachRow})
	if err != nil {
		t.Fatalf("QueryInstantSamples: %v\nSQL: %s", err, sql)
	}
	if len(samples) != 1 {
		t.Fatalf("samples = %#v, want one sample", samples)
	}
	if samples[0].Value != 7 || samples[0].Metric["__name__"] != "up" || samples[0].Metric["job"] != "api" || samples[0].Metric["instance"] != "i1" || samples[0].Metric["pod"] != "p1" {
		t.Fatalf("sample = %#v, want value 7 with metric/job/instance/pod labels", samples[0])
	}
	if _, ok := samples[0].Metric["renamed_col"]; ok {
		t.Fatalf("non-identity promoted column should not be exposed as a Prometheus label: %#v", samples[0].Metric)
	}

	allSelector := selectorSourceFromMatchers("up", nil, 5*time.Minute, 0, SelectorKindInstantVector)
	allSQL, allParams, err := BuildInstantSelectorQuerySQL(QueryConfig{Database: database, Table: table, PromotedTagColumns: promoted}, allSelector, sampleMS-5*60*1000, sampleMS)
	if err != nil {
		t.Fatalf("BuildInstantSelectorQuerySQL all series: %v", err)
	}
	allSamples, err := nativeClient.QueryInstantSamples(ctx, QueryRequest{SQL: allSQL, Params: allParams, Purpose: QueryPurposeInstant, Format: ResultFormatJSONEachRow})
	if err != nil {
		t.Fatalf("QueryInstantSamples all series: %v\nSQL: %s", err, allSQL)
	}
	if len(allSamples) != 2 {
		t.Fatalf("all samples = %#v, want two samples", allSamples)
	}
	byInstance := map[string]map[string]string{}
	for _, sample := range allSamples {
		byInstance[sample.Metric["instance"]] = sample.Metric
	}
	if byInstance["i1"]["job"] != "api" || byInstance["i1"]["pod"] != "p1" {
		t.Fatalf("i1 labels = %#v, want job=api pod=p1", byInstance["i1"])
	}
	if _, ok := byInstance["i2"]["job"]; ok {
		t.Fatalf("missing promoted job label should stay absent, got labels %#v", byInstance["i2"])
	}
	if byInstance["i2"]["pod"] != "p2" {
		t.Fatalf("i2 labels = %#v, want pod=p2", byInstance["i2"])
	}
}

func requireHTTPIntegrationClient(t *testing.T) *Client {
	t.Helper()
	if os.Getenv("PROM_SHIM_RUN_INTEGRATION_TESTS") == "" && os.Getenv("PROM_SHIM_CLICKHOUSE_ENDPOINT") == "" {
		t.Skip("set PROM_SHIM_RUN_INTEGRATION_TESTS=1 or PROM_SHIM_CLICKHOUSE_ENDPOINT with ClickHouse HTTP reachable")
	}
	client, err := NewClient(Config{
		Endpoint:       envOr("PROM_SHIM_CLICKHOUSE_ENDPOINT", "http://127.0.0.1:8123/"),
		Database:       envOr("PROM_SHIM_CLICKHOUSE_DATABASE", "observability"),
		Username:       envOr("PROM_SHIM_CLICKHOUSE_USERNAME", "default"),
		Password:       envOr("PROM_SHIM_CLICKHOUSE_PASSWORD", "otel"),
		RequestTimeout: time.Duration(envInt("PROM_SHIM_REQUEST_TIMEOUT_SECONDS", 30)) * time.Second,
		SettingsProfile: SettingsProfileConfig{
			Name: SettingsProfileNone,
		},
	})
	if err != nil {
		t.Fatalf("NewClient HTTP integration: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	values, err := client.QueryStringRows(ctx, QueryRequest{SQL: "SELECT 'ok' AS value\nFORMAT JSONEachRow"})
	if err != nil {
		_ = client.Close()
		t.Skipf("ClickHouse HTTP integration fixture unavailable: %v", err)
	}
	if len(values) != 1 || values[0] != "ok" {
		_ = client.Close()
		t.Skipf("ClickHouse HTTP integration fixture returned %#v, want [ok]", values)
	}
	return client
}

func queryIntegrationString(t *testing.T, ctx context.Context, client *Client, sql string) string {
	t.Helper()
	values, err := client.QueryStringRows(ctx, QueryRequest{SQL: sql, Format: ResultFormatJSONEachRow})
	if err != nil {
		t.Fatalf("query string %q: %v", sql, err)
	}
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func execIntegrationSQL(t *testing.T, ctx context.Context, client *Client, sql string) {
	t.Helper()
	response, err := client.Execute(ctx, sql, nil)
	if err != nil {
		t.Fatalf("execute %q: %v", sql, err)
	}
	if response != nil && response.Body != nil {
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
	}
}
