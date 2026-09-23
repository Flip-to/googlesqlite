package string

import (
	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
)

// LTRIM trims from the left; see trimValue for the cutset rules.
func LTRIM(v, cutset value.Value) (value.Value, error) {
	return trimValue("LTRIM", v, cutset, true, false)
}

var BindLtrim = helper.ScalarN(trimBinder("LTRIM", true, false))
