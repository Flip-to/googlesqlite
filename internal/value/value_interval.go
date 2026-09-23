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

// intervalParts returns the interval as (months, days, nanos), the
// three independent components GoogleSQL keeps.
func intervalParts(iv *intervalvalue.IntervalValue) (months, days, nanos int64) {
	months = int64(iv.Years)*12 + int64(iv.Months)
	days = int64(iv.Days)
	nanos = ((int64(iv.Hours)*60+int64(iv.Minutes))*60+int64(iv.Seconds))*1e9 + int64(iv.SubSecondNanos)
	return
}

func intervalFromParts(months, days, nanos int64) (*IntervalValue, error) {
	const maxInt32 = 1<<31 - 1
	hours := nanos / (3600 * 1e9)
	if months > maxInt32 || months < -maxInt32 || days > maxInt32 || days < -maxInt32 || hours > maxInt32 || hours < -maxInt32 {
		return nil, fmt.Errorf("interval overflow")
	}
	iv := &intervalvalue.IntervalValue{
		Months:         int32(months),
		Days:           int32(days),
		Hours:          int32(hours),
		Minutes:        int32(nanos / (60 * 1e9) % 60),
		Seconds:        int32(nanos / 1e9 % 60),
		SubSecondNanos: int32(nanos % 1e9),
	}
	return &IntervalValue{iv.Canonicalize()}, nil
}

// compareKey normalises an interval to nanoseconds with 30-day months
// and 24-hour days, the order GoogleSQL uses for INTERVAL comparison.
// big.Int avoids the int64 overflow of time.Duration for long spans.
func (iv *IntervalValue) compareKey() *big.Int {
	months, days, nanos := intervalParts(iv.IntervalValue)
	k := big.NewInt(months)
	k.Mul(k, big.NewInt(30))
	k.Add(k, big.NewInt(days))
	k.Mul(k, big.NewInt(24*3600*1e9))
	return k.Add(k, big.NewInt(nanos))
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
		return nil, fmt.Errorf("unsupported add operator for interval value and %T", v)
	}
	m1, d1, n1 := intervalParts(iv.IntervalValue)
	m2, d2, n2 := intervalParts(other.IntervalValue)
	return intervalFromParts(m1+m2, d1+d2, n1+n2)
}

func (iv *IntervalValue) Sub(v Value) (Value, error) {
	other, ok := v.(*IntervalValue)
	if !ok {
		return nil, fmt.Errorf("unsupported sub operator for interval value and %T", v)
	}
	m1, d1, n1 := intervalParts(iv.IntervalValue)
	m2, d2, n2 := intervalParts(other.IntervalValue)
	return intervalFromParts(m1-m2, d1-d2, n1-n2)
}

func (iv *IntervalValue) Mul(v Value) (Value, error) {
	return nil, fmt.Errorf("unsupported mul operator for interval value")
}

func (iv *IntervalValue) Div(v Value) (Value, error) {
	return nil, fmt.Errorf("unsupported div operator for interval value")
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
