package string

import (
	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
)

// BytesAsLatin1 adapts a REGEXP_* binder so BYTES arguments match byte
// by byte, as RE2 does in Latin-1 mode for BYTES in the reference
// implementation. Go's regexp reads UTF-8, so every byte is mapped to
// the rune with the same value, the STRING implementation runs, and
// STRING results (including array elements) are mapped back to BYTES.
// Positions stay correct because one rune stands for one byte.
func BytesAsLatin1(fn helper.BindFunction) helper.BindFunction {
	return func(args ...value.Value) (value.Value, error) {
		if len(args) == 0 {
			return fn(args...)
		}
		if _, ok := args[0].(value.BytesValue); !ok {
			return fn(args...)
		}
		conv := make([]value.Value, len(args))
		for i, a := range args {
			if b, ok := a.(value.BytesValue); ok {
				conv[i] = value.StringValue(bytesAsRunes(string(b)))
				continue
			}
			conv[i] = a
		}
		out, err := fn(conv...)
		if err != nil || out == nil {
			return out, err
		}
		return latin1ToBytes(out)
	}
}

func latin1ToBytes(v value.Value) (value.Value, error) {
	switch x := v.(type) {
	case value.StringValue:
		r := []rune(string(x))
		b := make([]byte, len(r))
		for i, c := range r {
			b[i] = byte(c)
		}
		return value.BytesValue(b), nil
	case *value.ArrayValue:
		out := &value.ArrayValue{Values: make([]value.Value, len(x.Values))}
		for i, e := range x.Values {
			if e == nil {
				continue
			}
			c, err := latin1ToBytes(e)
			if err != nil {
				return nil, err
			}
			out.Values[i] = c
		}
		return out, nil
	}
	return v, nil
}
