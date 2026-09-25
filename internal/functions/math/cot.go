package math

import (
	"fmt"
	"math"

	"github.com/goccy/googlesqlite/internal/value"
)

func COT(x value.Value) (value.Value, error) {
	xv, err := x.ToFloat64()
	if err != nil {
		return nil, err
	}
	// COT(0) is an error per docs/third_party/googlesql-docs/mathematical_functions.md.
	if xv == 0 {
		return nil, fmt.Errorf("COT: division by zero: 1 / TAN(0)")
	}
	return value.FloatValue(1 / math.Tan(xv)), nil
}
