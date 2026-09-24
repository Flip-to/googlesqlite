package string

import (
	"fmt"
	"regexp"

	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
)

func REGEXP_EXTRACT_ALL(val value.Value, expr string) (value.Value, error) {
	re, err := regexp.Compile(expr)
	if err != nil {
		return nil, err
	}
	if re.NumSubexp() > 1 {
		return nil, fmt.Errorf("REGEXP_EXTRACT_ALL: regular expressions passed into extraction functions must not have more than 1 capturing group")
	}
	var v string
	isBytes := false
	switch val.(type) {
	case value.StringValue:
		v, err = val.ToString()
	case value.BytesValue:
		var bs []byte
		bs, err = val.ToBytes()
		v, isBytes = string(bs), true
	default:
		return nil, fmt.Errorf("REGEXP_EXTRACT_ALL: val argument must be STRING or BYTES")
	}
	if err != nil {
		return nil, err
	}
	ret := &value.ArrayValue{}
	for _, loc := range re.FindAllStringSubmatchIndex(v, -1) {
		// RE2 does not report an empty match at the very end of a
		// non-empty input: REGEXP_EXTRACT_ALL(b"abc", b"") has three
		// elements (bytes.test, function_regexp_extract_all; verified
		// against BigQuery).
		if loc[0] == loc[1] && loc[0] == len(v) && len(v) > 0 {
			continue
		}
		g := len(loc) - 2
		text := ""
		if loc[g] >= 0 {
			text = v[loc[g]:loc[g+1]]
		}
		if isBytes {
			ret.Values = append(ret.Values, value.BytesValue(text))
		} else {
			ret.Values = append(ret.Values, value.StringValue(text))
		}
	}
	return ret, nil
}

var BindRegexpExtractAll = helper.Scalar2(func(a, b value.Value) (value.Value, error) {
	expr, err := b.ToString()
	if err != nil {
		return nil, err
	}
	return REGEXP_EXTRACT_ALL(a, expr)
})
