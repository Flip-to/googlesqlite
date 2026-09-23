package datetime

import (
	"fmt"
	"time"

	"github.com/goccy/googlesqlite/internal/functions/date"
	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
)

// DATETIME_DIFF counts the part boundaries crossed between b and a
// (datetime_functions.md): both sides are floored to the part before
// subtracting, so DATETIME '2024-01-01 00:01:00' minus
// '2023-12-31 23:59:59.999999' is 2 MINUTE.
func DATETIME_DIFF(a, b time.Time, part string) (value.Value, error) {
	unit := map[string]int64{
		"MICROSECOND": 1,
		"MILLISECOND": 1_000,
		"SECOND":      1_000_000,
		"MINUTE":      60 * 1_000_000,
		"HOUR":        3600 * 1_000_000,
	}
	if u, ok := unit[part]; ok {
		return value.IntValue(floorDiv(a.UnixMicro(), u) - floorDiv(b.UnixMicro(), u)), nil
	}
	value, err := date.DATE_DIFF(a, b, part)
	if err != nil {
		return nil, fmt.Errorf("DATETIME_DIFF: %w", err)
	}
	return value, nil
}

var BindDatetimeDiff = helper.Scalar3(func(a, b, c value.Value) (value.Value, error) {
	t, err := a.ToTime()
	if err != nil {
		return nil, err
	}
	t2, err := b.ToTime()
	if err != nil {
		return nil, err
	}
	part, err := c.ToString()
	if err != nil {
		return nil, err
	}
	return DATETIME_DIFF(t, t2, part)
})

func floorDiv(a, b int64) int64 {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}
