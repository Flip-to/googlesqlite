package numeric

import (
	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
)

func PARSE_BIGNUMERIC(numeric string) (value.Value, error) {
	r, err := parseNumericString(numeric, 38, true)
	if err != nil {
		return nil, err
	}
	return &value.NumericValue{Rat: r, IsBigNumeric: true}, nil
}

var BindParseBigNumeric = helper.Scalar1(func(a value.Value) (value.Value, error) {
	numeric, err := a.ToString()
	if err != nil {
		return nil, err
	}
	return PARSE_BIGNUMERIC(numeric)
})
