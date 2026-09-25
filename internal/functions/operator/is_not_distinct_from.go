package operator

import (
	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
)

// IS_NOT_DISTINCT_FROM is the negation of IS_DISTINCT_FROM.
func IS_NOT_DISTINCT_FROM(a, b value.Value) (value.Value, error) {
	same, err := value.NotDistinct(a, b)
	if err != nil {
		return nil, err
	}
	return value.BoolValue(same), nil
}

// BindIsNotDistinctFrom observes NULL itself, so it must use the
// KeepNull variant.
var BindIsNotDistinctFrom = helper.Scalar2KeepNull(IS_NOT_DISTINCT_FROM)
