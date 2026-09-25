package math

import (
	"fmt"
	"math"

	"github.com/goccy/googlesqlite/internal/value"
)

func COTH(x value.Value) (value.Value, error) {
	xv, err := x.ToFloat64()
	if err != nil {
		return nil, err
	}
	// COTH(0) is an error per docs/third_party/googlesql-docs/mathematical_functions.md.
	if xv == 0 {
		return nil, fmt.Errorf("COTH: division by zero: 1 / TANH(0)")
	}
	return value.FloatValue(1 / math.Tanh(xv)), nil
}
