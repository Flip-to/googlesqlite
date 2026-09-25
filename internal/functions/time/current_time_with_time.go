package time

import (
	"time"

	"github.com/goccy/googlesqlite/internal/value"
)

func CURRENT_TIME_WITH_TIME(v time.Time) (value.Value, error) {
	// CURRENT_* values carry microsecond precision, like CURRENT_TIMESTAMP,
	// so CURRENT_TIME = TIME(CURRENT_TIMESTAMP) holds on clocks finer than 1us.
	return value.TimeValue(v.Truncate(time.Microsecond)), nil
}
