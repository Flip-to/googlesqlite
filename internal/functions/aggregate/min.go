package aggregate

import (
	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
)

type MIN struct {
	initialized bool
	nan         value.Value
	min         value.Value
}

func (f *MIN) Step(v value.Value, opt *helper.Option) error {
	if v == nil {
		return nil
	}
	// Any NaN input makes the result NaN (aggregate_functions.md).
	if value.IsNaN(v) {
		f.nan = v
		return nil
	}
	if f.initialized {
		cond, err := v.LT(f.min)
		if err != nil {
			return err
		}
		if cond {
			f.min = v
		}
	} else {
		f.min = v
		f.initialized = true
	}
	return nil
}

func (f *MIN) Done() (value.Value, error) {
	if f.nan != nil {
		return f.nan, nil
	}
	return f.min, nil
}
