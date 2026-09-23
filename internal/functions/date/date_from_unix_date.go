package date

import (
	"time"

	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
)

func DATE_FROM_UNIX_DATE(unixdate int64) (value.Value, error) {
	// Seconds, not time.Duration (which overflows past about 292 years),
	// and UTC so the host time zone cannot shift the civil date.
	t := time.Unix(unixdate*86400, 0).UTC()
	return value.DateValue(t), nil
}

var BindDateFromUnixDate = helper.Scalar1(func(a value.Value) (value.Value, error) {
	unixdate, err := a.ToInt64()
	if err != nil {
		return nil, err
	}
	return DATE_FROM_UNIX_DATE(unixdate)
})
