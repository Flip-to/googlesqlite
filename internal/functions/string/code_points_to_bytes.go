package string

import (
	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
)

func CODE_POINTS_TO_BYTES(v *value.ArrayValue) (value.Value, error) {
	b := make([]byte, 0, len(v.Values))
	for _, vv := range v.Values {
		// A NULL element makes the whole result NULL
		// (strings.test, code_points_to_string_bytes_null_element).
		if vv == nil {
			return nil, nil
		}
		i64, err := vv.ToInt64()
		if err != nil {
			return nil, err
		}
		bv, err := helper.SafeByte(i64)
		if err != nil {
			return nil, err
		}
		b = append(b, bv)
	}
	return value.BytesValue(b), nil
}

var BindCodePointsToBytes = helper.Scalar1(func(a value.Value) (value.Value, error) {
	v, err := a.ToArray()
	if err != nil {
		return nil, err
	}
	return CODE_POINTS_TO_BYTES(v)
})
