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

func renderBothPaths(t *testing.T, query string) (lowerSQL, fragmentSQL string) {
	t.Helper()
	root, analysis, nativeAnalysis := buildLowerInputs(t, query)
	lowerCtx := LoweringCtx{
		Config:         testRenderConfig(),
		Analysis:       analysis,
		NativeAnalysis: nativeAnalysis,
		Params:         testRenderParams(),
	}
	lowerRQ, err := Lower(lowerCtx, root)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	fragment, err := native.BuildFragment(root, nativeAnalysis)
	if err != nil {
		t.Fatalf("BuildFragment: %v", err)
	}
	fragmentRQ, err := RenderFragment(testRenderConfig(), fragment, testRenderParams())
	if err != nil {
		t.Fatalf("RenderFragment: %v", err)
	}
	return lowerRQ.SQL, fragmentRQ.SQL
}

func TestLowerLeafMatchesFragment(t *testing.T) {
	got, want := renderBothPaths(t, `up{job="api"}`)
	if got != want {
		t.Errorf("SQL differs:\nLower:    %s\nFragment: %s", got, want)
	}
}

func TestLowerScalarLiteralMatchesFragment(t *testing.T) {
	got, want := renderBothPaths(t, `42`)
	if got != want {
		t.Errorf("SQL differs:\nLower:    %s\nFragment: %s", got, want)
	}
}

func TestLowerScalarBinaryMatchesFragment(t *testing.T) {
	got, want := renderBothPaths(t, `up * 2`)
	if got != want {
		t.Errorf("SQL differs:\nLower:    %s\nFragment: %s", got, want)
	}
}


func TestLowerVectorVectorBinaryFallsBack(t *testing.T) {
	// Vector-vector binaries aren't in Phase 1 Lower scope — must return
	// the sentinel so the caller falls back to Fragment rendering.
	root, analysis, nativeAnalysis := buildLowerInputs(t, `up and up`)
	_, err := Lower(LoweringCtx{
		Config:         testRenderConfig(),
		Analysis:       analysis,
		NativeAnalysis: nativeAnalysis,
		Params:         testRenderParams(),
	}, root)
	if !IsUnsupportedByLower(err) {
		t.Errorf("expected errUnsupportedLowerNode, got %v", err)
	}
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
