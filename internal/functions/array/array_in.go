package array

import (
	"github.com/goccy/googlesqlite/internal/value"
)

// ARRAY_IN implements `a IN UNNEST(b)` with SQL's three-valued rules
// (operators.md, IN operator): an empty or NULL array gives FALSE;
// otherwise TRUE on a match, NULL if some comparison is NULL (NULL a,
// NULL element or NULL STRUCT field), and FALSE otherwise.
func ARRAY_IN(a, b value.Value) (value.Value, error) {
	if b == nil {
		return value.BoolValue(false), nil
	}
	array, err := b.ToArray()
	if err != nil {
		return nil, err
	}
	if len(array.Values) == 0 {
		return value.BoolValue(false), nil
	}
	sawNull := false
	for _, v := range array.Values {
		eq, err := value.SQLEquals(a, v)
		if err != nil {
			return nil, err
		}
		if eq == nil {
			sawNull = true
			continue
		}
		if *eq {
			return value.BoolValue(true), nil
		}
	}
	if sawNull {
		return nil, nil
	}
	return value.BoolValue(false), nil
}

func BindInArray(args ...value.Value) (value.Value, error) {
	return ARRAY_IN(args[0], args[1])
}
