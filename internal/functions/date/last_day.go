package date

import (
	"fmt"
	"time"

	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
)

// LAST_DAY returns the last day of the date part containing t
// (date_functions.md LAST_DAY; additional_date_time_functions.test
// last_day_date / last_day_datetime).
func LAST_DAY(t time.Time, part string) (value.Value, error) {
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	switch part {
	case "YEAR":
		return value.DateValue(time.Date(t.Year()+1, time.Month(1), 0, 0, 0, 0, 0, t.Location())), nil
	case "QUARTER":
		firstOfNext := (int(t.Month())-1)/3*3 + 4
		return value.DateValue(time.Date(t.Year(), time.Month(firstOfNext), 0, 0, 0, 0, 0, t.Location())), nil
	case "MONTH":
		return value.DateValue(time.Date(t.Year(), t.Month()+1, 0, 0, 0, 0, 0, t.Location())), nil
	case "ISOYEAR":
		// The ISO year ends the day before the Monday of the week
		// containing January 4 of the next ISO year.
		isoYear, _ := day.ISOWeek()
		jan4 := time.Date(isoYear+1, time.January, 4, 0, 0, 0, 0, t.Location())
		monday := jan4.AddDate(0, 0, -((int(jan4.Weekday()) + 6) % 7))
		return value.DateValue(monday.AddDate(0, 0, -1)), nil
	}
	start, ok := weekStarts[part]
	switch part {
	case "WEEK":
		start, ok = time.Sunday, true
	case "ISOWEEK":
		start, ok = time.Monday, true
	}
	if ok {
		return value.DateValue(day.AddDate(0, 0, (int(start)+6-int(day.Weekday())+7)%7)), nil
	}
	return nil, fmt.Errorf("LAST_DAY: unexpected part %s", part)
}

func BindLastDay(args ...value.Value) (value.Value, error) {
	if len(args) != 1 && len(args) != 2 {
		return nil, fmt.Errorf("LAST_DAY: invalid number of arguments: got %d, want 1 or 2", len(args))
	}
	if helper.ExistsNull(args) {
		return nil, nil
	}
	t, err := args[0].ToTime()
	if err != nil {
		return nil, err
	}
	var part = "MONTH"
	if len(args) == 2 {
		p, err := args[1].ToString()
		if err != nil {
			return nil, err
		}
		part = p
	}
	return LAST_DAY(t, part)
}
