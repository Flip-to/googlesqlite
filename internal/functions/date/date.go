package date

import (
	"fmt"
	"time"

	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
)

func DATE(args ...value.Value) (value.Value, error) {
	if len(args) == 3 {
		year, err := args[0].ToInt64()
		if err != nil {
			return nil, err
		}
		month, err := args[1].ToInt64()
		if err != nil {
			return nil, err
		}
		day, err := args[2].ToInt64()
		if err != nil {
			return nil, err
		}
		yearInt, err := helper.SafeInt(year)
		if err != nil {
			return nil, err
		}
		monthInt, err := helper.SafeInt(month)
		if err != nil {
			return nil, err
		}
		dayInt, err := helper.SafeInt(day)
		if err != nil {
			return nil, err
		}
		// Out-of-range parts are an error, not a roll-over:
		// DATE(2024, 13, 1) fails, so SAFE.DATE(2024, 13, 1) is NULL.
		t := time.Date(yearInt, time.Month(monthInt), dayInt, 0, 0, 0, 0, time.UTC)
		if yearInt < 1 || yearInt > 9999 || int(t.Month()) != monthInt || t.Day() != dayInt || t.Year() != yearInt {
			return nil, fmt.Errorf("input calculates to invalid date: %d-%d-%d", yearInt, monthInt, dayInt)
		}
		return value.DateValue(t), nil
	} else if len(args) == 2 {
		t, err := args[0].ToTime()
		if err != nil {
			return nil, err
		}
		zone, err := args[1].ToString()
		if err != nil {
			return nil, err
		}
		loc, err := value.ToLocation(zone)
		if err != nil {
			return nil, err
		}
		return value.DateValue(t.In(loc)), nil
	} else {
		t, err := args[0].ToTime()
		if err != nil {
			return nil, err
		}
		return value.DateValue(t), nil
	}
}

// BindDate short-circuits to NULL when any argument is NULL; DATE
// itself performs the arity dispatch.
var BindDate = helper.ScalarN(DATE)
