// Package rangefn implements the runtime body for GoogleSQL RANGE
// functions: the constructor `RANGE(start, end)` and the accessors
// `RANGE_START`, `RANGE_END`, `RANGE_IS_START_UNBOUNDED`,
// `RANGE_IS_END_UNBOUNDED`.
//
// RANGE is a first-party GoogleSQL type (TYPE_RANGE = 29 in
// googlesql/public/type.proto, gated by FEATURE_RANGE_TYPE), so the
// `*value.RangeValue` shape lives in `internal/value` alongside the
// other primitives.
package rangefn

import (
	"fmt"

	"github.com/goccy/googlesqlite/internal/value"
)

// elementHeader returns the ValueType tag that should be stored on a
// new RangeValue from one of its bounds. Both bounds must agree (or
// at least one must be NULL); we pick the first non-nil bound's
// header.
func elementHeader(start, end value.Value) value.ValueType {
	pick := func(v value.Value) value.ValueType {
		switch v.(type) {
		case value.DateValue:
			return value.DateValueType
		case value.DatetimeValue:
			return value.DatetimeValueType
		case value.TimestampValue:
			return value.TimestampValueType
		}
		return ""
	}
	if h := pick(start); h != "" {
		return h
	}
	return pick(end)
}

// RANGE constructs a RANGE value from start and end bounds.
func RANGE(start, end value.Value) (value.Value, error) {
	// Both bounds may be NULL (an unbounded range); two non-NULL bounds
	// must satisfy start < end (range-functions.md, RANGE;
	// range_constructors.test).
	if start != nil && end != nil {
		lt, err := start.LT(end)
		if err != nil {
			return nil, err
		}
		if !lt {
			return nil, fmt.Errorf("Range start element must be smaller than range end element") //nolint:staticcheck // BigQuery's error text
		}
	}
	return &value.RangeValue{
		Start:      start,
		End:        end,
		ElemHeader: elementHeader(start, end),
	}, nil
}

func BindRange(args ...value.Value) (value.Value, error) {
	if len(args) != 2 {
		return nil, fmt.Errorf("RANGE: invalid number of arguments: got %d, want 2", len(args))
	}
	return RANGE(args[0], args[1])
}

// RANGE_START returns the lower bound of the range (NULL when
// unbounded).
func RANGE_START(r *value.RangeValue) (value.Value, error) {
	if r == nil {
		return nil, nil
	}
	return r.Start, nil
}

func BindRangeStart(args ...value.Value) (value.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("RANGE_START: invalid number of arguments: got %d, want 1", len(args))
	}
	if args[0] == nil {
		return nil, nil
	}
	r, ok := args[0].(*value.RangeValue)
	if !ok {
		return nil, fmt.Errorf("RANGE_START: expected RANGE, got %T", args[0])
	}
	return RANGE_START(r)
}

// RANGE_END returns the upper bound of the range (NULL when
// unbounded).
func RANGE_END(r *value.RangeValue) (value.Value, error) {
	if r == nil {
		return nil, nil
	}
	return r.End, nil
}

func BindRangeEnd(args ...value.Value) (value.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("RANGE_END: invalid number of arguments: got %d, want 1", len(args))
	}
	if args[0] == nil {
		return nil, nil
	}
	r, ok := args[0].(*value.RangeValue)
	if !ok {
		return nil, fmt.Errorf("RANGE_END: expected RANGE, got %T", args[0])
	}
	return RANGE_END(r)
}

// RANGE_IS_START_UNBOUNDED reports whether the range's start is NULL.
func RANGE_IS_START_UNBOUNDED(r *value.RangeValue) (value.Value, error) {
	if r == nil {
		return nil, nil
	}
	return value.BoolValue(r.Start == nil), nil
}

func BindRangeIsStartUnbounded(args ...value.Value) (value.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("RANGE_IS_START_UNBOUNDED: invalid number of arguments: got %d, want 1", len(args))
	}
	if args[0] == nil {
		return nil, nil
	}
	r, ok := args[0].(*value.RangeValue)
	if !ok {
		return nil, fmt.Errorf("RANGE_IS_START_UNBOUNDED: expected RANGE, got %T", args[0])
	}
	return RANGE_IS_START_UNBOUNDED(r)
}

// RANGE_IS_END_UNBOUNDED reports whether the range's end is NULL.
func RANGE_IS_END_UNBOUNDED(r *value.RangeValue) (value.Value, error) {
	if r == nil {
		return nil, nil
	}
	return value.BoolValue(r.End == nil), nil
}

func BindRangeIsEndUnbounded(args ...value.Value) (value.Value, error) {
	if len(args) != 1 {
		return nil, fmt.Errorf("RANGE_IS_END_UNBOUNDED: invalid number of arguments: got %d, want 1", len(args))
	}
	if args[0] == nil {
		return nil, nil
	}
	r, ok := args[0].(*value.RangeValue)
	if !ok {
		return nil, fmt.Errorf("RANGE_IS_END_UNBOUNDED: expected RANGE, got %T", args[0])
	}
	return RANGE_IS_END_UNBOUNDED(r)
}
