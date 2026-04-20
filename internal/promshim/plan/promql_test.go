package plan

import "testing"

func TestAnalyzeExpressionSupportsSumAggregation(t *testing.T) {
	expr, err := ParseExpression("sum by (job) (up)")
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if !result.Supported {
		t.Fatalf("expected supported aggregation, got %#v", result)
	}
	if result.Difficulty != DifficultyMedium {
		t.Fatalf("expected medium difficulty, got %s", result.Difficulty)
	}
}

func TestAnalyzeExpressionSupportsAvgAggregation(t *testing.T) {
	expr, err := ParseExpression("avg(up)")
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if !result.Supported {
		t.Fatalf("expected supported aggregation, got %#v", result)
	}
	if result.Difficulty != DifficultyMedium {
		t.Fatalf("expected medium difficulty, got %s", result.Difficulty)
	}
}

func TestAnalyzeExpressionSupportsScalarBinary(t *testing.T) {
	expr, err := ParseExpression("1 + 2")
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if !result.Supported {
		t.Fatalf("expected supported binary expression, got %#v", result)
	}
}

func TestAnalyzeExpressionSupportsVectorScalarBinary(t *testing.T) {
	expr, err := ParseExpression("up * 100")
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if !result.Supported {
		t.Fatalf("expected supported vector-scalar expression, got %#v", result)
	}
}

func TestAnalyzeExpressionSupportsUnaryVector(t *testing.T) {
	expr, err := ParseExpression("-up")
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if !result.Supported {
		t.Fatalf("expected supported unary expression, got %#v", result)
	}
}

func TestAnalyzeExpressionSupportsLabelReplace(t *testing.T) {
	expr, err := ParseExpression(`label_replace(up, "job_copy", "$1", "job", "(.*)")`)
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if !result.Supported {
		t.Fatalf("expected supported label_replace, got %#v", result)
	}
}

func TestAnalyzeExpressionSupportsLabelJoin(t *testing.T) {
	expr, err := ParseExpression(`label_join(up, "joined", "/", "job", "namespace")`)
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if !result.Supported {
		t.Fatalf("expected supported label_join, got %#v", result)
	}
}

func TestAnalyzeExpressionRejectsUnsupportedAggregator(t *testing.T) {
	expr, err := ParseExpression("topk(3, up)")
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if result.Supported {
		t.Fatalf("expected unsupported aggregation, got %#v", result)
	}
	if result.Reason == "" {
		t.Fatal("expected unsupported reason")
	}
}

func TestAnalyzeExpressionRejectsVectorVectorBinary(t *testing.T) {
	expr, err := ParseExpression("up + up")
	if err != nil {
		t.Fatal(err)
	}
	result := AnalyzeExpression(expr)
	if result.Supported {
		t.Fatalf("expected unsupported vector-vector binary, got %#v", result)
	}
}
