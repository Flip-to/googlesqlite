package internal

import (
	"encoding/base32"
	"encoding/base64"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	googlesql "github.com/goccy/go-googlesql"
	"github.com/goccy/go-json"

	"github.com/goccy/googlesqlite/internal/value"
)

// bindCastFormat implements CAST(expr AS type FORMAT fmt). Arguments:
// expr, format, JSON-encoded from type, JSON-encoded to type, safe.
// BYTES <-> STRING formats follow format-elements.md ("Format bytes as
// string" / "Format string as bytes"); date/time <-> STRING follow the
// date and time sections. Other type pairs fall back to the plain CAST.
func bindCastFormat(args ...value.Value) (value.Value, error) {
	if len(args) != 5 && len(args) != 6 {
		return nil, fmt.Errorf("CAST FORMAT: invalid number of arguments: got %d, want 5 or 6", len(args))
	}
	var fromType, toType Type
	for i, dst := range []*Type{&fromType, &toType} {
		s, err := args[2+i].ToString()
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(s), dst); err != nil {
			return nil, err
		}
	}
	safe, err := args[4].ToBool()
	if err != nil {
		return nil, err
	}
	if args[0] == nil || args[1] == nil || (len(args) == 6 && args[5] == nil) {
		return nil, nil
	}
	fail := func(err error) (value.Value, error) {
		if safe {
			return nil, nil
		}
		return nil, err
	}
	format, err := args[1].ToString()
	if err != nil {
		return nil, err
	}
	rawFormat := format
	format = strings.ToUpper(strings.TrimSpace(format))
	switch v := args[0].(type) {
	case value.BytesValue:
		if toType.Kind == int(googlesql.TypeKindTypeString) {
			s, err := formatBytesAsString([]byte(v), format)
			if err != nil {
				return fail(err)
			}
			return value.StringValue(s), nil
		}
	case value.DateValue, value.DatetimeValue, value.TimestampValue, value.TimeValue:
		if toType.Kind == int(googlesql.TypeKindTypeString) {
			t, err := v.ToTime()
			if err != nil {
				return nil, err
			}
			if _, ok := v.(value.TimestampValue); ok {
				// Timestamps render in the default time zone (UTC) unless
				// AT TIME ZONE names another one.
				loc := time.UTC
				if len(args) == 6 {
					name, err := args[5].ToString()
					if err != nil {
						return nil, err
					}
					if loc, err = zoneLocation(name); err != nil {
						return fail(err)
					}
				}
				t = t.In(loc)
			}
			s, err := formatDateTimeElements(t, rawFormat)
			if err != nil {
				return fail(err)
			}
			return value.StringValue(s), nil
		}
	case value.StringValue:
		if toType.Kind == int(googlesql.TypeKindTypeBytes) {
			b, err := parseStringAsBytes(string(v), format)
			if err != nil {
				return fail(err)
			}
			return value.BytesValue(b), nil
		}
		targets := map[int]castParseTarget{
			int(googlesql.TypeKindTypeDate):      castParseDate,
			int(googlesql.TypeKindTypeDatetime):  castParseDatetime,
			int(googlesql.TypeKindTypeTime):      castParseTime,
			int(googlesql.TypeKindTypeTimestamp): castParseTimestamp,
		}
		if target, ok := targets[toType.Kind]; ok {
			loc := time.UTC
			if len(args) == 6 {
				name, err := args[5].ToString()
				if err != nil {
					return nil, err
				}
				if loc, err = zoneLocation(name); err != nil {
					return fail(err)
				}
			}
			t, err := parseStringWithFormat(string(v), rawFormat, target, loc, time.Now().UTC())
			if err != nil {
				return fail(err)
			}
			switch target {
			case castParseDate:
				return value.DateValue(t), nil
			case castParseDatetime:
				return value.DatetimeValue(t), nil
			case castParseTime:
				return value.TimeValue(t), nil
			}
			return value.TimestampValue(t.UTC()), nil
		}
	}
	return CAST(args[0], &fromType, &toType, safe)
}

