package rules

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	commonmodel "github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/rulefmt"
	"github.com/prometheus/prometheus/promql/parser"
	"sigs.k8s.io/yaml"
)

type Mode string

const (
	ModeOff     Mode = "off"
	ModeVirtual Mode = "virtual"
)

type RecordingRule struct {
	Name        string
	Expr        parser.Expr
	ExprString  string
	Labels      map[string]string
	GroupName   string
	GroupLabels map[string]string
	Interval    time.Duration
	QueryOffset time.Duration
	Source      string
}

type cachedExpansion struct {
	Expr       parser.Expr
	Expansions []Expansion
}

type Registry struct {
	byName            map[string]RecordingRule
	conflicts         map[string][]RecordingRule
	cached            map[string]cachedExpansion
	cachedMu          sync.RWMutex
	errors            []error
	expansionMetrics  atomic.Pointer[ExpansionMetrics]
	materializeAll    bool
	materializedRules map[string]bool
}

func EmptyRegistry() *Registry {
	return &Registry{byName: map[string]RecordingRule{}, conflicts: map[string][]RecordingRule{}, cached: map[string]cachedExpansion{}}
}

func LoadFiles(patterns []string) (*Registry, error) {
	reg := EmptyRegistry()
	var files []string
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid recording rule file glob %q: %w", pattern, err)
		}
		if len(matches) == 0 {
			reg.errors = append(reg.errors, fmt.Errorf("recording rule file glob %q matched no files", pattern))
			continue
		}
		files = append(files, matches...)
	}
	sort.Strings(files)
	for _, file := range files {
		if err := reg.loadFile(file); err != nil {
			reg.errors = append(reg.errors, err)
		}
	}
	reg.validateExpansions()
	return reg, nil
}

func (r *Registry) Lookup(name string) (RecordingRule, bool) {
	if r == nil {
		return RecordingRule{}, false
	}
	rule, ok := r.byName[name]
	return rule, ok
}

func (r *Registry) Candidates(name string) []RecordingRule {
	if r == nil {
		return nil
	}
	if rule, ok := r.byName[name]; ok {
		return []RecordingRule{rule}
	}
	conflict, ok := r.conflicts[name]
	if !ok {
		return nil
	}
	return append([]RecordingRule(nil), conflict...)
}

func (r *Registry) Conflict(name string) ([]RecordingRule, bool) {
	if r == nil {
		return nil, false
	}
	rules, ok := r.conflicts[name]
	return append([]RecordingRule(nil), rules...), ok
}

func (r *Registry) ExpansionMetrics() *ExpansionMetrics {
	if r == nil {
		return nil
	}
	return r.expansionMetrics.Load()
}

func (r *Registry) SetExpansionMetrics(m *ExpansionMetrics) {
	if r != nil && m != nil {
		r.expansionMetrics.Store(m)
	}
}

func (r *Registry) SetMaterializedRules(ruleSet map[string]bool, all bool) {
	if r == nil {
		return
	}
	r.materializeAll = all
	r.materializedRules = ruleSet
}

func (r *Registry) IsMaterialized(name string) bool {
	if r == nil {
		return false
	}
	if r.materializeAll {
		return true
	}
	if r.materializedRules == nil {
		return false
	}
	return r.materializedRules[name]
}

func (r *Registry) Rules() map[string]RecordingRule {
	if r == nil {
		return nil
	}
	// Return a snapshot: byName rules plus all conflict definitions.
	result := map[string]RecordingRule{}
	for name, rule := range r.byName {
		result[name] = rule
	}
	for _, conflicting := range r.conflicts {
		for _, rule := range conflicting {
			result[rule.Name] = rule
		}
	}
	return result
}

func (r *Registry) Len() int {
	if r == nil {
		return 0
	}
	return len(r.byName)
}

func (r *Registry) Empty() bool {
	return r == nil || (len(r.byName) == 0 && len(r.conflicts) == 0)
}

func (r *Registry) AllRules() []RecordingRule {
	if r == nil {
		return nil
	}
	all := make([]RecordingRule, 0, len(r.byName)+len(r.conflicts))
	for _, rule := range r.byName {
		all = append(all, rule)
	}
	for _, variants := range r.conflicts {
		all = append(all, variants...)
	}
	return all
}

func (r *Registry) CachedExpansion(key string) (parser.Expr, []Expansion, bool) {
	if r == nil {
		return nil, nil, false
	}
	r.cachedMu.RLock()
	defer r.cachedMu.RUnlock()
	cached, ok := r.cached[key]
	if !ok {
		return nil, nil, false
	}
	return cached.Expr, append([]Expansion(nil), cached.Expansions...), true
}

func (r *Registry) storeCachedExpansion(key string, expr parser.Expr, expansions []Expansion) {
	if r == nil {
		return
	}
	r.cachedMu.Lock()
	defer r.cachedMu.Unlock()
	r.cached[key] = cachedExpansion{Expr: expr, Expansions: append([]Expansion(nil), expansions...)}
}

func (r *Registry) Errors() []error {
	if r == nil {
		return nil
	}
	return append([]error(nil), r.errors...)
}

