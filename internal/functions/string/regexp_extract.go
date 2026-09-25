package string

import (
	"fmt"
	"regexp"

	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
)

func REGEXP_EXTRACT(val value.Value, expr string, position, occurrence int64) (value.Value, error) {
	if position <= 0 {
		return nil, fmt.Errorf("REGEXP_EXTRACT: unexpected position number. position must be positive number")
	}
	if occurrence <= 0 {
		return nil, fmt.Errorf("REGEXP_EXTRACT: unexpected occurrence number. occurrence must be positive number")
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return nil, err
	}
	if re.NumSubexp() > 1 {
		return nil, fmt.Errorf("REGEXP_EXTRACT: regular expressions passed into extraction functions must not have more than 1 capturing group")
	}
	posInt, err := helper.SafeInt(position)
	if err != nil {
		return nil, err
	}
	pos := posInt - 1
	occ, err := helper.SafeInt(occurrence)
	if err != nil {
		return nil, err
	}
	switch val.(type) {
	case value.StringValue:
		v, err := val.ToString()
		if err != nil {
			return nil, err
		}
		runes := []rune(v)
		if pos >= len(runes) {
			return nil, nil
		}
		rest := string(runes[pos:])
		matches := re.FindAllStringSubmatchIndex(rest, occ)
		if len(matches) < occ {
			return nil, nil
		}
		m := matches[occ-1]
		// The capture group's span, or the whole match without groups.
		start, end := m[len(m)-2], m[len(m)-1]
		if start < 0 {
			// An optional group that did not participate is NULL, not ''.
			return nil, nil
		}
		return value.StringValue(rest[start:end]), nil
	case value.BytesValue:
		v, err := val.ToBytes()
		if err != nil {
			return nil, err
		}
		if pos >= len(v) {
			return nil, nil
		}
		matches := re.FindAllSubmatch(v[pos:], occ)
		if len(matches) < occ {
			return nil, nil
		}
		match := matches[occ-1]
		return value.BytesValue(match[len(match)-1]), nil
	}
	return nil, fmt.Errorf("REGEXP_EXTRACT: val argument must be STRING or BYTES")
}

var BindRegexpExtract = helper.ScalarN(func(args ...value.Value) (value.Value, error) {
	regexp, err := args[1].ToString()
	if err != nil {
		return nil, err
	}
	var pos int64 = 1
	if len(args) > 2 {
		p, err := args[2].ToInt64()
		if err != nil {
			return nil, err
		}
		pos = p
	}
	var occurrence int64 = 1
	if len(args) > 3 {
		o, err := args[3].ToInt64()
		if err != nil {
			return nil, err
		}
		occurrence = o
	}
	return REGEXP_EXTRACT(args[0], regexp, pos, occurrence)
})
