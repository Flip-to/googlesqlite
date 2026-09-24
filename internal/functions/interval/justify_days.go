package interval

import (
	"fmt"

	"github.com/goccy/googlesqlite/internal/value"
)

func BindJustifyDays(args ...value.Value) (value.Value, error) {
	if args[0] == nil {
		return nil, nil
	}
	interval, ok := args[0].(*value.IntervalValue)
	if !ok {
		return nil, fmt.Errorf("JUSTIFY_DAYS: unexpected argument type %T", args[0])
	}
	return JUSTIFY_DAYS(interval)
}
