package string

import (
	"fmt"

	"github.com/goccy/googlesqlite/internal/functions/helper"
	"github.com/goccy/googlesqlite/internal/value"
)

func ASCII(v string) (value.Value, error) {
	return value.IntValue(v[0]), nil
}

var BindAscii = helper.Scalar1(func(a value.Value) (value.Value, error) {
	// RawText: BYTES must use the first byte, not the first base64 digit.
	ascii, err := value.RawText(a)
	if err != nil {
		return nil, err
	}
	if ascii == "" {
		return value.IntValue(0), nil
	}
	// For STRING the first character must be ASCII; a multi-byte
	// UTF-8 lead byte is an error (safe_function.test, safe_ascii).
	if _, isBytes := a.(value.BytesValue); !isBytes && ascii[0] >= 0x80 {
		return nil, fmt.Errorf("ASCII: first character of %q is not an ASCII character", ascii)
	}
	return ASCII(ascii)
})
