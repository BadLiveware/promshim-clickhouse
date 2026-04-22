package promshim

import (
	"strings"

	planpkg "ch-observability/internal/promshim/plan"
	"github.com/prometheus/prometheus/promql/parser"
)

func ensureDelegatedExprSupportedForContext(expr parser.Expr, ctx planContext, stage string) error {
	result := analyzeDelegatedExprSupportForContext(expr, ctx)
	if result.Supported {
		return nil
	}
	return newPlanBuildError(expr, result, stage)
}

func analyzeDelegatedExprSupportForContext(expr parser.Expr, ctx planContext) planpkg.SupportResult {
	if ctx.Mode != evalModeRange || expr == nil {
		return planpkg.SupportResult{Supported: true}
	}

	expr = unwrapTransparentExpr(expr)
	call, ok := expr.(*parser.Call)
	if !ok {
		return planpkg.SupportResult{Supported: true}
	}

	switch strings.ToLower(call.Func.Name) {
	case "increase":
		return planpkg.SupportResult{Supported: false, Difficulty: planpkg.DifficultyMedium, Reason: `function "increase" is not implemented yet for range queries`}
	default:
		return planpkg.SupportResult{Supported: true}
	}
}
