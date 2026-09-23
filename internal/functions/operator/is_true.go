package operator

import (
	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
)

func IS_TRUE(a value.Value) (value.Value, error) {
	if a == nil {
		return value.BoolValue(false), nil
	}
	b, err := a.ToBool()
	if err != nil {
		return nil, err
	}
	return value.BoolValue(b), nil
}

// BindIsTrue never returns NULL (NULL IS TRUE is FALSE), so it
// must use the KeepNull variant. IS NOT TRUE is NOT over this result.
var BindIsTrue = helper.Scalar1KeepNull(IS_TRUE)
