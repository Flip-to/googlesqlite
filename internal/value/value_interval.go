package value

import (
	"fmt"
	"math"
	"math/big"
	"strconv"
	"time"

	"github.com/goccy/googlesqlite/internal/intervalvalue"
)

type IntervalValue struct {
	*intervalvalue.IntervalValue
}

// Interval range limits (data-types.md, interval type): each of the
// three independent parts is bounded by 10000 years.
const (
	maxIntervalMonths = 120000
	maxIntervalDays   = 3660000
	maxIntervalHours  = 87840000
)

var (
	nanosPerSecond   = big.NewInt(1e9)
	nanosPerHour     = big.NewInt(3600 * 1e9)
	nanosPerDay      = big.NewInt(24 * 3600 * 1e9)
	maxIntervalNanos = new(big.Int).Mul(big.NewInt(maxIntervalHours), nanosPerHour)
)

// intervalTimeNanos returns the H:M:S.F part in nanoseconds. It needs
// more than 64 bits: 87840000 hours is about 3.2e20 nanoseconds.
func intervalTimeNanos(iv *intervalvalue.IntervalValue) *big.Int {
	secs := (int64(iv.Hours)*60+int64(iv.Minutes))*60 + int64(iv.Seconds)
	n := new(big.Int).Mul(big.NewInt(secs), nanosPerSecond)
	return n.Add(n, big.NewInt(int64(iv.SubSecondNanos)))
}

// IntervalParts returns the interval as (months, days, nanos), the
// three independent components GoogleSQL keeps.
func IntervalParts(iv *IntervalValue) (months, days int64, nanos *big.Int) {
	return int64(iv.Years)*12 + int64(iv.Months), int64(iv.Days), intervalTimeNanos(iv.IntervalValue)
}

// NewIntervalFromParts builds a canonical INTERVAL from its three
// parts, failing when a part is outside the INTERVAL range.
func NewIntervalFromParts(months, days, nanos *big.Int) (*IntervalValue, error) {
	if err := checkIntervalField("months", months, big.NewInt(maxIntervalMonths)); err != nil {
		return nil, err
	}
	if err := checkIntervalField("days", days, big.NewInt(maxIntervalDays)); err != nil {
		return nil, err
	}
	if err := checkIntervalField("nanoseconds", nanos, maxIntervalNanos); err != nil {
		return nil, err
	}
	m := months.Int64()
	hours, rem := new(big.Int).QuoRem(nanos, nanosPerHour, new(big.Int))
	r := rem.Int64()
	return &IntervalValue{&intervalvalue.IntervalValue{
		Years:          int32(m / 12),
		Months:         int32(m % 12),
		Days:           int32(days.Int64()),
		Hours:          int32(hours.Int64()),
		Minutes:        int32(r / (60 * 1e9)),
		Seconds:        int32(r / 1e9 % 60),
		SubSecondNanos: int32(r % 1e9),
	}}, nil
}

func checkIntervalField(name string, v, limit *big.Int) error {
	if v.CmpAbs(limit) > 0 {
		return fmt.Errorf("Interval field %s '%s' is out of range %s to %s", name, v, new(big.Int).Neg(limit), limit) //nolint:staticcheck // BigQuery's error text
	}
	return nil
}

func intervalFromParts(months, days int64, nanos *big.Int) (*IntervalValue, error) {
	return NewIntervalFromParts(big.NewInt(months), big.NewInt(days), nanos)
}

// DivideIntervalParts divides an interval given by its (possibly out of
// range) parts by n the way GoogleSQL does: the remainder of the months
// carries into days as 30-day months, the remainder of the days into
// the time part as 24-hour days, and the time part is truncated toward
// zero to microsecond precision.
func DivideIntervalParts(months, days, nanos *big.Int, n int64) (*IntervalValue, error) {
	if n == 0 {
		return nil, fmt.Errorf("division by zero: INTERVAL / 0")
	}
	d := big.NewInt(n)
	m, rm := new(big.Int).QuoRem(months, d, new(big.Int))
	dd := new(big.Int).Add(days, rm.Mul(rm, big.NewInt(30)))
	q, rd := new(big.Int).QuoRem(dd, d, new(big.Int))
	nn := new(big.Int).Add(nanos, rd.Mul(rd, nanosPerDay))
	nq := nn.Quo(nn, d)
	micro := big.NewInt(1000)
	nq.Quo(nq, micro).Mul(nq, micro)
	return NewIntervalFromParts(m, q, nq)
}

// compareKey normalises an interval to nanoseconds with 30-day months
// and 24-hour days, the order GoogleSQL uses for INTERVAL comparison.
func (iv *IntervalValue) compareKey() *big.Int {
	months, days, nanos := IntervalParts(iv)
	k := big.NewInt(months)
	k.Mul(k, big.NewInt(30))
	k.Add(k, big.NewInt(days))
	k.Mul(k, nanosPerDay)
	return k.Add(k, nanos)
}

