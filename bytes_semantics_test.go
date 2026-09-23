package googlesqlite_test

import (
	"context"
	"database/sql"
	"testing"
)

// TestBytesSemantics pins BYTES functions to byte semantics. The driver
// stores BYTES base64-encoded, and several functions read that storage
// text instead of the bytes: CAST(BYTES AS STRING) returned base64,
// ASCII(b"a") returned 89 ('Y' from "YQ=="), STRING_AGG joined base64
// fragments, and LIKE, TRIM, INSTR, SPLIT and REGEXP_* matched the
// wrong thing.
//
// Expected values come from the GoogleSQL compliance fixtures
// (compliance/testdata/bytes.test, strings.test, cast_function.test,
// analytic_string_aggregation.test, like_all.test) and the examples in
// docs/third_party/googlesql-docs/format-elements.md. Each query reads
// its BYTES from UNNEST so the analyzer cannot constant-fold it; TO_HEX
// shows BYTES results unambiguously.
func TestBytesSemantics(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("googlesqlite", ":memory:?_test=bytes_semantics")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cases := []struct {
		name  string
		query string
		want  any
	}{
		// strings.test format_utf8 (CAST of BYTES to STRING).
		{"cast_bytes_to_string", `SELECT CAST(x AS STRING) FROM UNNEST([b"\xe7\xb4\xaf\xe8\xa8\x88"]) x`, "累計"},
		// bytes.test function_ascii.
		{"ascii", `SELECT ASCII(x) FROM UNNEST([b"a"]) x`, int64(97)},
		{"ascii_empty", `SELECT ASCII(x) FROM UNNEST([b""]) x`, int64(0)},
		// bytes.test function_trim / function_ltrim / function_rtrim.
		{"trim_byte_set", `SELECT TO_HEX(TRIM(x, b" \xac")) FROM UNNEST([b" \xE2\x82\xAC "]) x`, "e282"},
		{"trim_empty_set", `SELECT TO_HEX(TRIM(x, b"")) FROM UNNEST([b" a b "]) x`, "2061206220"},
		{"ltrim_byte_set", `SELECT TO_HEX(LTRIM(x, b" \xe2")) FROM UNNEST([b" \xE2\x82\xAC "]) x`, "82ac20"},
		{"ltrim_empty_set", `SELECT TO_HEX(LTRIM(x, b"")) FROM UNNEST([b" a b "]) x`, "2061206220"},
		{"rtrim_byte_set", `SELECT TO_HEX(RTRIM(x, b" \xac")) FROM UNNEST([b" \xE2\x82\xAC "]) x`, "20e282"},
		// bytes.test function_instr.
		{"instr_bytes", `SELECT INSTR(x, b"\x82") FROM UNNEST([b"\xE2\x82\xAC"]) x`, int64(2)},
		{"instr_occurrence", `SELECT INSTR(x, b"\x82", 1, 2) FROM UNNEST([b"\xE2\x82\x82"]) x`, int64(3)},
		{"instr_negative", `SELECT INSTR(x, b"\x82", -1) FROM UNNEST([b"\xE2\x82\x82"]) x`, int64(3)},
		// string_functions.md INSTR: 0 when position exceeds the length.
		{"instr_position_past_end", `SELECT INSTR(x, 'a', 100) FROM UNNEST(['ab']) x`, int64(0)},
		// bytes.test split_empty_delimiter; strings.test split_empty_delimiter.
		{"split_empty_bytes", `SELECT ARRAY_LENGTH(SPLIT(x, b"")) FROM UNNEST([b""]) x`, int64(1)},
		{"split_bytes_per_byte", `SELECT TO_HEX(SPLIT(x, b"")[OFFSET(1)]) FROM UNNEST([b"\x00\x02"]) x`, "02"},
		{"split_empty_string", `SELECT ARRAY_LENGTH(SPLIT(x, "")) FROM UNNEST([""]) x`, int64(1)},
		// bytes.test function_regexp_extract / function_regexp_instr.
		{"regexp_extract_bytes", `SELECT TO_HEX(REGEXP_EXTRACT(x, b"a.c")) FROM UNNEST([b"abcabc"]) x`, "616263"},
		{"regexp_instr_bytes", `SELECT REGEXP_INSTR(x, b"a(b)c", 2, 1, 1) FROM UNNEST([b"abcabc"]) x`, int64(6)},
		{"regexp_dot_is_one_byte", `SELECT REGEXP_CONTAINS(x, b"^\xe2.\x82$") FROM UNNEST([b"\xE2\x82\x82"]) x`, true},
		// like_all.test like_all_bytes_constant_patterns.
		{"like_bytes", `SELECT x LIKE b'Valu%1' FROM UNNEST([b'Value1']) x`, true},
		// like on BYTES matches '_' against a single byte.
		{"like_bytes_underscore", `SELECT x LIKE b'_\x82\x82' FROM UNNEST([b"\xE2\x82\x82"]) x`, true},
		// analytic_string_aggregation.test string_agg_BYTES_*: BYTES in,
		// BYTES out, raw bytes joined, explicit empty delimiter kept.
		{"string_agg_bytes", `SELECT TO_HEX(STRING_AGG(x, b";" ORDER BY x)) FROM UNNEST([b"A", b"BC"]) x`, "413b4243"},
		{"string_agg_bytes_empty_delim", `SELECT TO_HEX(STRING_AGG(x, b"" ORDER BY x)) FROM UNNEST([b"A", b"BC"]) x`, "414243"},
		{"string_agg_bytes_window", `SELECT TO_HEX(STRING_AGG(x, b";") OVER (ORDER BY x ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING)) FROM UNNEST([b"A", b"BC"]) x LIMIT 1`, "413b4243"},
		{"string_agg_window_skips_null", `SELECT STRING_AGG(x, ",") OVER (ORDER BY o ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING) FROM UNNEST([STRUCT(1 AS o, 'a' AS x), STRUCT(2, CAST(NULL AS STRING))]) LIMIT 1`, "a"},
		// cast_function.test cast_bytes_to_string_all_formats and
		// format-elements.md examples.
		{"cast_format_base2", `SELECT CAST(x AS STRING FORMAT 'base2') FROM UNNEST([b'abc']) x`, "011000010110001001100011"},
		{"cast_format_base8", `SELECT CAST(x AS STRING FORMAT 'BASE8') FROM UNNEST([b'\x02\x11\x3B']) x`, "00410473"},
		{"cast_format_hex", `SELECT CAST(x AS STRING FORMAT 'HEX') FROM UNNEST([b'\x00\x01\xEF\xFF']) x`, "0001efff"},
		{"cast_format_base32", `SELECT CAST(x AS STRING FORMAT 'base32') FROM UNNEST([b'abc']) x`, "MFRGG==="},
		{"cast_format_base64m", `SELECT CAST(x AS STRING FORMAT 'BASE64M') FROM UNNEST([b'\xde\xad\xbe\xef']) x`, "3q2+7w=="},
		{"cast_format_ascii", `SELECT CAST(x AS STRING FORMAT 'ASCII') FROM UNNEST([b'\x48\x65\x6c\x6c\x6f']) x`, "Hello"},
		{"cast_format_case_insensitive", `SELECT CAST(x AS STRING FORMAT 'bAsE2') FROM UNNEST([b'abc']) x`, "011000010110001001100011"},
		{"cast_format_string_to_bytes_base8", `SELECT TO_HEX(CAST(x AS BYTES FORMAT 'base8')) FROM UNNEST(['30261143']) x`, "616263"},
		{"cast_format_string_to_bytes_base32", `SELECT TO_HEX(CAST(x AS BYTES FORMAT 'base32')) FROM UNNEST(['MFRGG===']) x`, "616263"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got any
			if err := db.QueryRowContext(context.Background(), c.query).Scan(&got); err != nil {
				t.Fatalf("%s: %v", c.query, err)
			}
			if got != c.want {
				t.Errorf("%s = %#v, want %#v", c.query, got, c.want)
			}
		})
	}

	// cast_function.test cast_bytes_to_string_format_is_null and
	// cast_string_to_bytes_safe_cast_invalid_input.
	for _, q := range []string{
		`SELECT CAST(x AS STRING FORMAT CAST(NULL AS STRING)) FROM UNNEST([b'abc']) x`,
		`SELECT SAFE_CAST(x AS BYTES FORMAT 'base2') FROM UNNEST(['123']) x`,
		`SELECT SAFE_CAST(x AS STRING FORMAT 'x' || 'y' || 'z') FROM UNNEST([b'abc']) x`,
	} {
		var got any
		if err := db.QueryRowContext(context.Background(), q).Scan(&got); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		if got != nil {
			t.Errorf("%s = %#v, want NULL", q, got)
		}
	}
}
