package math

import (
	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
)

func GREATEST(args ...value.Value) (value.Value, error) {
	var max, nan value.Value
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
		if max == nil {
			max = arg
			continue
		}
		gt, err := arg.GT(max)
		if err != nil {
			return nil, err
		}
		if gt {
			max = arg
		}
	}
	if nan != nil {
		return nan, nil
	}
	return max, nil
}

var BindGreatest = helper.ScalarN(GREATEST)
