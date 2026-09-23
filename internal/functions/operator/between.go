package operator

import (
	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
)

// BETWEEN is `target >= start AND target <= end` with SQL's
// three-valued AND: a FALSE side makes it FALSE even when the other
// side is NULL (1 BETWEEN 5 AND NULL is FALSE), a NULL side otherwise
// makes it NULL.
func BETWEEN(target, start, end value.Value) (value.Value, error) {
	if target == nil {
		return nil, nil
	}
	ge, err := compareOrNull(target, start, true)
	if err != nil {
		return nil, err
	}
	le, err := compareOrNull(target, end, false)
	if err != nil {
		return nil, err
	}
	if (ge != nil && !*ge) || (le != nil && !*le) {
		return value.BoolValue(false), nil
	}
	if ge == nil || le == nil {
		return nil, nil
	}
	return value.BoolValue(true), nil
}

// compareOrNull returns target >= bound (gte) or target <= bound, or
// nil when bound is NULL.
func compareOrNull(target, bound value.Value, gte bool) (*bool, error) {
	if bound == nil {
		return nil, nil
	}
	var ok bool
	var err error
	if gte {
		ok, err = target.GTE(bound)
	} else {
		ok, err = target.LTE(bound)
	}
	if err != nil {
		return nil, err
	}
	return &ok, nil
}

var BindBetween = helper.Scalar3KeepNull(BETWEEN)