func (iv *IntervalValue) compare(v Value) (int, error) {
	other, ok := v.(*IntervalValue)
	if !ok {
		return 0, fmt.Errorf("INTERVAL comparison: other side is %T", v)
	}
	return iv.compareKey().Cmp(other.compareKey()), nil
}

// Add and Sub combine INTERVAL values part by part (months, days,
// time), as GoogleSQL does.
func (iv *IntervalValue) Add(v Value) (Value, error) {
	other, ok := v.(*IntervalValue)
	if !ok {
		switch v.(type) {
		case DateValue, DatetimeValue, TimestampValue:
			// INTERVAL + DATE/DATETIME/TIMESTAMP is commutative.
			return v.Add(iv)
		}
		return nil, fmt.Errorf("unsupported add operator for interval value and %T", v)
	}
	m1, d1, n1 := IntervalParts(iv)
	m2, d2, n2 := IntervalParts(other)
	return intervalFromParts(m1+m2, d1+d2, n1.Add(n1, n2))
}

func (iv *IntervalValue) Sub(v Value) (Value, error) {
	other, ok := v.(*IntervalValue)
	if !ok {
		return nil, fmt.Errorf("unsupported sub operator for interval value and %T", v)
	}
	m1, d1, n1 := IntervalParts(iv)
	m2, d2, n2 := IntervalParts(other)
	return intervalFromParts(m1-m2, d1-d2, n1.Sub(n1, n2))
}

// Mul multiplies each part of the interval by an INT64.
func (iv *IntervalValue) Mul(v Value) (Value, error) {
	n, ok := v.(IntValue)
	if !ok {
		return nil, fmt.Errorf("unsupported mul operator for interval value and %T", v)
	}
	k := big.NewInt(int64(n))
	months, days, nanos := IntervalParts(iv)
	return NewIntervalFromParts(
		new(big.Int).Mul(big.NewInt(months), k),
		new(big.Int).Mul(big.NewInt(days), k),
		nanos.Mul(nanos, k))
}

// Div divides the interval by an INT64 (see DivideIntervalParts).
func (iv *IntervalValue) Div(v Value) (Value, error) {
	n, ok := v.(IntValue)
	if !ok {
		return nil, fmt.Errorf("unsupported div operator for interval value and %T", v)
	}
	months, days, nanos := IntervalParts(iv)
	return DivideIntervalParts(big.NewInt(months), big.NewInt(days), nanos, int64(n))
}

func (iv *IntervalValue) EQ(v Value) (bool, error) {
	c, err := iv.compare(v)
	return c == 0, err
}

func (iv *IntervalValue) GT(v Value) (bool, error) {
	c, err := iv.compare(v)
	return c > 0, err
}

func (iv *IntervalValue) GTE(v Value) (bool, error) {
	c, err := iv.compare(v)
	return c >= 0, err
}

func (iv *IntervalValue) LT(v Value) (bool, error) {
	c, err := iv.compare(v)
	return c < 0, err
}

func (iv *IntervalValue) LTE(v Value) (bool, error) {
	c, err := iv.compare(v)
	return c <= 0, err
}

func (iv *IntervalValue) ToInt64() (int64, error) {
	return 0, fmt.Errorf("unsupported int64 cast for interval value")
}

func (iv *IntervalValue) ToString() (string, error) {
	return iv.String(), nil
}

func (iv *IntervalValue) ToBytes() ([]byte, error) {
	s, err := iv.ToString()
	if err != nil {
		return nil, err
	}
	return []byte(s), nil
}

func (iv *IntervalValue) ToFloat64() (float64, error) {
	return 0, fmt.Errorf("unsupported float64 cast for interval value")
}

func (iv *IntervalValue) ToBool() (bool, error) {
	return false, fmt.Errorf("unsupported bool cast for interval value")
}

func (iv *IntervalValue) ToArray() (*ArrayValue, error) {
	return nil, fmt.Errorf("unsupported array cast for interval value")
}

func (iv *IntervalValue) ToStruct() (*StructValue, error) {
	return nil, fmt.Errorf("unsupported struct cast for interval value")
}

func (iv *IntervalValue) ToJSON() (string, error) {
	s, err := iv.ToString()
	if err != nil {
		return "", err
	}
	return strconv.Quote(s), nil
}

func (iv *IntervalValue) ToTime() (time.Time, error) {
	return time.Time{}, fmt.Errorf("unsupported time cast for interval value")
}

func (iv *IntervalValue) ToRat() (*big.Rat, error) {
	return nil, fmt.Errorf("unsupported numeric cast for interval value")
}

