package operator

import (
	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
)

func IS_FALSE(a value.Value) (value.Value, error) {
	if a == nil {
		return value.BoolValue(false), nil
	}
	b, err := a.ToBool()
	if err != nil {
		return nil, err
	}
	return value.BoolValue(!b), nil
}

// BindIsFalse never returns NULL (NULL IS FALSE is FALSE), so it
// must use the KeepNull variant. IS NOT FALSE is NOT over this result.
var BindIsFalse = helper.Scalar1KeepNull(IS_FALSE)
