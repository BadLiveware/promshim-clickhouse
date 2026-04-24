package local

import (
	"strings"

	logicalpkg "github.com/BadLiveware/promshim-clickhouse/internal/promshim/logical"
	"github.com/prometheus/prometheus/promql/parser"
)

func ensureDelegatedExprSupportedForContext(expr parser.Expr, ctx PlanContext, stage string) error {
	result := analyzeDelegatedExprSupportForContext(expr, ctx)
	if result.Supported {
		return nil
	}
	return NewPlanBuildError(expr, result, stage)
}

func analyzeDelegatedExprSupportForContext(expr parser.Expr, ctx PlanContext) logicalpkg.SupportResult {
	if ctx.Mode != EvalModeRange || expr == nil {
		return logicalpkg.SupportResult{Supported: true}
	}

	expr = unwrapTransparentExpr(expr)
	call, ok := expr.(*parser.Call)
	if !ok {
		return logicalpkg.SupportResult{Supported: true}
	}

	switch strings.ToLower(call.Func.Name) {
	case "increase":
		return logicalpkg.SupportResult{Supported: false, Difficulty: logicalpkg.DifficultyMedium, Reason: `function "increase" is not implemented yet for range queries`}
	default:
		return logicalpkg.SupportResult{Supported: true}
	}
}
