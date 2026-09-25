package value

import (
	"fmt"
	"math"
	"math/big"
	"reflect"
	"time"

	"github.com/goccy/googlesqlite/internal/intervalvalue"
)

type Value interface {
	Add(Value) (Value, error)
	Sub(Value) (Value, error)
	Mul(Value) (Value, error)
	Div(Value) (Value, error)
	EQ(Value) (bool, error)
	GT(Value) (bool, error)
	GTE(Value) (bool, error)
	LT(Value) (bool, error)
	LTE(Value) (bool, error)
	ToInt64() (int64, error)
	ToString() (string, error)
	ToBytes() ([]byte, error)
	ToFloat64() (float64, error)
	ToBool() (bool, error)
	ToArray() (*ArrayValue, error)
	ToStruct() (*StructValue, error)
	ToJSON() (string, error)
	ToTime() (time.Time, error)
	ToRat() (*big.Rat, error)
	Format(verb rune) string
	Interface() any
}

func isDate(date string) bool {
	return dateRe.MatchString(date)
}

func isDatetime(datetime string) bool {
	return datetimeRe.MatchString(datetime)
}

func isTime(v string) bool {
	return timeRe.MatchString(v)
}

func isTimestamp(timestamp string) bool {
	loc, err := toLocation("")
	if err != nil {
		return false
	}
	if _, err := parseTimestamp(timestamp, loc); err != nil {
		return false
	}
	return true
}

func parseDate(date string) (time.Time, error) {
	return time.Parse("2006-01-02", date)
}

func parseDatetime(datetime string) (time.Time, error) {
	if t, err := time.Parse(datetimeFormat, datetime); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02 15:04:05.999999", datetime)
}

func parseTime(t string) (time.Time, error) {
	return time.Parse("15:04:05.999999", t)
}

// parseTimestamp parses timestamp in loc. A civil time that falls in a
// DST gap (02:30 on a spring-forward day) does not exist; BigQuery keeps
// the offset in effect before the transition, so
// TIMESTAMP('2024-03-10 02:30:00', 'America/New_York') is 07:30 UTC.
// Go's ParseInLocation instead picks the other offset for such times.
func parseTimestamp(timestamp string, loc *time.Location) (time.Time, error) {
	t, err := parseTimestampRaw(timestamp, loc)
	// Only zone-less input is interpreted in loc; input with its own
	// offset parses into a different Location and is left alone.
	if err != nil || loc == nil || loc == time.UTC || t.Location() != loc {
		return t, err
	}
	wall, werr := parseTimestampRaw(timestamp, time.UTC)
	if werr != nil || sameWallClock(wall, t) {
		return t, nil
	}
	// In a gap: apply the offset from just before the transition.
	_, before := t.Add(-2 * time.Hour).In(loc).Zone()
	return wall.Add(-time.Duration(before) * time.Second), nil
}

func sameWallClock(a, b time.Time) bool {
	return a.Year() == b.Year() && a.YearDay() == b.YearDay() && a.Hour() == b.Hour() && a.Minute() == b.Minute() && a.Second() == b.Second()
}

func parseTimestampRaw(timestamp string, loc *time.Location) (time.Time, error) {
	if t, err := time.ParseInLocation("2006-01-02T15:04:05.999999999Z07:00", timestamp, loc); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation("2006-01-02T15:04:05.999999999-07:00", timestamp, loc); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation("2006-01-02T15:04:05.999999999-07", timestamp, loc); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation("2006-01-02T15:04:05.999999999 MST", timestamp, loc); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation("2006-01-02T15:04:05", timestamp, loc); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation("2006-01-02 15:04:05.999999999Z07:00", timestamp, loc); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation("2006-01-02 15:04:05.999999999-07:00", timestamp, loc); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation("2006-01-02 15:04:05.999999999-07", timestamp, loc); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation("2006-01-02 15:04:05.999999999 MST", timestamp, loc); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation("2006-01-02 15:04:05.999999999", timestamp, loc); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation("2006-01-02", timestamp, loc); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("failed to parse timestamp. unexpected format %s", timestamp)
}

func DateFromInt64Value(v int64) (time.Time, error) {
	// Build from seconds rather than a time.Duration: days * 24h
	// overflows int64 nanoseconds outside roughly 1677..2262.
	return time.Unix(v*86400, 0).UTC(), nil
}

func TimestampFromFloatValue(f float64) (time.Time, error) {
	secs := math.Trunc(f)
	micros := math.Trunc((f-secs)*1e6 + 0.5)
	return time.Unix(int64(secs), int64(micros)*1000).UTC(), nil
}

func TimestampFromInt64Value(v int64) (time.Time, error) {
	sec := v / int64(time.Millisecond)
	msec := v - sec*int64(time.Millisecond)
	return time.Unix(sec, msec*int64(time.Millisecond)).UTC(), nil
}

func parseInterval(v string) (*IntervalValue, error) {
	if v == "" {
		return nil, fmt.Errorf("interval value is empty")
	}
	isNegative := v[0] == '-'
	interval, err := intervalvalue.ParseInterval(v)
	if err != nil {
		return nil, err
	}
	if isNegative && interval.Months > 0 {
		interval.Months *= -1
	}
	return &IntervalValue{IntervalValue: interval}, nil
}

func isNullValue(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	if _, ok := v.([]byte); ok {
		if rv.IsNil() {
			return true
		}
	}
	return false
}
