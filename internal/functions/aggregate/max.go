package aggregate

import (
	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
)

type MAX struct {
	initialized bool
	nan         value.Value
	max         value.Value
}

func (f *MAX) Step(v value.Value, opt *helper.Option) error {
	if v == nil {
		return nil
	}
	// Any NaN input makes the result NaN (aggregate_functions.md).
	if value.IsNaN(v) {
		f.nan = v
		return nil
	}
	if f.initialized {
		cond, err := v.GT(f.max)
		if err != nil {
			return err
		}
		if cond {
			f.max = v
		}
	} else {
		f.max = v
		f.initialized = true
	}
	return nil
}

func (f *MAX) Done() (value.Value, error) {
	if f.nan != nil {
		return f.nan, nil
	}
	return f.max, nil
}
