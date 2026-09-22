package math

import (
	"fmt"
	"math"

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
