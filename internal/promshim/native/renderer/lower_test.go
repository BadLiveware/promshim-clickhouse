package renderer

import (
	"testing"

	logicalpkg "github.com/BadLiveware/promshim-ch/internal/promshim/logical"
	"github.com/BadLiveware/promshim-ch/internal/promshim/native"
	"github.com/BadLiveware/promshim-ch/internal/promshim/storage"
)

func testRenderConfig() storage.QueryConfig {
	return storage.QueryConfig{Database: "observability", Table: "prometheus"}
}

func testRenderParams() RenderParams {
	return RenderParams{Mode: native.RenderModeInstant, EvaluationTimeMS: 1_700_000_000_000}
}

func buildLowerInputs(t *testing.T, query string) (logicalpkg.Node, *logicalpkg.Analysis, *native.Analysis) {
	t.Helper()
	expr := mustParseExpr(t, query)
	root, err := logicalpkg.ToLogical(expr)
	if err != nil {
		t.Fatalf("ToLogical: %v", err)
	}
	return root, logicalpkg.Analyze(root), native.Analyze(root)
}

func TestLowerNilNodeErrors(t *testing.T) {
	_, err := Lower(LoweringCtx{Analysis: &logicalpkg.Analysis{Info: map[logicalpkg.Node]*logicalpkg.NodeInfo{}}}, nil)
	if err == nil {
		t.Fatalf("expected error for nil node")
	}
	if IsUnsupportedByLower(err) {
		t.Fatalf("expected non-sentinel error for nil node, got sentinel")
	}
}

func TestLowerNilAnalysisErrors(t *testing.T) {
	root, _, nativeAnalysis := buildLowerInputs(t, `up`)
	_, err := Lower(LoweringCtx{NativeAnalysis: nativeAnalysis}, root)
	if err == nil {
		t.Fatalf("expected error for nil Analysis")
	}
	if IsUnsupportedByLower(err) {
		t.Fatalf("expected non-sentinel error for nil Analysis, got sentinel")
	}
}
