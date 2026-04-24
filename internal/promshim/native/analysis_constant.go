package native

import (
	"math"

	"github.com/prometheus/prometheus/promql/parser"
)

// syntheticLiteralValue reports whether info corresponds to a folded
// scalar literal (synthetic series with OutputKindScalar and
// SyntheticSeries.Func == "literal"), returning the literal value. It
// drives the Analyze walk's constant-folding branches for unary and
// binary plans.
func syntheticLiteralValue(info *LoweringInfo) (float64, bool) {
	if info == nil || info.SyntheticSeries == nil {
		return 0, false
	}
	if info.SubtreeShape != SubtreeShapeSyntheticSeries {
		return 0, false
	}
	if info.OutputKind != OutputKindScalar {
		return 0, false
	}
	if info.SyntheticSeries.Func != "literal" {
		return 0, false
	}
	return info.SyntheticSeries.Value, true
}

func foldUnaryScalarLiteral(op parser.ItemType, value float64) (float64, bool) {
	switch op {
	case parser.ADD:
		return value, true
	case parser.SUB:
		return -value, true
	default:
		return 0, false
	}
}

func foldBinaryScalarLiteral(op parser.ItemType, returnBool bool, lhs, rhs float64) (float64, bool) {
	if returnBool {
		switch op {
		case parser.EQLC:
			if lhs == rhs {
				return 1, true
			}
			return 0, true
		case parser.NEQ:
			if lhs != rhs {
				return 1, true
			}
			return 0, true
		case parser.GTR:
			if lhs > rhs {
				return 1, true
			}
			return 0, true
		case parser.LSS:
			if lhs < rhs {
				return 1, true
			}
			return 0, true
		case parser.GTE:
			if lhs >= rhs {
				return 1, true
			}
			return 0, true
		case parser.LTE:
			if lhs <= rhs {
				return 1, true
			}
			return 0, true
		default:
			return 0, false
		}
	}
	switch op {
	case parser.ADD:
		return lhs + rhs, true
	case parser.SUB:
		return lhs - rhs, true
	case parser.MUL:
		return lhs * rhs, true
	case parser.DIV:
		return lhs / rhs, true
	case parser.MOD:
		return math.Mod(lhs, rhs), true
	case parser.POW:
		return math.Pow(lhs, rhs), true
	default:
		return 0, false
	}
}
