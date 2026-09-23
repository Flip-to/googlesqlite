package operator

import (
	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
)

// IS_DISTINCT_FROM treats NULL = NULL and NaN = NaN, also inside
// STRUCT and ARRAY values (operators.md, IS DISTINCT FROM).
func IS_DISTINCT_FROM(a, b value.Value) (value.Value, error) {
	same, err := value.NotDistinct(a, b)
	if err != nil {
		return nil, err
	}
	return value.BoolValue(!same), nil
}

// BindIsDistinctFrom observes NULL itself, so it must use the
// KeepNull variant.
var BindIsDistinctFrom = helper.Scalar2KeepNull(IS_DISTINCT_FROM)
