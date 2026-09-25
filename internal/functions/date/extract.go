package date

import (
	"fmt"
	"time"

	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
)

func EXTRACT(v value.Value, part, zone string) (value.Value, error) {
	switch vv := v.(type) {
	case *value.IntervalValue:
		switch part {
		case "YEAR":
			return value.IntValue(vv.Years), nil
		case "MONTH":
			return value.IntValue(vv.Months), nil
		case "DAY":
			return value.IntValue(vv.Days), nil
		case "HOUR":
			return value.IntValue(vv.Hours), nil
		case "MINUTE":
			return value.IntValue(vv.Minutes), nil
		case "SECOND":
			return value.IntValue(vv.Seconds), nil
		case "MILLISECOND":
			return value.IntValue(vv.SubSecondNanos / int32(time.Millisecond)), nil
		case "MICROSECOND":
			return value.IntValue(vv.SubSecondNanos / int32(time.Microsecond)), nil
		}
		return nil, fmt.Errorf("EXTRACT: unexpected part %s for interval", part)
	case value.DateValue, value.DatetimeValue, value.TimeValue, value.TimestampValue:
		t, err := v.ToTime()
		if err != nil {
			return nil, err
		}
		if _, ok := v.(value.TimestampValue); ok {
			loc, err := value.ToLocation(zone)
			if err != nil {
				return nil, err
			}
			t = t.In(loc)
		}
		switch part {
		case "ISOYEAR":
			year, _ := t.ISOWeek()
			return value.IntValue(year), nil
		case "YEAR":
			return value.IntValue(t.Year()), nil
		case "MONTH":
			return value.IntValue(t.Month()), nil
		case "ISOWEEK":
			_, week := t.ISOWeek()
			return value.IntValue(week), nil
		case "WEEK":
			return value.IntValue(weekOfYear(t, time.Sunday)), nil
		case "WEEK_SUNDAY", "WEEK_MONDAY", "WEEK_TUESDAY", "WEEK_WEDNESDAY", "WEEK_THURSDAY", "WEEK_FRIDAY", "WEEK_SATURDAY":
			return value.IntValue(weekOfYear(t, weekStarts[part])), nil
		case "DAY":
			return value.IntValue(t.Day()), nil
		case "DAYOFYEAR":
			return value.IntValue(t.YearDay()), nil
		case "DAYOFWEEK":
			return value.IntValue(int(t.Weekday()) + 1), nil
		case "QUARTER":
			day := t.YearDay()
			const quarterDays = 91
			switch {
			case day <= quarterDays:
				return value.IntValue(1), nil
			case day <= quarterDays*2:
				return value.IntValue(2), nil
			case day <= quarterDays*3:
				return value.IntValue(3), nil
			}
			return value.IntValue(4), nil
		case "HOUR":
			return value.IntValue(t.Hour()), nil
		case "MINUTE":
			return value.IntValue(t.Minute()), nil
		case "SECOND":
			return value.IntValue(t.Second()), nil
		case "MILLISECOND":
			return value.IntValue(t.Nanosecond() / int(time.Millisecond)), nil
		case "MICROSECOND":
			return value.IntValue(t.Nanosecond() / int(time.Microsecond)), nil
		case "DATE":
			return value.DateValue(t), nil
		case "DATETIME":
			return value.DatetimeValue(t), nil
		case "TIME":
			return value.TimeValue(t), nil
		}
		return nil, fmt.Errorf("EXTRACT: unexpected part %s for data/datetime/time/timestamp", part)
	}
	return nil, fmt.Errorf("EXTRACT: value type must be INTERVAL or DATE or DATETIME or TIME or TIMESTAMP")
}

func BindExtract(args ...value.Value) (value.Value, error) {
	if helper.ExistsNull(args) {
		return nil, nil
	}
	part, err := args[1].ToString()
	if err != nil {
		return nil, err
	}
	zone := "UTC"
	if len(args) == 3 {
		timeZone, err := args[2].ToString()
		if err != nil {
			return nil, err
		}
		zone = timeZone
	}
	return EXTRACT(args[0], part, zone)
}

func BindExtractDate(args ...value.Value) (value.Value, error) {
	if helper.ExistsNull(args) {
		return nil, nil
	}
	zone := "UTC"
	if len(args) == 2 {
		timeZone, err := args[1].ToString()
		if err != nil {
			return nil, err
		}
		zone = timeZone
	}
	return EXTRACT(args[0], "DATE", zone)
}

var weekStarts = map[string]time.Weekday{
	"WEEK_SUNDAY":    time.Sunday,
	"WEEK_MONDAY":    time.Monday,
	"WEEK_TUESDAY":   time.Tuesday,
	"WEEK_WEDNESDAY": time.Wednesday,
	"WEEK_THURSDAY":  time.Thursday,
	"WEEK_FRIDAY":    time.Friday,
	"WEEK_SATURDAY":  time.Saturday,
}

// weekOfYear is EXTRACT(WEEK(<start>)): weeks begin on start, and days
// before the year's first start day are in week 0 (date_functions.md,
// EXTRACT). The range is [0, 53].
func weekOfYear(t time.Time, start time.Weekday) int {
	offset := (int(t.Weekday()) - int(start) + 7) % 7
	return (t.YearDay() - 1 + 7 - offset) / 7
}
