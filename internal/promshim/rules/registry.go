package rules

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	commonmodel "github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/rulefmt"
	"github.com/prometheus/prometheus/promql/parser"
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

type Registry struct {
	byName    map[string]RecordingRule
	conflicts map[string][]RecordingRule
	errors    []error
}

func EmptyRegistry() *Registry {
	return &Registry{byName: map[string]RecordingRule{}, conflicts: map[string][]RecordingRule{}}
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
	return reg, nil
}

func (r *Registry) Lookup(name string) (RecordingRule, bool) {
	if r == nil {
		return RecordingRule{}, false
	}
	rule, ok := r.byName[name]
	return rule, ok
}

func (r *Registry) Conflict(name string) ([]RecordingRule, bool) {
	if r == nil {
		return nil, false
	}
	rules, ok := r.conflicts[name]
	return append([]RecordingRule(nil), rules...), ok
}

func (r *Registry) Len() int {
	if r == nil {
		return 0
	}
	return len(r.byName)
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
	for _, group := range groups.Groups {
		for _, rawRule := range group.Rules {
			if rawRule.Record == "" {
				continue
			}
			expr, err := parser.NewParser(parser.Options{EnableBinopFillModifiers: true, EnableExperimentalFunctions: true}).ParseExpr(rawRule.Expr)
			if err != nil {
				return fmt.Errorf("parsing recording rule %q from %s: %w", rawRule.Record, file, err)
			}
			rule := RecordingRule{
				Name:        rawRule.Record,
				Expr:        expr,
				ExprString:  rawRule.Expr,
				Labels:      cloneMap(rawRule.Labels),
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
		r.conflicts[rule.Name] = append(conflict, rule)
		return
	}
	r.byName[rule.Name] = rule
}

func sameRule(a, b RecordingRule) bool {
	return a.ExprString == b.ExprString && mapsEqual(a.Labels, b.Labels) && mapsEqual(a.GroupLabels, b.GroupLabels)
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
