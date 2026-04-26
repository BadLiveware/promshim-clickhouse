package opt

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
)

const DisableOptimizedIREnv = "PROM_SHIM_DISABLE_OPTIMIZED_IR"

type PassMetadata struct {
	Name                  string   `json:"name"`
	Families              []string `json:"families,omitempty"`
	Preconditions         []string `json:"preconditions,omitempty"`
	PreservedInvariants   []string `json:"preservedInvariants,omitempty"`
	MetadataProduced      []string `json:"metadataProduced,omitempty"`
	ExpectedSignals       []string `json:"expectedSignals,omitempty"`
	RollbackConfiguration string   `json:"rollbackConfiguration,omitempty"`
}

type PassResult struct {
	Name                string       `json:"name"`
	Iteration           int          `json:"iteration"`
	Applied             bool         `json:"applied"`
	SkipReasons         []string     `json:"skipReasons,omitempty"`
	InspectedNodes      int          `json:"inspectedNodes,omitempty"`
	OptimizerTimeMicros int64        `json:"optimizerTimeMicros,omitempty"`
	BeforeFingerprint   string       `json:"beforeFingerprint,omitempty"`
	AfterFingerprint    string       `json:"afterFingerprint,omitempty"`
	Metadata            PassMetadata `json:"metadata"`
}

type Trace struct {
	Disabled bool         `json:"disabled,omitempty"`
	EnvGate  string       `json:"envGate,omitempty"`
	Passes   []PassResult `json:"passes,omitempty"`
}

// Pass is a pure rewrite over the logical IR. Apply returns the
// (possibly-rewritten) root, whether it made any change, and any error.
// Implementations MUST NOT mutate the input node tree in place.
type Pass interface {
	Name() string
	Apply(root logical.Node, analysis *logical.Analysis) (logical.Node, bool, error)
}

type DescribedPass interface {
	Metadata() PassMetadata
}

const maxOptimizeIterations = 8

// DefaultPassesForEnv returns the canonical ordered pass list unless the
// rollback/differential-testing gate disables optimized IR.
func DefaultPassesForEnv() []Pass {
	if OptimizedIRDisabled() {
		return nil
	}
	return DefaultPasses
}

func OptimizedIRDisabled() bool {
	raw := os.Getenv(DisableOptimizedIREnv)
	if raw == "" {
		return false
	}
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		return false
	}
	return parsed
}

// Optimize runs passes in fixed order, re-analyzing between any rewriting
// pass, until a full iteration produces no change. It errors if the
// fixpoint cap is exceeded.
func Optimize(root logical.Node, passes []Pass) (logical.Node, *logical.Analysis, error) {
	root, analysis, _, err := OptimizeWithTrace(root, passes)
	return root, analysis, err
}

func OptimizeWithTrace(root logical.Node, passes []Pass) (logical.Node, *logical.Analysis, *Trace, error) {
	if root == nil {
		return nil, nil, nil, fmt.Errorf("opt: Optimize requires a non-nil root")
	}
	trace := &Trace{}
	if OptimizedIRDisabled() {
		trace.Disabled = true
		trace.EnvGate = DisableOptimizedIREnv
		return root, logical.Analyze(root), trace, nil
	}
	analysis := logical.Analyze(root)
	if len(passes) == 0 {
		return root, analysis, trace, nil
	}
	for iter := 0; iter < maxOptimizeIterations; iter++ {
		changed := false
		for _, p := range passes {
			metadata := passMetadata(p)
			beforeFingerprint := logicalFingerprint(root)
			beforeNodes := inspectedNodeCount(analysis)
			started := time.Now()
			next, didChange, err := p.Apply(root, analysis)
			elapsedMicros := time.Since(started).Microseconds()
			if elapsedMicros == 0 {
				elapsedMicros = 1
			}
			if err != nil {
				return nil, nil, trace, fmt.Errorf("opt: pass %q: %w", p.Name(), err)
			}
			result := PassResult{
				Name:                p.Name(),
				Iteration:           iter + 1,
				Applied:             didChange,
				InspectedNodes:      beforeNodes,
				OptimizerTimeMicros: elapsedMicros,
				BeforeFingerprint:   beforeFingerprint,
				AfterFingerprint:    beforeFingerprint,
				Metadata:            metadata,
			}
			if !didChange {
				result.SkipReasons = []string{"no_matching_subtree"}
			}
			if didChange {
				result.AfterFingerprint = logicalFingerprint(next)
			}
			trace.Passes = append(trace.Passes, result)
			if didChange {
				root = next
				analysis = logical.Analyze(root)
				changed = true
			}
		}
		if !changed {
			return root, analysis, trace, nil
		}
	}
	return nil, nil, trace, fmt.Errorf("opt: did not reach fixpoint in %d iterations", maxOptimizeIterations)
}

func passMetadata(pass Pass) PassMetadata {
	if described, ok := pass.(DescribedPass); ok {
		metadata := described.Metadata()
		if metadata.Name == "" {
			metadata.Name = pass.Name()
		}
		return metadata
	}
	return PassMetadata{Name: pass.Name()}
}

// DefaultPasses is the canonical ordered pass list applied to every query.
var DefaultPasses = []Pass{
	constantFoldUnaryNegation{},
	cancelRepeatedAverage{},
}

func inspectedNodeCount(analysis *logical.Analysis) int {
	if analysis == nil {
		return 0
	}
	return len(analysis.Info)
}

func logicalFingerprint(node logical.Node) string {
	if node == nil {
		return ""
	}
	described, ok := node.(interface{ ExprString() string })
	if !ok {
		return ""
	}
	expr := described.ExprString()
	if expr == "" {
		return ""
	}
	sum := sha1.Sum([]byte(expr))
	return hex.EncodeToString(sum[:])[:12]
}
