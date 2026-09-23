package math

import (
	"fmt"
	"math"
	"math/big"

	"github.com/goccy/googlesqlite/internal/value"
)

// ABS keeps the argument's type (mathematical_functions.md). Going
// through FLOAT64 lost INT64 precision: ABS(9223372036854775807)
// became 9223372036854775808 and then overflowed. ABS(MinInt64) is an
// error.
func ABS(a value.Value) (value.Value, error) {
	switch v := a.(type) {
	case value.IntValue:
		if int64(v) == math.MinInt64 {
			return nil, fmt.Errorf("int64 overflow: ABS(%d)", int64(v))
		}
		if v < 0 {
			return -v, nil
		}
		return v, nil
	case *value.NumericValue:
		return &value.NumericValue{Rat: new(big.Rat).Abs(v.Rat), IsBigNumeric: v.IsBigNumeric}, nil
	}
	f64, err := a.ToFloat64()
	if err != nil {
		return nil, err
	}
	return value.FloatValue(math.Abs(f64)), nil
}