func (r *Registry) loadFile(file string) error {
	groups, errs := rulefmt.ParseFile(file, false, commonmodel.UTF8Validation, parser.NewParser(parser.Options{EnableBinopFillModifiers: true, EnableExperimentalFunctions: true}))
	if len(errs) > 0 {
		return fmt.Errorf("loading recording rules from %s: %v", file, errs)
	}
	if groups == nil {
		return nil
	}
	// Fallback: scan for label-like keys at the rule level that are outside the
	// labels: block. Some CRD definitions place labels inline (e.g. workload_type:
	// deployment) rather than inside labels:. Capture them so selectorStaticMismatch
	// can prune non-matching definitions at query time.
	fallbackLabels := loadFallbackRuleLabels(file)
	for _, group := range groups.Groups {
		for _, rawRule := range group.Rules {
			if rawRule.Record == "" {
				continue
			}
			expr, err := parser.NewParser(parser.Options{EnableBinopFillModifiers: true, EnableExperimentalFunctions: true}).ParseExpr(rawRule.Expr)
			if err != nil {
				return fmt.Errorf("parsing recording rule %q from %s: %w", rawRule.Record, file, err)
			}
			labels := cloneMap(rawRule.Labels)
			if fallback := fallbackLabels[rawRule.Record]; fallback != nil {
				for k, v := range fallback {
					if _, exists := labels[k]; !exists {
						labels[k] = v
					}
				}
			}
			rule := RecordingRule{
				Name:        rawRule.Record,
				Expr:        expr,
				ExprString:  rawRule.Expr,
				Labels:      labels,
				GroupName:   group.Name,
				GroupLabels: cloneMap(group.Labels),
				Interval:    time.Duration(group.Interval),
				Source:      file,
			}
			if group.QueryOffset != nil {
				rule.QueryOffset = time.Duration(*group.QueryOffset)
			}
			r.add(rule)
		}
	}
	return nil
}

func (r *Registry) validateExpansions() {
	if len(r.errors) > 0 || len(r.byName) == 0 {
		return
	}
	names := make([]string, 0, len(r.byName))
	for name := range r.byName {
		names = append(names, name)
	}
	sort.Strings(names)
	state := expandState{registry: r, visiting: map[string]struct{}{}}
	for _, name := range names {
		rule := r.byName[name]
		if _, _, ok := r.CachedExpansion(recordingRuleCacheKey(rule)); ok {
			continue
		}
		if _, _, err := state.expandRuleExpr(rule, true); err != nil {
			r.errors = append(r.errors, fmt.Errorf("validating recording rule %q expansion: %w", rule.Name, err))
		}
	}
}

func (r *Registry) add(rule RecordingRule) {
	if existing, ok := r.byName[rule.Name]; ok {
		if sameRule(existing, rule) {
			return
		}
		delete(r.byName, rule.Name)
		r.conflicts[rule.Name] = append([]RecordingRule{existing}, rule)
		return
	}
	if conflict, ok := r.conflicts[rule.Name]; ok {
		for _, existing := range conflict {
			if sameRule(existing, rule) {
				return
			}
		}
		r.conflicts[rule.Name] = append(conflict, rule)
		return
	}
	r.byName[rule.Name] = rule
}

func sameRule(a, b RecordingRule) bool {
	return a.ExprString == b.ExprString && a.Interval == b.Interval && a.QueryOffset == b.QueryOffset && mapsEqual(a.Labels, b.Labels) && mapsEqual(a.GroupLabels, b.GroupLabels)
}

var knownRuleFields = map[string]bool{
	"record":          true,
	"alert":           true,
	"expr":            true,
	"for":             true,
	"keep_firing_for": true,
	"labels":          true,
	"annotations":     true,
}

// loadFallbackRuleLabels parses a YAML rules file and extracts label-like inline
// keys from rule definitions that are NOT inside a labels: block. Some CRD
// generators place labels such as workload_type: deployment at the rule level
// rather than inside labels:, causing rulefmt.ParseFile to drop them. This
// fallback captures those keys so selectorStaticMismatch can prune non-matching
// definitions at query time.
func loadFallbackRuleLabels(file string) map[string]map[string]string {
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil
	}
	var doc map[string]interface{}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil
	}
	result := map[string]map[string]string{}
	groups, _ := doc["groups"].([]interface{})
	for _, g := range groups {
		group, _ := g.(map[string]interface{})
		rules, _ := group["rules"].([]interface{})
		for _, r := range rules {
			rule, _ := r.(map[string]interface{})
			record, _ := rule["record"].(string)
			if record == "" {
				continue
			}
			extra := map[string]string{}
			for key, val := range rule {
				if knownRuleFields[key] {
					continue
				}
				if strVal, ok := val.(string); ok && key != "" {
					extra[key] = strVal
				}
			}
			if len(extra) > 0 {
				result[record] = extra
			}
		}
	}
	return result
}

func recordingRuleCacheKey(rule RecordingRule) string {
	return strings.Join([]string{
		"name=" + rule.Name,
		"expr=" + strconv.Quote(rule.ExprString),
		"interval=" + rule.Interval.String(),
		"query_offset=" + rule.QueryOffset.String(),
		"labels=" + encodeSortedLabels(rule.Labels),
		"group_labels=" + encodeSortedLabels(rule.GroupLabels),
	}, "|")
}

func encodeSortedLabels(in map[string]string) string {
	if len(in) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+strconv.Quote(in[key]))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		if b[k] != av {
			return false
		}
	}
	return true
}

func cloneMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
