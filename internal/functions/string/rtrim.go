package string

import (
	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
)

// RTRIM trims from the right; see trimValue for the cutset rules.
func RTRIM(v, cutset value.Value) (value.Value, error) {
	return trimValue("RTRIM", v, cutset, false, true)
}

var BindRtrim = helper.ScalarN(trimBinder("RTRIM", false, true))
