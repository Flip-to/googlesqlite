package interval

import (
	"math/big"

	"github.com/goccy/googlesqlite/internal/value"
)

// MAKE_INTERVAL builds an interval from its fields; the year and month
// fields combine into the months part and hours, minutes and seconds
// into the time part, each of which must be in the INTERVAL range.
func MAKE_INTERVAL(year, month, day, hour, minute, second int64) (value.Value, error) {
	months := new(big.Int).Mul(big.NewInt(year), big.NewInt(12))
	months.Add(months, big.NewInt(month))
	secs := new(big.Int).Mul(big.NewInt(hour), big.NewInt(3600))
	secs.Add(secs, new(big.Int).Mul(big.NewInt(minute), big.NewInt(60)))
	secs.Add(secs, big.NewInt(second))
	return value.NewIntervalFromParts(months, big.NewInt(day), secs.Mul(secs, big.NewInt(1e9)))
}

func BindMakeInterval(args ...value.Value) (value.Value, error) {
	var fields [6]int64
	for i := range fields {
		if i >= len(args) {
			break
		}
		if args[i] == nil {
			return nil, nil
		}
		v, err := args[i].ToInt64()
		if err != nil {
			return nil, err
		}
		fields[i] = v
	}
	return MAKE_INTERVAL(fields[0], fields[1], fields[2], fields[3], fields[4], fields[5])
}
