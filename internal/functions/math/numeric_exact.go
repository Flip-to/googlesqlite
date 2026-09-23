package math

import (
	"math/big"

	"github.com/goccy/googlesqlite/internal/value"
)

// Exact NUMERIC / BIGNUMERIC helpers. Converting through FLOAT64 lost
// precision: FLOOR(NUMERIC '99999999999999999999999999999.9') was
// 99999999999999991433150857220 instead of 1E+29.

func ratFloor(r *big.Rat) *big.Rat {
	q := new(big.Int).Quo(r.Num(), r.Denom()) // truncates toward zero
	if r.Sign() < 0 && !r.IsInt() {
		q.Sub(q, big.NewInt(1))
	}
	return new(big.Rat).SetInt(q)
}

func ratCeil(r *big.Rat) *big.Rat {
	q := new(big.Int).Quo(r.Num(), r.Denom())
	if r.Sign() > 0 && !r.IsInt() {
		q.Add(q, big.NewInt(1))
	}
	return new(big.Rat).SetInt(q)
}

func numericResult(r *big.Rat, isBig bool) value.Value {
	return &value.NumericValue{Rat: r, IsBigNumeric: isBig}
}
