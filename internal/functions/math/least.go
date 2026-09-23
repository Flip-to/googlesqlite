package math

import (
	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
)

func LEAST(args ...value.Value) (value.Value, error) {
	var min, nan value.Value
	for _, arg := range args {
		if arg == nil {
			return nil, nil
		}
		// NaN in any argument makes the result NaN
		// (mathematical_functions.md GREATEST / LEAST).
		if value.IsNaN(arg) {
			nan = arg
			continue
		}
		if min == nil {
			min = arg
			continue
		}
		less, err := arg.LT(min)
		if err != nil {
			return nil, err
		}
		if less {
			min = arg
		}
	}
	if nan != nil {
		return nan, nil
	}
	return min, nil
}

var BindLeast = helper.ScalarN(LEAST)