func (iv *IntervalValue) Format(verb rune) string {
	s, err := iv.ToString()
	if err != nil {
		return ""
	}
	if verb == 'T' {
		return fmt.Sprintf(`INTERVAL "%s" YEAR TO SECOND`, s)
	}
	return s
}

func (iv *IntervalValue) Interface() any {
	s, err := iv.ToString()
	if err != nil {
		return nil
	}
	return s
}

// DistinctKey returns a string that is equal for two values exactly
// when they are equal for DISTINCT / GROUP BY. It is the value's text
// except for INTERVAL, whose text differs between equal values
// (INTERVAL 1 MONTH = INTERVAL 30 DAY).
func DistinctKey(v Value) (string, error) {
	if iv, ok := v.(*IntervalValue); ok {
		return "interval:" + iv.compareKey().String(), nil
	}
	s, err := v.ToString()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%T:%s", v, s), nil
}

// NotDistinct reports whether a and b are equal under IS NOT DISTINCT
// FROM / GROUP BY semantics: NULL equals NULL and NaN equals NaN, also
// inside STRUCT and ARRAY values (operators.md, IS DISTINCT FROM).
func NotDistinct(a, b Value) (bool, error) {
	if a == nil || b == nil {
		return a == nil && b == nil, nil
	}
	switch x := a.(type) {
	case FloatValue:
		if y, ok := b.(FloatValue); ok && math.IsNaN(float64(x)) {
			return math.IsNaN(float64(y)), nil
		}
		if y, ok := b.(FloatValue); ok && math.IsNaN(float64(y)) {
			return false, nil
		}
	case *StructValue:
		y, ok := b.(*StructValue)
		if !ok || len(x.Values) != len(y.Values) {
			return false, nil
		}
		for i := range x.Values {
			eq, err := NotDistinct(x.Values[i], y.Values[i])
			if err != nil || !eq {
				return false, err
			}
		}
		return true, nil
	case *ArrayValue:
		y, ok := b.(*ArrayValue)
		if !ok || len(x.Values) != len(y.Values) {
			return false, nil
		}
		for i := range x.Values {
			eq, err := NotDistinct(x.Values[i], y.Values[i])
			if err != nil || !eq {
				return false, err
			}
		}
		return true, nil
	}
	return a.EQ(b)
}

// SQLEquals is SQL's three-valued "=": NULL when either side is NULL;
// for STRUCT, false if any field pair is unequal, otherwise NULL if any
// field comparison is NULL, otherwise true (operators.md, comparison
// operators). The result is nil for NULL.
func SQLEquals(a, b Value) (*bool, error) {
	if a == nil || b == nil {
		return nil, nil
	}
	x, ok := a.(*StructValue)
	if !ok {
		eq, err := a.EQ(b)
		if err != nil {
			return nil, err
		}
		return &eq, nil
	}
	y, err := b.ToStruct()
	if err != nil {
		return nil, err
	}
	if len(x.Values) != len(y.Values) {
		f := false
		return &f, nil
	}
	sawNull := false
	for i := range x.Values {
		eq, err := SQLEquals(x.Values[i], y.Values[i])
		if err != nil {
			return nil, err
		}
		if eq == nil {
			sawNull = true
			continue
		}
		if !*eq {
			f := false
			return &f, nil
		}
	}
	if sawNull {
		return nil, nil
	}
	t := true
	return &t, nil
}

// addInterval adds sign*iv to t the way GoogleSQL does: months first,
// clamping the day to the end of the resulting month (DATE '2024-01-31'
// + INTERVAL 1 MONTH is 2024-02-29), then days, then the time part.
// time.Date normalises day overflow into the next month instead.
func addInterval(t time.Time, iv *IntervalValue, sign int) time.Time {
	months, days, nanos := IntervalParts(iv)
	t = addMonthsClamped(t, sign*int(months))
	t = t.AddDate(0, 0, sign*int(days))
	secs, sub := new(big.Int).QuoRem(nanos, nanosPerSecond, new(big.Int))
	s, ns := int64(sign)*secs.Int64(), int64(sign)*sub.Int64()
	// time.Duration holds about 292 years; add the seconds in chunks.
	const chunk = int64(1e9)
	for s > chunk || s < -chunk {
		step := chunk
		if s < 0 {
			step = -chunk
		}
		t = t.Add(time.Duration(step) * time.Second)
		s -= step
	}
	return t.Add(time.Duration(s)*time.Second + time.Duration(ns))
}

// addMonthsClamped adds m months and clamps the day to the last day of
// the target month.
func addMonthsClamped(t time.Time, m int) time.Time {
	y, mo, d := t.Date()
	first := time.Date(y, mo, 1, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location()).AddDate(0, m, 0)
	last := first.AddDate(0, 1, -1).Day()
	if d > last {
		d = last
	}
	return time.Date(first.Year(), first.Month(), d, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
}
