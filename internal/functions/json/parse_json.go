package json

import (
	"bytes"
	"fmt"
	"math/big"
	"strconv"

	"github.com/goccy/go-json"
	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
)

// PARSE_JSON parses expr. wide_number_mode decides what happens to a
// number that INT64, UINT64 and FLOAT64 cannot represent without loss
// (json_functions.md, PARSE_JSON):
//
//   - 'exact' (the default) rejects it: PARSE_JSON('{"id":
//     922337203685477580701}', wide_number_mode=>'exact') raises
//     "Input number: 922337203685477580701 cannot round-trip through
//     string representation" on BigQuery;
//   - 'round' stores it as the nearest FLOAT64, giving
//     {"id":9.223372036854776e+20}.
func PARSE_JSON(expr, mode string) (value.Value, error) {
	switch mode {
	case "", "exact", "round":
	default:
		return nil, fmt.Errorf("PARSE_JSON: invalid wide_number_mode: %s", mode)
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(expr)))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	var tail any
	if err := dec.Decode(&tail); err == nil {
		return nil, fmt.Errorf("PARSE_JSON: unexpected trailing data")
	}
	changed := false
	var walk func(n any) (any, error)
	walk = func(n any) (any, error) {
		switch t := n.(type) {
		case json.Number:
			if numberIsExact(string(t)) {
				return t, nil
			}
			if mode == "round" {
				f, err := strconv.ParseFloat(string(t), 64)
				if err != nil {
					return nil, err
				}
				changed = true
				// Shortest round-trip form, as BigQuery prints it:
				// 9.223372036854776e+20.
				return json.Number(strconv.FormatFloat(f, 'g', -1, 64)), nil
			}
			return nil, fmt.Errorf("Invalid input: Input number: %s cannot round-trip through string representation", t) //nolint:staticcheck // BigQuery's error text
		case []any:
			for i, e := range t {
				r, err := walk(e)
				if err != nil {
					return nil, err
				}
				t[i] = r
			}
		case map[string]any:
			for k, e := range t {
				r, err := walk(e)
				if err != nil {
					return nil, err
				}
				t[k] = r
			}
		}
		return n, nil
	}
	v, err := walk(v)
	if err != nil {
		return nil, err
	}
	if !changed {
		return value.JsonValue(expr), nil
	}
	out, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return value.JsonValue(out), nil
}

// numberIsExact reports whether a JSON number fits INT64 or UINT64, or
// survives a FLOAT64 round trip: its shortest FLOAT64 rendering has the
// same decimal value.
func numberIsExact(s string) bool {
	if _, err := strconv.ParseInt(s, 10, 64); err == nil {
		return true
	}
	if _, err := strconv.ParseUint(s, 10, 64); err == nil {
		return true
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return false
	}
	want, ok := new(big.Rat).SetString(s)
	if !ok {
		return false
	}
	got, ok := new(big.Rat).SetString(strconv.FormatFloat(f, 'g', -1, 64))
	if !ok {
		return false
	}
	return want.Cmp(got) == 0
}

var BindParseJson = helper.Scalar2(func(a, b value.Value) (value.Value, error) {
	v, err := a.ToString()
	if err != nil {
		return nil, err
	}
	mode, err := b.ToString()
	if err != nil {
		return nil, err
	}
	return PARSE_JSON(v, mode)
})
