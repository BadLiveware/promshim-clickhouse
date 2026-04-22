package native

import (
	"math"

	"github.com/prometheus/prometheus/promql/parser"
)

func syntheticLiteralValue(fragment *NativeFragment) (float64, bool) {
	if fragment == nil || fragment.Kind != FragmentKindSyntheticSeries || fragment.Synthetic == nil || fragment.Synthetic.Func != "literal" || fragment.Synthetic.Value == nil {
		return 0, false
	}
	return *fragment.Synthetic.Value, true
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
