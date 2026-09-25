package string

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
)

// trimValue implements TRIM, LTRIM and RTRIM. A nil cutset means the
// argument was omitted: STRING strips the Unicode whitespace class and
// BYTES strips ASCII whitespace. An explicit empty cutset trims
// nothing. For BYTES the cutset is a set of bytes, not UTF-8
// characters.
func trimValue(name string, v, cutsetV value.Value, left, right bool) (value.Value, error) {
	switch v.(type) {
	case value.StringValue:
		s, err := v.ToString()
		if err != nil {
			return nil, err
		}
		if cutsetV == nil {
			return value.StringValue(trimFunc(s, unicode.IsSpace, left, right)), nil
		}
		cutset, err := cutsetV.ToString()
		if err != nil {
			return nil, err
		}
		return value.StringValue(trimFunc(s, func(r rune) bool { return strings.ContainsRune(cutset, r) }, left, right)), nil
	case value.BytesValue:
		b, err := v.ToBytes()
		if err != nil {
			return nil, err
		}
		var set [256]bool
		if cutsetV == nil {
			for _, c := range []byte(" \t\n\r\f\v") {
				set[c] = true
			}
		} else {
			cb, err := cutsetV.ToBytes()
			if err != nil {
				return nil, err
			}
			for _, c := range cb {
				set[c] = true
			}
		}
		start, end := 0, len(b)
		if left {
			for start < end && set[b[start]] {
				start++
			}
		}
		if right {
			for end > start && set[b[end-1]] {
				end--
			}
		}
		return value.BytesValue(b[start:end]), nil
	}
	return nil, fmt.Errorf("%s: value must be STRING or BYTES", name)
}

func trimFunc(s string, f func(rune) bool, left, right bool) string {
	if left {
		s = strings.TrimLeftFunc(s, f)
	}
	if right {
		s = strings.TrimRightFunc(s, f)
	}
	return s
}

func TRIM(v, cutsetV value.Value) (value.Value, error) {
	return trimValue("TRIM", v, cutsetV, true, true)
}

func trimBinder(name string, left, right bool) func(args ...value.Value) (value.Value, error) {
	return func(args ...value.Value) (value.Value, error) {
		if len(args) != 1 && len(args) != 2 {
			return nil, fmt.Errorf("%s: invalid number of arguments: got %d, want 1 or 2", name, len(args))
		}
		var cutset value.Value
		if len(args) == 2 {
			cutset = args[1]
		}
		return trimValue(name, args[0], cutset, left, right)
	}
}

var BindTrim = helper.ScalarN(trimBinder("TRIM", true, true))
