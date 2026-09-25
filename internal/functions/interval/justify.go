package interval

import (
	"math/big"

	"github.com/goccy/googlesqlite/internal/value"
)

var nanosPerDay = big.NewInt(24 * 3600 * 1e9)

// justifyHours carries whole 24-hour periods of the time part into the
// days and then makes the days and the time part share one sign.
func justifyHours(days int64, nanos *big.Int) (int64, *big.Int) {
	q, r := new(big.Int).QuoRem(nanos, nanosPerDay, new(big.Int))
	days += q.Int64()
	switch {
	case days > 0 && r.Sign() < 0:
		days--
		r.Add(r, nanosPerDay)
	case days < 0 && r.Sign() > 0:
		days++
		r.Sub(r, nanosPerDay)
	}
	return days, r
}

// justifyDays carries whole 30-day periods into the months and then
// makes the months and days share one sign.
func justifyDays(months, days int64) (int64, int64) {
	months += days / 30
	days %= 30
	switch {
	case months > 0 && days < 0:
		months--
		days += 30
	case months < 0 && days > 0:
		months++
		days -= 30
	}
	return months, days
}

func build(months, days int64, nanos *big.Int) (value.Value, error) {
	return value.NewIntervalFromParts(big.NewInt(months), big.NewInt(days), nanos)
}

func JUSTIFY_DAYS(v *value.IntervalValue) (value.Value, error) {
	months, days, nanos := value.IntervalParts(v)
	months, days = justifyDays(months, days)
	return build(months, days, nanos)
}

func JUSTIFY_HOURS(v *value.IntervalValue) (value.Value, error) {
	months, days, nanos := value.IntervalParts(v)
	days, nanos = justifyHours(days, nanos)
	return build(months, days, nanos)
}

func JUSTIFY_INTERVAL(v *value.IntervalValue) (value.Value, error) {
	months, days, nanos := value.IntervalParts(v)
	q, r := new(big.Int).QuoRem(nanos, nanosPerDay, new(big.Int))
	days += q.Int64()
	months, days = justifyDays(months, days)
	days, nanos = justifyHours(days, r)
	// Borrowing a day for the time part can flip the sign of the days.
	months, days = justifyDays(months, days)
	return build(months, days, nanos)
}