func formatBytesAsString(b []byte, format string) (string, error) {
	switch format {
	case "HEX", "BASE16":
		return encodeBits(b, 4), nil
	case "BASE2":
		return encodeBits(b, 1), nil
	case "BASE8":
		return encodeBits(b, 3), nil
	case "BASE32":
		return base32.StdEncoding.EncodeToString(b), nil
	case "BASE64":
		return base64.StdEncoding.EncodeToString(b), nil
	case "BASE64M":
		s := base64.StdEncoding.EncodeToString(b)
		var out strings.Builder
		for len(s) > 76 {
			out.WriteString(s[:76])
			out.WriteByte('\n')
			s = s[76:]
		}
		out.WriteString(s)
		return out.String(), nil
	case "ASCII":
		for _, c := range b {
			if c > 0x7f {
				return "", fmt.Errorf("CAST FORMAT 'ASCII': byte 0x%02x is not ASCII", c)
			}
		}
		return string(b), nil
	case "UTF-8", "UTF8":
		if !utf8.Valid(b) {
			return "", fmt.Errorf("CAST FORMAT 'UTF-8': input is not valid UTF-8")
		}
		return string(b), nil
	}
	return "", fmt.Errorf("CAST: invalid format %q for BYTES to STRING", format)
}

func parseStringAsBytes(s, format string) ([]byte, error) {
	switch format {
	case "HEX", "BASE16":
		return decodeBits(s, 4)
	case "BASE2":
		return decodeBits(s, 1)
	case "BASE8":
		return decodeBits(s, 3)
	case "BASE32":
		return base32.StdEncoding.DecodeString(s)
	case "BASE64", "BASE64M":
		// Whitespace is ignored for BASE64 and BASE64M.
		return base64.StdEncoding.DecodeString(strings.Join(strings.Fields(s), ""))
	case "ASCII":
		for i := 0; i < len(s); i++ {
			if s[i] > 0x7f {
				return nil, fmt.Errorf("CAST FORMAT 'ASCII': input is not ASCII")
			}
		}
		return []byte(s), nil
	case "UTF-8", "UTF8":
		return []byte(s), nil
	}
	return nil, fmt.Errorf("CAST: invalid format %q for STRING to BYTES", format)
}

const radixDigits = "0123456789abcdef"

// encodeBits writes b as a big-endian bit stream in groups of width
// bits (1 for BASE2, 3 for BASE8, 4 for BASE16), zero-padding the
// final group.
func encodeBits(b []byte, width int) string {
	var out strings.Builder
	var acc, n uint
	for _, c := range b {
		acc = acc<<8 | uint(c)
		n += 8
		for n >= uint(width) {
			n -= uint(width)
			out.WriteByte(radixDigits[(acc>>n)&(1<<width-1)])
		}
		acc &= 1<<n - 1
	}
	if n > 0 {
		out.WriteByte(radixDigits[(acc<<(uint(width)-n))&(1<<width-1)])
	}
	return out.String()
}

// decodeBits is the inverse of encodeBits. Letters are
// case-insensitive; trailing bits that do not fill a byte are dropped.
func decodeBits(s string, width int) ([]byte, error) {
	var out []byte
	var acc, n uint
	for i := 0; i < len(s); i++ {
		d := strings.IndexByte(radixDigits, lowerASCII(s[i]))
		if d < 0 || d >= 1<<width {
			return nil, fmt.Errorf("CAST: invalid character %q for BASE%d", s[i], 1<<width)
		}
		acc = acc<<uint(width) | uint(d)
		n += uint(width)
		if n >= 8 {
			n -= 8
			out = append(out, byte(acc>>n))
			acc &= 1<<n - 1
		}
	}
	if out == nil {
		out = []byte{}
	}
	return out, nil
}

func lowerASCII(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + 'a' - 'A'
	}
	return c
}
