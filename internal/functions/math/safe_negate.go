package math

import (
	"fmt"
	"math"
	"math/big"

	"github.com/goccy/googlesqlite/internal/value"
)

// negate keeps the argument's type. -MinInt64 overflows: an error for
// the unary minus operator, NULL for SAFE_NEGATE.
func negate(x value.Value, safe bool) (value.Value, error) {
	switch v := x.(type) {
	case value.IntValue:
		if int64(v) == math.MinInt64 {
			if safe {
				return nil, nil
			}
			return nil, fmt.Errorf("int64 overflow: -(%d)", int64(v))
		}
		return value.IntValue(-v), nil
	case *value.NumericValue:
		return &value.NumericValue{Rat: new(big.Rat).Neg(v.Rat), IsBigNumeric: v.IsBigNumeric}, nil
	}
	f, err := x.ToFloat64()
	if err != nil {
		return nil, err
	}
	return value.FloatValue(-f), nil
}

func SAFE_NEGATE(x value.Value) (value.Value, error) { return negate(x, true) }

// UNARY_MINUS implements the -x operator on non-literal operands; the
// analyzer folds literals itself.
func UNARY_MINUS(x value.Value) (value.Value, error) { return negate(x, false) }
