package timestamp

import (
	"fmt"
	"time"

	"github.com/goccy/googlesqlite/internal/functions/date"
	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
)

// TIMESTAMP_DIFF counts whole units elapsed between b and a,
// truncating toward zero. DAY is a 24-hour unit (timestamp_functions.md),
// not a count of calendar-date boundaries. Microsecond integers avoid
// the ~292-year overflow of time.Duration.
func TIMESTAMP_DIFF(a, b time.Time, part string) (value.Value, error) {
	micros := a.UnixMicro() - b.UnixMicro()
	unit := map[string]int64{
		"MICROSECOND": 1,
		"MILLISECOND": 1_000,
		"SECOND":      1_000_000,
		"MINUTE":      60 * 1_000_000,
		"HOUR":        3600 * 1_000_000,
		"DAY":         24 * 3600 * 1_000_000,
	}
	if u, ok := unit[part]; ok {
		return value.IntValue(micros / u), nil
	}
	dateDiff, err := date.DATE_DIFF(a, b, part)
	if err != nil {
		return nil, fmt.Errorf("TIMESTAMP_DIFF: %w", err)
	}
	return dateDiff, nil
}

var BindTimestampDiff = helper.Scalar3(func(a, b, c value.Value) (value.Value, error) {
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
	return TIMESTAMP_DIFF(t, t2, part)
})
