package string

import (
	"fmt"
	"regexp"
	"unicode/utf8"

	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
)

func REGEXP_INSTR(sourceValue, exprValue value.Value, position, occurrence, occurrencePos int64) (value.Value, error) {
	if position <= 0 {
		return nil, fmt.Errorf("REGEXP_INSTR: position must be positive")
	}
	if occurrence <= 0 {
		return nil, fmt.Errorf("REGEXP_INSTR: occurrence must be positive")
	}
	if occurrencePos != 0 && occurrencePos != 1 {
		return nil, fmt.Errorf("REGEXP_INSTR: occurrence_position must be 0 or 1")
	}
	occ, err := helper.SafeInt(occurrence)
	if err != nil {
		return nil, err
	}
	switch sourceValue.(type) {
	case value.StringValue, value.BytesValue:
	default:
		return nil, fmt.Errorf("REGEXP_INSTR: source value must be STRING or BYTES")
	}
	// BYTES arrive as Latin-1 STRING through BytesAsLatin1, so one rune
	// is one byte and the STRING logic below covers both types.
	source, err := value.RawText(sourceValue)
	if err != nil {
		return nil, err
	}
	expr, err := value.RawText(exprValue)
	if err != nil {
		return nil, err
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return nil, err
	}
	if re.NumSubexp() > 1 {
		return nil, fmt.Errorf("REGEXP_INSTR: regular expression has more than one capturing group")
	}
	runes := []rune(source)
	if expr == "" || position > int64(len(runes)) {
		return value.IntValue(0), nil
	}
	// Work in characters: slice from the start position, then map byte
	// offsets of the match back to character offsets.
	rest := string(runes[position-1:])
	matches := re.FindAllStringSubmatchIndex(rest, occ)
	if len(matches) < occ {
		return value.IntValue(0), nil
	}
	m := matches[occ-1]
	start, end := m[0], m[1]
	// With one capturing group the position is that group's.
	if len(m) >= 4 && m[2] >= 0 {
		start, end = m[2], m[3]
	}
	off := start
	if occurrencePos == 1 {
		off = end
	}
	return value.IntValue(position + int64(utf8.RuneCountInString(rest[:off]))), nil
}

var BindRegexpInstr = helper.ScalarN(func(args ...value.Value) (value.Value, error) {
	var (
		pos           int64 = 1
		occurrence    int64 = 1
		occurrencePos int64 = 0
	)
	if len(args) > 2 {
		p, err := args[2].ToInt64()
		if err != nil {
			return nil, err
		}
		pos = p
	}
	if len(args) > 3 {
		o, err := args[3].ToInt64()
		if err != nil {
			return nil, err
		}
		occurrence = o
	}
	if len(args) > 4 {
		p, err := args[4].ToInt64()
		if err != nil {
			return nil, err
		}
		occurrencePos = p
	}
	return REGEXP_INSTR(args[0], args[1], pos, occurrence, occurrencePos)
})
