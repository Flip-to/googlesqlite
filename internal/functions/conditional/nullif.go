package conditional

import (
	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
)

func NULLIF(expr, exprToMatch value.Value) (value.Value, error) {
	if expr == nil {
		return nil, nil
	}
	// A NULL comparison is not a match, so NULLIF('a', NULL) is 'a'.
	eq, err := value.SQLEquals(expr, exprToMatch)
	if err != nil {
		return nil, err
	}
	if eq != nil && *eq {
		return nil, nil
	}
	return expr, nil
}

// BindNullIf observes NULL itself (it returns NULL when expr is NULL
// and compares against a possibly-NULL exprToMatch), so it must use
// the KeepNull variant.
var BindNullIf = helper.Scalar2KeepNull(NULLIF)
