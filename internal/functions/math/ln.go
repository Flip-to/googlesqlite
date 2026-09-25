package math

import (
	"fmt"
	"math"

	"github.com/goccy/googlesqlite/internal/value"
)

// LN is an error for X <= 0 (mathematical_functions.md LN), so
// SAFE.LOG(-1) is NULL (flipto-dbt probe safe_prefix-2502.h0).
func LN(x value.Value) (value.Value, error) {
	f, err := x.ToFloat64()
	if err != nil {
		return nil, err
	}
	if f <= 0 {
		return nil, fmt.Errorf("LN: invalid argument LN(%v)", f)
	}
	return value.FloatValue(math.Log(f)), nil
}
