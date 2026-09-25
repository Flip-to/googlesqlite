package string

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
)

func SPLIT(val, delimValue value.Value) (value.Value, error) {
	switch val.(type) {
	case value.StringValue:
		v, err := val.ToString()
		if err != nil {
			return nil, err
		}
		var delim = ","
		if delimValue != nil {
			delimV, err := delimValue.ToString()
			if err != nil {
				return nil, err
			}
			delim = delimV
		}
		ret := &value.ArrayValue{}
		if v == "" {
			// Splitting an empty STRING yields one empty element.
			ret.Values = append(ret.Values, value.StringValue(""))
			return ret, nil
		}
		for splitted := range strings.SplitSeq(v, delim) {
			ret.Values = append(ret.Values, value.StringValue(splitted))
		}
		return ret, nil
	case value.BytesValue:
		v, err := val.ToBytes()
		if err != nil {
			return nil, err
		}
		if delimValue == nil {
			return nil, fmt.Errorf("SPLIT: delimiter must be specified for bytes val")
		}
		delim, err := delimValue.ToBytes()
		if err != nil {
			return nil, err
		}
		ret := &value.ArrayValue{}
		switch {
		case len(v) == 0:
			// Splitting empty BYTES yields one empty element.
			ret.Values = append(ret.Values, value.BytesValue([]byte{}))
		case len(delim) == 0:
			// An empty delimiter splits BYTES into single bytes (not
			// UTF-8 sequences, which bytes.Split would produce).
			for i := range v {
				ret.Values = append(ret.Values, value.BytesValue(v[i:i+1]))
			}
		default:
			for splitted := range bytes.SplitSeq(v, delim) {
				ret.Values = append(ret.Values, value.BytesValue(splitted))
			}
		}
		return ret, nil
	}
	return nil, fmt.Errorf("SPLIT: val must be STRING or BYTES")
}

func BindSplit(args ...value.Value) (value.Value, error) {
	// A NULL value or delimiter yields a NULL array, matching
	// googlesql/compliance/testdata/strings.test.
	if helper.ExistsNull(args) {
		return nil, nil
	}
	var delim value.Value
	if len(args) > 1 {
		delim = args[1]
	}
	return SPLIT(args[0], delim)
}
