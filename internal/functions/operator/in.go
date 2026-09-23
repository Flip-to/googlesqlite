package operator

import (
	"github.com/goccy/googlesqlite/internal/value"
)

// IN is true if any element equals a, NULL if no element does but a
// comparison is NULL (a NULL element or field), and false otherwise.
// So `3 IN (1, 2, NULL)` is NULL, as in SQL.
func IN(a value.Value, values ...value.Value) (value.Value, error) {
	if a == nil {
		return nil, nil
	}
	sawNull := false
	for _, v := range values {
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

func BindIn(args ...value.Value) (value.Value, error) {
	return IN(args[0], args[1:]...)
}
