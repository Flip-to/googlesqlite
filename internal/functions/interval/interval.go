package interval

import (
	"fmt"
	"math/big"

	"github.com/goccy/googlesqlite/internal/value"
)

// INTERVAL builds `INTERVAL v part`, failing when the value is outside
// the INTERVAL range.
func INTERVAL(v int64, part string) (value.Value, error) {
	var months, days, nanos int64
	var nanoUnit int64
	switch part {
	case "YEAR":
		months = 12
	case "QUARTER":
		months = 3
	case "MONTH":
		months = 1
	case "WEEK":
		days = 7
	case "DAY":
		days = 1
	case "HOUR":
		nanoUnit = 3600 * 1e9
	case "MINUTE":
		nanoUnit = 60 * 1e9
	case "SECOND":
		nanoUnit = 1e9
	case "MILLISECOND":
		nanoUnit = 1e6
	case "MICROSECOND":
		nanoUnit = 1e3
	case "NANOSECOND":
		nanoUnit = 1
	default:
		return nil, fmt.Errorf("unexpected interval part: %s", part)
	}
	nanos = nanoUnit
	n := big.NewInt(v)
	return value.NewIntervalFromParts(
		new(big.Int).Mul(n, big.NewInt(months)),
		new(big.Int).Mul(n, big.NewInt(days)),
		new(big.Int).Mul(n, big.NewInt(nanos)))
}

func BindInterval(args ...value.Value) (value.Value, error) {
	if args[0] == nil || args[1] == nil {
		return nil, nil
	}
	v, err := args[0].ToInt64()
	if err != nil {
		return nil, err
	}
	part, err := args[1].ToString()
	if err != nil {
		return nil, err
	}
	return INTERVAL(v, part)
}
