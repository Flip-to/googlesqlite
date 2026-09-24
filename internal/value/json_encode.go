package value

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// EncodeJSON renders v with BigQuery's JSON encodings, for TO_JSON and
// TO_JSON_STRING. It differs from ToJSON only where ToJSON's text is also
// what the driver scans: DATE, DATETIME, TIME and TIMESTAMP are JSON strings
// ("2017-03-06"), at any depth inside an ARRAY or STRUCT.
// https://cloud.google.com/bigquery/docs/reference/standard-sql/json_functions#json_encodings
func EncodeJSON(v Value) (string, error) {
	switch vv := v.(type) {
	case nil:
		return "null", nil
	case IntValue:
		// INT64 values outside [-2^53, 2^53] are quoted so JSON readers
		// that use doubles do not lose precision (json_functions.md
		// TO_JSON_STRING).
		const maxExact = 1 << 53
		if vv > maxExact || vv < -maxExact {
			return strconv.Quote(strconv.FormatInt(int64(vv), 10)), nil
		}
		return strconv.FormatInt(int64(vv), 10), nil
	case DatetimeValue:
		// "2024-01-01T12:34:06.500": fraction in groups of three digits.
		t := time.Time(vv)
		return strconv.Quote(t.Format("2006-01-02T15:04:05") + fractionInGroups(t)), nil
	case DateValue, TimeValue, TimestampValue:
		s, err := vv.ToString()
		if err != nil {
			return "", err
		}
		return strconv.Quote(s), nil
	case StringValue:
		return jsonQuote(string(vv)), nil
	case *ArrayValue:
		elems := make([]string, 0, len(vv.Values))
		for _, e := range vv.Values {
			s, err := EncodeJSON(e)
			if err != nil {
				return "", err
			}
			elems = append(elems, s)
		}
		return fmt.Sprintf("[%s]", strings.Join(elems, ",")), nil
	case *StructValue:
		fields := make([]string, 0, len(vv.Keys))
		for i, key := range vv.Keys {
			s, err := EncodeJSON(vv.Values[i])
			if err != nil {
				return "", err
			}
			fields = append(fields, fmt.Sprintf("%s:%s", jsonQuote(key), s))
		}
		return fmt.Sprintf("{%s}", strings.Join(fields, ",")), nil
	}
	return v.ToJSON()
}

// jsonQuote renders s as a JSON string literal: `"` and `\` are
// backslash-escaped, \b \f \n \r \t use their short forms and other
// control characters use \u00XX (strings.test,
// to_json_string_with_escaped_field_names). strconv.Quote emits
// \x00-style escapes, which are not valid JSON.
func jsonQuote(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, `\u%04x`, r)
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
