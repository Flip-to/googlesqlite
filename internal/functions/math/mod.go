package math

import (
	"fmt"
	"math"
	"math/big"

	"github.com/goccy/googlesqlite/internal/value"
)

func MOD(x, y value.Value) (value.Value, error) {
	// MOD of two INT64 arguments is INT64 (see the MOD return type
	// table). Go's % truncates toward zero, so the result has the sign
	// of X, as the reference requires.
	if xi, ok := x.(value.IntValue); ok {
		if yi, ok := y.(value.IntValue); ok {
			if yi == 0 {
				return nil, fmt.Errorf("MOD: zero divided")
			}
			if yi == -1 {
				return value.IntValue(0), nil
			}
			return xi % yi, nil
		}
	}
	// NUMERIC MOD is exact: x - y * TRUNC(x / y), with the sign of x.
	if xn, ok := x.(*value.NumericValue); ok {
		yr, err := y.ToRat()
		if err != nil {
			return nil, err
		}
		if yr.Sign() == 0 {
			return nil, fmt.Errorf("MOD: zero divided")
		}
		q := new(big.Rat).Quo(xn.Rat, yr)
		tq := new(big.Rat).SetInt(new(big.Int).Quo(q.Num(), q.Denom()))
		isBig := xn.IsBigNumeric
		if yn, ok := y.(*value.NumericValue); ok && yn.IsBigNumeric {
			isBig = true
		}
		return numericResult(new(big.Rat).Sub(xn.Rat, new(big.Rat).Mul(yr, tq)), isBig), nil
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
		return nil, fmt.Errorf("MOD: zero divided")
	}
	return value.FloatValue(math.Mod(xv, yv)), nil
}
