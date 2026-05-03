package rules

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	logicalpkg "github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
	nativeplan "github.com/BadLiveware/promshim-clickhouse/internal/promshim/native"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/native/renderer"
	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/storage"
	"github.com/prometheus/prometheus/promql/parser"
)

type Materializer struct {
	registry *Registry
	client   *storage.Client
	db       string
	table    string
	ruleSet  map[string]bool // set of rule names to materialize, or nil for all
	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

func NewMaterializer(registry *Registry, client *storage.Client, db, table string, ruleSet map[string]bool) *Materializer {
	return &Materializer{
		registry: registry,
		client:   client,
		db:       db,
		table:    table,
		ruleSet:  ruleSet,
		stopCh:   make(chan struct{}),
	}
}

func (m *Materializer) Start(ctx context.Context) {
	rules := m.registry.Rules()
	for name, rule := range rules {
		if m.ruleSet != nil && !m.ruleSet[name] {
			continue
		}
		if rule.Interval <= 0 {
			continue
		}
		rule := rule
		m.wg.Add(1)
		go m.runRule(ctx, rule)
	}
}

func (m *Materializer) Stop() {
	m.stopOnce.Do(func() { close(m.stopCh) })
	m.wg.Wait()
}

func (m *Materializer) runRule(ctx context.Context, rule RecordingRule) {
	defer m.wg.Done()
	ticker := time.NewTicker(rule.Interval)
	defer ticker.Stop()

	var lastEval time.Time

	// Evaluate immediately on start, then at each tick.
	now := time.Now()
	if err := m.evaluateRule(ctx, rule, now); err == nil {
		lastEval = now
	} else {
		log.Printf("materializer: initial eval of %q failed: %v", rule.Name, err)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-m.stopCh:
			return
		case t := <-ticker.C:
			if !t.After(lastEval) {
				continue
			}
			if err := m.evaluateRule(ctx, rule, t); err == nil {
				lastEval = t
			} else {
				log.Printf("materializer: eval of %q at %v failed: %v", rule.Name, t, err)
			}
		}
	}
}

func (m *Materializer) evaluateRule(ctx context.Context, rule RecordingRule, evalTime time.Time) error {
	// 1. Expand nested recording rule references.
	expanded, err := ExpandExpr(rule.Expr, m.registry)
	if err != nil {
		return fmt.Errorf("expand: %w", err)
	}

	// 2. Build logical plan, render native SQL (labels are applied to results,
	// not to the expression AST, to keep SQL clean).
	evalTS := evalTime.UnixMilli()
	logical, err := logicalpkg.ToLogical(expanded.Expr)
	if err != nil {
		return fmt.Errorf("logical: %w", err)
	}
	analysis := logicalpkg.Analyze(logical)
	nativeAnalysis := nativeplan.Analyze(logical)

	cfg := storage.QueryConfig{Database: m.db, Table: m.table, EnableNativeGridFunctions: false, EnableCumulativeAvgOverTime: false}
	rq, err := renderer.Lower(renderer.LoweringCtx{
		Config:         cfg,
		Analysis:       analysis,
		NativeAnalysis: nativeAnalysis,
		Params: renderer.RenderParams{
			Mode:             nativeplan.RenderModeInstant,
			EvaluationTimeMS: evalTS,
			RequiredStartMS:  evalTS,
			RequiredEndMS:    evalTS + 1,
			ResolveSourcePromQL: func(expr parser.Expr) (string, error) {
				return "", fmt.Errorf("rule materialization does not support delegated PromQL")
			},
		},
	}, logical)
	if err != nil {
		return fmt.Errorf("render: %w", err)
	}

	// 4. Execute the SQL against ClickHouse.
	resp, err := m.client.ExecuteWithSettings(ctx, rq.SQL, rq.QueryParams, rq.QuerySettings)
	if err != nil {
		return fmt.Errorf("execute: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// 5. Parse results.
	var queryResult struct {
		Data []struct {
			Tags      [][2]string `json:"tags"`
			Timestamp float64     `json:"timestamp"`
			Value     float64     `json:"value"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&queryResult); err != nil {
		return fmt.Errorf("decode result: %w", err)
	}
	if len(queryResult.Data) == 0 {
		return nil // no data, nothing to write
	}

	// 6. Write results back via INSERT.
	insertSQL, err := buildMaterializationInsert(m.table, queryResult.Data, rule, evalTime)
	if err != nil {
		return fmt.Errorf("build insert: %w", err)
	}

	resp2, err := m.client.ExecuteWithSettings(ctx, insertSQL+" SETTINGS allow_experimental_time_series_table=1", nil, nil)
	if err != nil {
		// Log but don't fail — the data is already evaluated.
		log.Printf("materializer: write-back for %q failed: %v", rule.Name, err)
		return nil
	}
	_ = resp2.Body.Close()
	return nil
}

func buildMaterializationInsert(table string, rows []struct {
	Tags      [][2]string `json:"tags"`
	Timestamp float64     `json:"timestamp"`
	Value     float64     `json:"value"`
}, rule RecordingRule, evalTime time.Time) (string, error) {
	if len(rows) == 0 {
		return "", nil
	}
	parts := make([]string, 0, len(rows))
	for _, row := range rows {
		tags := buildTagsArray(row.Tags, rule)
		parts = append(parts, fmt.Sprintf("(toUnixTimestamp64Milli(toDateTime64(%f, 3)), %s, %s)", row.Timestamp, sqlFloat(row.Value), tags))
	}
	return fmt.Sprintf("INSERT INTO %s (timestamp, value, tags) VALUES %s", table, strings.Join(parts, ", ")), nil
}

func buildTagsArray(baseTags [][2]string, rule RecordingRule) string {
	tagMap := map[string]string{}
	for _, tag := range baseTags {
		tagMap[tag[0]] = tag[1]
	}
	// Override __name__ with the record name.
	tagMap["__name__"] = rule.Name
	// Overlay GroupLabels (rule Labels take precedence).
	for k, v := range rule.GroupLabels {
		if _, ok := tagMap[k]; !ok {
			tagMap[k] = v
		}
	}
	for k, v := range rule.Labels {
		tagMap[k] = v
	}
	// Serialize as [tuple(k, v), ...]
	tuples := make([]string, 0, len(tagMap))
	for k, v := range tagMap {
		tuples = append(tuples, fmt.Sprintf("tuple('%s', '%s')", escapeString(k), escapeString(v)))
	}
	return "[" + strings.Join(tuples, ", ") + "]"
}

func sqlFloat(v float64) string {
	return fmt.Sprintf("%g", v)
}

func escapeString(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "'", "\\'")
	return s
}
