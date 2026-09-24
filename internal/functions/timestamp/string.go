package timestamp

import (
	"fmt"
	"time"

	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
)

func STRING(t time.Time, zone string) (value.Value, error) {
	loc, err := value.ToLocation(zone)
	if err != nil {
		return nil, err
	}
	t = t.In(loc)
	// BigQuery prints the zone's offset as +HH, or +HH:MM when it has
	// minutes (STRING(TIMESTAMP ..., 'Asia/Kolkata') ends in +05:30).
	_, off := t.Zone()
	sign := "+"
	if off < 0 {
		sign, off = "-", -off
	}
	zoneText := fmt.Sprintf("%s%02d", sign, off/3600)
	if m := off % 3600 / 60; m != 0 {
		zoneText += fmt.Sprintf(":%02d", m)
	}
	return value.StringValue(t.Format("2006-01-02 15:04:05") + value.FractionInGroups(t) + zoneText), nil
}

func BindString(args ...value.Value) (value.Value, error) {
	if len(args) != 1 && len(args) != 2 {
		return nil, fmt.Errorf("STRING: invalid number of arguments: got %d, want 1 or 2", len(args))
	}
	if helper.ExistsNull(args) {
		return nil, nil
	}
	jsonValue, ok := args[0].(value.JsonValue)
	if ok {
		return value.StringValue(fmt.Sprint(jsonValue.Interface())), nil
	}
	t, err := args[0].ToTime()
	if err != nil {
		return nil, err
	}
	var zone string
	if len(args) == 2 {
		z, err := args[1].ToString()
		if err != nil {
			return nil, err
		}
		zone = z
	}
	return STRING(t, zone)
}
