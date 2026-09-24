package value

import (
	"encoding/json"
	"fmt"
	"io"
	"math/big"
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
	return encodeJSON(v, false)
}

// EncodeJSONString is EncodeJSON for TO_JSON_STRING, which also quotes
// a NUMERIC / BIGNUMERIC unless it is an integer in [-2^53, 2^53]
// (json_functions.md, JSON encodings; flipto-dbt probe
// to_json_string-5209.19).
func EncodeJSONString(v Value) (string, error) {
	return encodeJSON(v, true)
}

func encodeJSON(v Value, toString bool) (string, error) {
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
	case JsonValue:
		if toString {
			// Numbers and strings come back in BigQuery's canonical
			// form: 2.50 is 2.5 and "it's" is "it's" (flipto-dbt
			// probes safe_parse_json-2825.1 and .2).
			if s, err := canonicalJSON(string(vv)); err == nil {
				return s, nil
			}
		}
	case *NumericValue:
		if toString {
			s := vv.toString()
			if !vv.Rat.IsInt() || vv.Rat.Num().CmpAbs(maxExactJSONInt) > 0 {
				return strconv.Quote(s), nil
			}
			return s, nil
		}
	case *ArrayValue:
		elems := make([]string, 0, len(vv.Values))
		for _, e := range vv.Values {
			s, err := encodeJSON(e, toString)
			if err != nil {
				return "", err
			}
			elems = append(elems, s)
		}
		return fmt.Sprintf("[%s]", strings.Join(elems, ",")), nil
	case *StructValue:
		fields := make([]string, 0, len(vv.Keys))
		for i, key := range vv.Keys {
			s, err := encodeJSON(vv.Values[i], toString)
			if err != nil {
				return "", err
			}
			fields = append(fields, fmt.Sprintf("%s:%s", jsonQuote(key), s))
		}
		return fmt.Sprintf("{%s}", strings.Join(fields, ",")), nil
	}
	return v.ToJSON()
}

var maxExactJSONInt = big.NewInt(1 << 53)

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

// canonicalJSON re-renders a JSON document keeping member order:
// strings through jsonQuote, integers as written, other numbers as
// FLOAT64.
func canonicalJSON(raw string) (string, error) {
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	var b strings.Builder
	if err := canonicalJSONValue(dec, &b); err != nil {
		return "", err
	}
	if _, err := dec.Token(); err != io.EOF {
		return "", fmt.Errorf("trailing data in JSON")
	}
	return b.String(), nil
}

func canonicalJSONValue(dec *json.Decoder, b *strings.Builder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			b.WriteByte('{')
			for i := 0; dec.More(); i++ {
				if i > 0 {
					b.WriteByte(',')
				}
				k, err := dec.Token()
				if err != nil {
					return err
				}
				key, ok := k.(string)
				if !ok {
					return fmt.Errorf("invalid JSON object key")
				}
				b.WriteString(jsonQuote(key))
				b.WriteByte(':')
				if err := canonicalJSONValue(dec, b); err != nil {
					return err
				}
			}
			b.WriteByte('}')
		case '[':
			b.WriteByte('[')
			for i := 0; dec.More(); i++ {
				if i > 0 {
					b.WriteByte(',')
				}
				if err := canonicalJSONValue(dec, b); err != nil {
					return err
				}
			}
			b.WriteByte(']')
		}
		if _, err := dec.Token(); err != nil { // closing delimiter
			return err
		}
	case string:
		b.WriteString(jsonQuote(t))
	case json.Number:
		if _, err := strconv.ParseInt(string(t), 10, 64); err == nil {
			b.WriteString(string(t))
			break
		}
		if _, err := strconv.ParseUint(string(t), 10, 64); err == nil {
			b.WriteString(string(t))
			break
		}
		f, err := strconv.ParseFloat(string(t), 64)
		if err != nil {
			b.WriteString(string(t))
			break
		}
		b.WriteString(formatFloat(f))
	case bool:
		b.WriteString(strconv.FormatBool(t))
	case nil:
		b.WriteString("null")
	}
	return nil
}
