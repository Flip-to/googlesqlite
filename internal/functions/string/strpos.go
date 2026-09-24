package string

import (
	"bytes"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
)

func STRPOS(val, search value.Value) (value.Value, error) {
	switch val.(type) {
	case value.StringValue:
		v, err := val.ToString()
		if err != nil {
			return nil, err
		}
		s, err := search.ToString()
		if err != nil {
			return nil, err
		}
		// STRING positions count characters, not bytes
		// (strings.test, diff_strings_and_bytes_9).
		idx := strings.Index(v, s)
		if idx < 0 {
			return value.IntValue(0), nil
		}
		return value.IntValue(utf8.RuneCountInString(v[:idx]) + 1), nil
	case value.BytesValue:
		v, err := val.ToBytes()
		if err != nil {
			return nil, err
		}
		s, err := search.ToBytes()
		if err != nil {
			return nil, err
		}
		return value.IntValue(bytes.Index(v, s) + 1), nil
	}
	return nil, fmt.Errorf("STRPOS: argument type must be STRING or BYTES")
}

var BindStrpos = helper.Scalar2(STRPOS)
