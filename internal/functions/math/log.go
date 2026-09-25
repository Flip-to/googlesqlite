package math

import (
	"fmt"
	"math"

	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
)

// LOG computes the logarithm of x to base y, following the special
// cases of the GoogleSQL LOG(X, Y) reference table: -inf for X or
// +inf for Y yields NaN, +inf for X yields -inf/+inf depending on
// whether Y is below or above 1, and X <= 0, Y <= 0 or Y = 1 is an
// error.
func LOG(x, y value.Value) (value.Value, error) {
	xf, err := x.ToFloat64()
	if err != nil {
		return nil, err
	}
	yf, err := y.ToFloat64()
	if err != nil {
		return nil, err
	}
	switch {
	case math.IsNaN(xf) || math.IsNaN(yf):
		return value.FloatValue(math.NaN()), nil
	case math.IsInf(xf, -1) || math.IsInf(yf, 1):
		return value.FloatValue(math.NaN()), nil
	case xf <= 0 || yf <= 0 || yf == 1:
		return nil, fmt.Errorf("LOG: invalid arguments LOG(%v, %v)", xf, yf)
	}
	return value.FloatValue(math.Log(xf) / math.Log(yf)), nil
}

var BindLog = helper.ScalarN(func(args ...value.Value) (value.Value, error) {
	if len(args) == 1 {
		return LN(args[0])
	}
	return LOG(args[0], args[1])
})
