package math

import (
	"math"

	"github.com/goccy/googlesqlite/internal/value"
)

func CEIL(x value.Value) (value.Value, error) {
	if n, ok := x.(*value.NumericValue); ok {
		return numericResult(ratCeil(n.Rat), n.IsBigNumeric), nil
	}
	xv, err := x.ToFloat64()
	if err != nil {
		return nil, err
	}
	return value.FloatValue(math.Ceil(xv)), nil
}
