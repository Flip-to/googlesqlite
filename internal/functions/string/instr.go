package string

import (
	"fmt"

	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
)

func INSTR(source, search value.Value, position, occurrence int64) (value.Value, error) {
	if position == 0 {
		return nil, fmt.Errorf("INSTR: position must not be 0")
	}
	if occurrence <= 0 {
		return nil, fmt.Errorf("INSTR: occurrence must be positive, got %d", occurrence)
	}
	switch source.(type) {
	case value.StringValue:
		if _, ok := search.(value.StringValue); !ok {
			return nil, fmt.Errorf("INSTR: value and subvalue must be the same type")
		}
		src, err := source.ToString()
		if err != nil {
			return nil, err
		}
		sub, err := search.ToString()
		if err != nil {
			return nil, err
		}
		// STRING positions count characters.
		return value.IntValue(instrIndex([]rune(src), []rune(sub), position, occurrence)), nil
	case value.BytesValue:
		if _, ok := search.(value.BytesValue); !ok {
			return nil, fmt.Errorf("INSTR: value and subvalue must be the same type")
		}
		src, err := source.ToBytes()
		if err != nil {
			return nil, err
		}
		sub, err := search.ToBytes()
		if err != nil {
			return nil, err
		}
		return value.IntValue(instrIndex(src, sub, position, occurrence)), nil
	}
	return nil, fmt.Errorf("INSTR: value must be STRING or BYTES")
}

// instrIndex returns the 1-based position of the occurrence-th match of
// sub in src, counting overlapping matches. A positive position starts
// the search there and scans forward; a negative one starts at that
// offset from the end (-1 is the last element) and scans backward.
// It returns 0 when there is no such match or position is out of range.
func instrIndex[T comparable](src, sub []T, position, occurrence int64) int64 {
	n, m := int64(len(src)), int64(len(sub))
	matchAt := func(i int64) bool {
		for k := int64(0); k < m; k++ {
			if src[i+k] != sub[k] {
				return false
			}
		}
		return true
	}
	var found int64
	if position > 0 {
		if position > n {
			return 0
		}
		for i := position - 1; i+m <= n; i++ {
			if matchAt(i) {
				found++
				if found == occurrence {
					return i + 1
				}
			}
		}
		return 0
	}
	start := n + position
	if start < 0 {
		return 0
	}
	if start+m > n {
		start = n - m
	}
	for i := start; i >= 0; i-- {
		if matchAt(i) {
			found++
			if found == occurrence {
				return i + 1
			}
		}
	}
	return 0
}

var BindInstr = helper.ScalarN(func(args ...value.Value) (value.Value, error) {
	if len(args) != 2 && len(args) != 3 && len(args) != 4 {
		return nil, fmt.Errorf("INSTR: invalid number of arguments: got %d, want one of 2, 3, 4", len(args))
	}
	var (
		// Per BigQuery docs, position defaults to 1 (start from the
		// first character). The previous default of 0 tripped the
		// `position > 0` guard in INSTR for 2-arg calls.
		position   int64 = 1
		occurrence int64 = 1
	)
	if len(args) >= 3 {
		pos, err := args[2].ToInt64()
		if err != nil {
			return nil, err
		}
		position = pos
	}
	if len(args) == 4 {
		occur, err := args[3].ToInt64()
		if err != nil {
			return nil, err
		}
		occurrence = occur
	}
	return INSTR(args[0], args[1], position, occurrence)
})
