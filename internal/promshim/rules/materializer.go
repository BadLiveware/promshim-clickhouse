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
	registry    *Registry
	client      *storage.Client
	db          string
	table       string
	ruleSet     map[string]bool
	stopCh      chan struct{}
	stopOnce    sync.Once
	wg          sync.WaitGroup
	running     sync.Map
	getRegistry func() *Registry
	reload      func() error
}

func NewMaterializer(registry *Registry, getRegistry func() *Registry, reload func() error, client *storage.Client, db, table string, ruleSet map[string]bool) *Materializer {
	return &Materializer{
		registry:    registry,
		getRegistry: getRegistry,
		reload:      reload,
		client:      client,
		db:          db,
		table:       table,
		ruleSet:     ruleSet,
		stopCh:      make(chan struct{}),
	}
}

func (m *Materializer) Start(ctx context.Context) {
	// If the initial registry is empty (rule-syncer hasn't written files yet),
	// retry after a short delay to catch the first sync.
	rules := m.getRegistry().Rules()
	if len(rules) == 0 {
		log.Printf("materializer: initial registry empty, polling until syncer+reload populates...")
		for i := 1; ; i++ {
			select {
			case <-ctx.Done():
				return
			case <-m.stopCh:
				return
			case <-time.After(10 * time.Second):
			}
			// Trigger a recording rule reload from the query service if
			// the registry is still empty (no query has triggered one yet).
			if m.reload != nil {
				_ = m.reload()
			}
			rules = m.getRegistry().Rules()
			if len(rules) > 0 {
				break
			}
			log.Printf("materializer: still empty, retry %d...", i)
		}
	}
	log.Printf("materializer: starting with %d recording rules", len(rules))
	for name, rule := range rules {
		if m.ruleSet != nil && !m.ruleSet[name] {
			continue
		}
		interval := rule.Interval
		if interval <= 0 {
			// Prometheus defaults to global.evaluation_interval = 1m when no
			// per-group interval is set.
			interval = 1 * time.Minute
			r := rule
			r.Interval = interval
			rule = r
		}
		rule := rule
		m.running.Store(name, struct{}{})
		m.wg.Add(1)
		go m.runRule(ctx, rule)
		// Stagger startup evals by 200ms per rule to avoid saturating the
		// ClickHouse connection pool when all rules fire their first eval at once.
		time.Sleep(200 * time.Millisecond)
	}
	m.wg.Add(1)
	go m.refreshLoop(ctx)
}

func (m *Materializer) refreshLoop(ctx context.Context) {
	defer m.wg.Done()
	ticker := time.NewTicker(2 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.stopCh:
			return
		case <-ticker.C:
		}
		rules := m.getRegistry().Rules()
		if len(rules) == 0 {
			continue
		}
		for name, rule := range rules {
			if m.ruleSet != nil && !m.ruleSet[name] {
				continue
			}
			if _, exists := m.running.Load(name); exists {
				continue
			}
			if rule.Interval <= 0 {
				r := rule
				r.Interval = 1 * time.Minute
				rule = r
			}
			rule := rule
			m.running.Store(name, struct{}{})
			m.wg.Add(1)
			go m.runRule(ctx, rule)
		}
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

	// Evaluate immediately on start.
	m.tryAcquireAndEval(ctx, rule, time.Now())

	for {
		select {
		case <-ctx.Done():
			return
		case <-m.stopCh:
			return
		case t := <-ticker.C:
			m.tryAcquireAndEval(ctx, rule, t)
		}
	}
}

func (m *Materializer) tryAcquireAndEval(ctx context.Context, rule RecordingRule, evalTime time.Time) {
	// Multi-replica note: all replicas evaluate the same rule at the same
	// timestamp. This is intentional — ClickHouse TimeSeries engine handles
	// duplicate (id, timestamp, value) tuples natively. The cost is 2× eval
	// per interval, which is still orders of magnitude cheaper than the
	// 24× full-history JOIN that virtual expansion triggered per dashobard.
	if err := m.evaluateRule(ctx, rule, evalTime); err != nil {
		log.Printf("materializer: eval of %q at %v failed: %v", rule.Name, evalTime, err)
	}
}

func (m *Materializer) evaluateRule(ctx context.Context, rule RecordingRule, evalTime time.Time) error {
	// 1. Expand nested recording rule references.
	expanded, err := ExpandExpr(rule.Expr, m.getRegistry())
	if err != nil {
		return fmt.Errorf("expand: %w", err)
	}

	// 2. Build logical plan, render native SQL (labels are applied to results,
	// not to the expression AST, to keep SQL clean).
	// Use a ±5s window around the eval time for the materializer query.
	// The TimeSeries table is populated at roughly 1s granularity by
	// the OTel collector; exact ms-level timestamp match would miss all.
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
			RequiredStartMS:  evalTS - 5000,
			RequiredEndMS:    evalTS + 5000,
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
		return nil
	}

	// 6. Write results back via INSERT.
	insertSQL, err := buildMaterializationInsert(m.table, queryResult.Data, rule, evalTime)
	if err != nil {
		return fmt.Errorf("build insert: %w", err)
	}
	if err := m.client.Exec(ctx, insertSQL+" SETTINGS allow_experimental_time_series_table=1, readonly=0", nil, nil); err != nil {
		log.Printf("materializer: write-back for %q failed: %v", rule.Name, err)
		return nil
	}
	log.Printf("materializer: wrote %d rows for %q", len(queryResult.Data), rule.Name)
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
