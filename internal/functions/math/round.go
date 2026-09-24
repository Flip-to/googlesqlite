package math

import (
	"fmt"
	"math/big"

	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
	"gonum.org/v1/gonum/floats/scalar"
)

// Rounding modes accepted by ROUND(NUMERIC|BIGNUMERIC, INT64, ROUNDING_MODE)
// (docs/third_party/googlesql-docs/mathematical_functions.md).
const (
	roundHalfAwayFromZero = "ROUND_HALF_AWAY_FROM_ZERO"
	roundHalfEven         = "ROUND_HALF_EVEN"
)

func ROUND(x value.Value, precision int) (value.Value, error) {
	if n, ok := x.(*value.NumericValue); ok {
		r, err := ratRound(n.Rat, precision, roundHalfAwayFromZero)
		if err != nil {
			return nil, err
		}
		if !value.CheckNumericRange(r, n.IsBigNumeric) {
			// Rounding can carry past the type's range (flipto-dbt
			// probe round-0868.27).
			return nil, fmt.Errorf("numeric overflow: ROUND(%s, %d)", n.FloatString(9), precision)
		}
		return numericResult(r, n.IsBigNumeric), nil
	}
	xv, err := x.ToFloat64()
	if err != nil {
		return nil, err
	}
	return value.FloatValue(scalar.Round(xv, precision)), nil
}

// ratRound rounds r to `precision` digits after the decimal point
// (negative precision rounds to the left of it) exactly, without a
// FLOAT64 round trip.
func ratRound(r *big.Rat, precision int, mode string) (*big.Rat, error) {
	if precision > 100 {
		precision = 100
	}
	if precision < -100 {
		return new(big.Rat), nil
	}
	scale := new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(absInt(precision))), nil))
	v := new(big.Rat).Set(r)
	if precision >= 0 {
		v.Mul(v, scale)
	} else {
		v.Quo(v, scale)
	}
	neg := v.Sign() < 0
	a := new(big.Rat).Abs(v)
	q, rem := new(big.Int).QuoRem(a.Num(), a.Denom(), new(big.Int))
	// Compare 2*rem with the denominator to decide the half-way case.
	cmp := new(big.Int).Mul(rem, big.NewInt(2)).Cmp(a.Denom())
	switch {
	case cmp > 0:
		q.Add(q, big.NewInt(1))
	case cmp == 0:
		switch mode {
		case roundHalfAwayFromZero:
			q.Add(q, big.NewInt(1))
		case roundHalfEven:
			if q.Bit(0) == 1 {
				q.Add(q, big.NewInt(1))
			}
		default:
			return nil, fmt.Errorf("ROUND: unsupported rounding mode %q", mode)
		}
	}
	if neg {
		q.Neg(q)
	}
	out := new(big.Rat).SetInt(q)
	if precision >= 0 {
		out.Quo(out, scale)
	} else {
		out.Mul(out, scale)
	}
	return out, nil
}

func absInt(i int) int {
	if i < 0 {
		return -i
	}
	return i
}

var BindRound = helper.ScalarN(func(args ...value.Value) (value.Value, error) {
	if len(args) < 1 || len(args) > 3 {
		return nil, fmt.Errorf("ROUND: invalid number of arguments: got %d, want 1 to 3", len(args))
	}
	var precision = 0
	if len(args) >= 2 {
		i64, err := args[1].ToInt64()
		if err != nil {
			return nil, err
		}
		precision, err = helper.SafeInt(i64)
		if err != nil {
			return nil, err
		}
	}
	if len(args) == 3 {
		mode, err := args[2].ToString()
		if err != nil {
			return nil, err
		}
		n, ok := args[0].(*value.NumericValue)
		if !ok {
			return nil, fmt.Errorf("ROUND: rounding mode is only supported for NUMERIC and BIGNUMERIC")
		}
		r, err := ratRound(n.Rat, precision, mode)
		if err != nil {
			return nil, err
		}
		if !value.CheckNumericRange(r, n.IsBigNumeric) {
			// Rounding can carry past the type's range (flipto-dbt
			// probe round-0868.27).
			return nil, fmt.Errorf("numeric overflow: ROUND(%s, %d)", n.FloatString(9), precision)
		}
		return numericResult(r, n.IsBigNumeric), nil
	}
	return ROUND(args[0], precision)
})
