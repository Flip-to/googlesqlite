package math

import (
	"math"
	"math/big"

	"github.com/goccy/googlesqlite/internal/value"
)

func SAFE_DIVIDE(x, y value.Value) (value.Value, error) {
	// NUMERIC and BIGNUMERIC must divide exactly and round to their
	// scale; going through float64 loses digits.
	xn, xok := x.(*value.NumericValue)
	yn, yok := y.(*value.NumericValue)
	if xok || yok {
		yr, err := y.ToRat()
		if err != nil {
			return nil, err
		}
		if yr.Sign() == 0 {
			return nil, nil
		}
		xr, err := x.ToRat()
		if err != nil {
			return nil, err
		}
		isBig := (xok && xn.IsBigNumeric) || (yok && yn.IsBigNumeric)
		return (&value.NumericValue{Rat: new(big.Rat).Set(xr), IsBigNumeric: isBig}).Div(y)
	}
	xv, err := x.ToFloat64()
	if err != nil {
		return nil, err
	}
	yv, err := y.ToFloat64()
	if err != nil {
		return nil, err
	}
	if yv == 0 {
		return nil, nil
	}
	q := xv / yv
	if math.IsInf(q, 0) && !math.IsInf(xv, 0) && !math.IsInf(yv, 0) {
		// Overflow of finite operands is an error for "/", so NULL here
		// (arithmetic_functions.test arithmetic_functions_14_safe_divide).
		return nil, nil
	}
	return value.FloatValue(q), nil
}
