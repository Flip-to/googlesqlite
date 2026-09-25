package math

import (
	"math"

	"github.com/goccy/googlesqlite/internal/value"
)

func IEEE_DIVIDE(x, y value.Value) (value.Value, error) {
	x64, err := x.ToFloat64()
	if err != nil {
		return nil, err
	}
	y64, err := y.ToFloat64()
	if err != nil {
		return nil, err
	}
	if x64 == 0 {
		if y64 == 0 {
			return value.FloatValue(math.NaN()), nil
		}
		if math.IsNaN(y64) {
			return value.FloatValue(math.NaN()), nil
		}
		return value.FloatValue(0), nil
	}
	if math.IsNaN(x64) {
		if y64 == 0 {
			return value.FloatValue(math.NaN()), nil
		}
	} else if math.IsInf(x64, 0) || math.IsInf(x64, -1) {
		if math.IsInf(y64, 0) || math.IsInf(y64, -1) {
			return value.FloatValue(math.NaN()), nil
		}
	}
	// Go float division follows IEEE 754, including the sign of a zero
	// divisor: 1 / -0.0 is -inf (math_functions.test math_abs_zero).
	return value.FloatValue(x64 / y64), nil
}
