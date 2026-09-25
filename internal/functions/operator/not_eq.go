package operator

import (
	"github.com/goccy/googlesqlite/internal/value"
)

// NOT_EQ is three-valued: see value.SQLEquals.
func NOT_EQ(a, b value.Value) (value.Value, error) {
	eq, err := value.SQLEquals(a, b)
	if err != nil || eq == nil {
		return nil, err
	}
	return value.BoolValue(!*eq), nil
}
