package string

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/goccy/googlesqlite/internal/value"
)

func REPLACE(originalValue, fromValue, toValue value.Value) (value.Value, error) {
	switch originalValue.(type) {
	case value.StringValue:
		v, err := originalValue.ToString()
		if err != nil {
			return nil, err
		}
		from, err := fromValue.ToString()
		if err != nil {
			return nil, err
		}
		to, err := toValue.ToString()
		if err != nil {
			return nil, err
		}
		// An empty search value replaces nothing (string_functions.md
		// REPLACE); strings.ReplaceAll would insert between every rune.
		if from == "" {
			return value.StringValue(v), nil
		}
		return value.StringValue(strings.ReplaceAll(v, from, to)), nil
	case value.BytesValue:
		v, err := originalValue.ToBytes()
		if err != nil {
			return nil, err
		}
		from, err := fromValue.ToBytes()
		if err != nil {
			return nil, err
		}
		to, err := toValue.ToBytes()
		if err != nil {
			return nil, err
		}
		if len(from) == 0 {
			return value.BytesValue(v), nil
		}
		return value.BytesValue(bytes.ReplaceAll(v, from, to)), nil
	}
	return nil, fmt.Errorf("REPLACE: originalValue must be STRING or BYTES")
}
