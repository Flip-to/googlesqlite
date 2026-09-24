package googlesqlite_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

// TestTimestampLiteralUTC pins zone-dependent conversions of literals
// to BigQuery's default time zone, UTC. The analyzer constant-folds
// literal casts and literal coercions in its own default zone
// (America/Los_Angeles) and go-googlesql cannot be set to UTC, so the
// driver re-evaluates those folded values in UTC
// (docs/decisions/analyzer-default-time-zone.md).
//
// Expected values follow the BigQuery reference rules that a TIMESTAMP
// string without a zone is UTC and that TIMESTAMP to DATE / DATETIME /
// TIME / STRING conversions use UTC unless a zone is given
// (docs/third_party/googlesql-docs/conversion_rules.md,
// data-types.md "Time zones", timestamp_functions.md), and every
// expected value was checked on real BigQuery on 2026-09-24.
//
// Every case runs in two forms: with literal inputs (folded by the
// analyzer) and with the inputs read from UNNEST (not folded, evaluated
// at runtime), and both must give the same text.
func TestTimestampLiteralUTC(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("googlesqlite", ":memory:?_test=timestamp_literal_utc")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cases := []struct {
		name string
		// expr uses $1, $2 for inputs.
		expr   string
		inputs []string
		want   string
	}{
		// TIMESTAMP literals and string to TIMESTAMP.
		{"ts_literal_naive", `$1`, []string{`TIMESTAMP '2024-01-01 10:00:00'`}, "2024-01-01 10:00:00+00"},
		{"ts_literal_offset", `$1`, []string{`TIMESTAMP '2024-01-01 10:00:00+05'`}, "2024-01-01 05:00:00+00"},
		{"ts_literal_zone_name", `$1`, []string{`TIMESTAMP '2024-01-01 10:00:00 America/New_York'`}, "2024-01-01 15:00:00+00"},
		{"cast_string_to_ts", `CAST($1 AS TIMESTAMP)`, []string{`'2024-01-01 10:00:00'`}, "2024-01-01 10:00:00+00"},
		{"cast_string_offset_to_ts", `CAST($1 AS TIMESTAMP)`, []string{`'2024-01-01 10:00:00+05'`}, "2024-01-01 05:00:00+00"},
		{"cast_string_zone_to_ts", `CAST($1 AS TIMESTAMP)`, []string{`'2024-01-01 10:00:00 America/New_York'`}, "2024-01-01 15:00:00+00"},
		{"cast_date_string_to_ts", `SAFE_CAST($1 AS TIMESTAMP)`, []string{`'2024-01-01'`}, "2024-01-01 00:00:00+00"},
		{"cast_double_quoted_to_ts", `CAST($1 AS TIMESTAMP)`, []string{`"2024-06-30 23:59:59.5"`}, "2024-06-30 23:59:59.500+00"},
		// TIMESTAMP to other types.
		{"ts_to_date", `CAST($1 AS DATE)`, []string{`TIMESTAMP '2024-01-01 03:00:00+00'`}, "2024-01-01"},
		{"ts_naive_to_date", `CAST($1 AS DATE)`, []string{`TIMESTAMP '2024-01-01 03:00:00'`}, "2024-01-01"},
		{"ts_to_string", `CAST($1 AS STRING)`, []string{`TIMESTAMP '2024-01-01 03:00:00'`}, "2024-01-01 03:00:00+00"},
		{"ts_to_datetime", `CAST(CAST($1 AS DATETIME) AS STRING)`, []string{`TIMESTAMP '2024-01-01 03:00:00'`}, "2024-01-01 03:00:00"},
		{"ts_to_time", `CAST($1 AS TIME)`, []string{`TIMESTAMP '2024-01-01 03:00:00'`}, "03:00:00"},
		{"nested_string_ts_datetime", `CAST(CAST(CAST($1 AS TIMESTAMP) AS DATETIME) AS STRING)`, []string{`'2024-01-01 03:00:00'`}, "2024-01-01 03:00:00"},
		{"nested_ts_date_string", `CAST(CAST($1 AS DATE) AS STRING)`, []string{`TIMESTAMP '2024-01-01 03:00:00'`}, "2024-01-01"},
		{"date_to_ts", `CAST($1 AS TIMESTAMP)`, []string{`DATE '2024-01-01'`}, "2024-01-01 00:00:00+00"},
		{"datetime_to_ts", `CAST($1 AS TIMESTAMP)`, []string{`DATETIME '2024-01-01 01:02:03'`}, "2024-01-01 01:02:03+00"},
		// DST boundaries of America/Los_Angeles must not matter in UTC.
		{"dst_gap_string_to_ts", `CAST($1 AS TIMESTAMP)`, []string{`'2024-03-10 02:30:00'`}, "2024-03-10 02:30:00+00"},
		{"dst_overlap_string_to_ts", `CAST($1 AS TIMESTAMP)`, []string{`'2024-11-03 01:30:00'`}, "2024-11-03 01:30:00+00"},
		{"dst_gap_datetime_to_ts", `CAST($1 AS TIMESTAMP)`, []string{`DATETIME '2024-03-10 02:30:00'`}, "2024-03-10 02:30:00+00"},
		{"dst_ts_to_datetime", `CAST(CAST($1 AS DATETIME) AS STRING)`, []string{`TIMESTAMP '2024-03-10 10:30:00'`}, "2024-03-10 10:30:00"},
		{"dst_ts_to_date_near_midnight", `CAST($1 AS DATE)`, []string{`TIMESTAMP '2024-11-03 07:30:00'`}, "2024-11-03"},
		// Implicit coercion of a string literal to TIMESTAMP.
		{"coerce_equal", `TIMESTAMP '2024-01-01 10:00:00' = $1`, []string{`'2024-01-01 10:00:00'`}, "true"},
		{"coerce_less", `$1 < TIMESTAMP '2024-01-01 10:00:00'`, []string{`'2024-01-01 10:00:00'`}, "false"},
		{"coerce_timestamp_add", `TIMESTAMP_ADD($1, INTERVAL 1 HOUR)`, []string{`'2024-01-01 10:00:00'`}, "2024-01-01 11:00:00+00"},
		{"coerce_timestamp_diff", `TIMESTAMP_DIFF(TIMESTAMP '2024-01-02 00:00:00', $1, HOUR)`, []string{`'2024-01-01 00:00:00'`}, "24"},
		{"coerce_format_timestamp", `FORMAT_TIMESTAMP('%F %T', $1)`, []string{`'2024-01-01 03:00:00'`}, "2024-01-01 03:00:00"},
		// Timestamp functions over literals (never folded).
		{"timestamp_sub", `TIMESTAMP_SUB($1, INTERVAL 1 HOUR)`, []string{`TIMESTAMP '2024-01-01 00:30:00'`}, "2023-12-31 23:30:00+00"},
		{"timestamp_trunc_day", `TIMESTAMP_TRUNC($1, DAY)`, []string{`TIMESTAMP '2024-01-01 03:00:00'`}, "2024-01-01 00:00:00+00"},
		{"extract_hour", `EXTRACT(HOUR FROM $1)`, []string{`TIMESTAMP '2024-01-01 03:00:00'`}, "3"},
		{"extract_date", `EXTRACT(DATE FROM $1)`, []string{`TIMESTAMP '2024-01-01 03:00:00'`}, "2024-01-01"},
		{"format_timestamp", `FORMAT_TIMESTAMP('%Y-%m-%d %H:%M:%S', $1)`, []string{`TIMESTAMP '2024-01-01 03:00:00'`}, "2024-01-01 03:00:00"},
		{"date_of_ts", `DATE($1)`, []string{`TIMESTAMP '2024-01-01 03:00:00'`}, "2024-01-01"},
		{"string_of_ts", `STRING($1)`, []string{`TIMESTAMP '2024-01-01 03:00:00'`}, "2024-01-01 03:00:00+00"},
		{"string_of_ts_zone", `STRING($1, 'America/Los_Angeles')`, []string{`TIMESTAMP '2024-01-01 03:00:00'`}, "2023-12-31 19:00:00-08"},
		// Bare dates: `-01` at the end is not an offset.
		{"ts_literal_date_only", `$1`, []string{`TIMESTAMP '2020-01-01'`}, "2020-01-01 00:00:00+00"},
		{"cast_date_only_string_to_ts", `CAST($1 AS TIMESTAMP)`, []string{`'2020-01-01'`}, "2020-01-01 00:00:00+00"},
		// RANGE<TIMESTAMP> bounds: zone-less read as UTC, explicit
		// offsets kept, also inside STRUCT and ARRAY literals.
		{"range_date_only_start", `RANGE_START($1)`, []string{`RANGE<TIMESTAMP> '[2020-01-01, 2020-01-02)'`}, "2020-01-01 00:00:00+00"},
		{"range_date_only_end", `RANGE_END($1)`, []string{`RANGE<TIMESTAMP> '[2020-01-01, 2020-01-02)'`}, "2020-01-02 00:00:00+00"},
		{"range_offset_bound_kept", `RANGE_START($1)`, []string{`RANGE<TIMESTAMP> '[2020-01-01 12:00:00.000005+01, 2020-01-02)'`}, "2020-01-01 11:00:00.000005+00"},
		{"range_unbounded_end", `RANGE_END($1) IS NULL`, []string{`RANGE<TIMESTAMP> '[2020-01-01, UNBOUNDED)'`}, "true"},
		{"range_in_struct", `RANGE_START($1.r1)`, []string{`STRUCT(RANGE<TIMESTAMP> '[2020-01-01, 2020-01-02)' AS r1, RANGE<TIMESTAMP> '[2020-01-01 12:00:00.000005+01, 2020-01-02)' AS r2)`}, "2020-01-01 00:00:00+00"},
		{"range_in_struct_offset", `RANGE_START($1.r2)`, []string{`STRUCT(RANGE<TIMESTAMP> '[2020-01-01, 2020-01-02)' AS r1, RANGE<TIMESTAMP> '[2020-01-01 12:00:00.000005+01, 2020-01-02)' AS r2)`}, "2020-01-01 11:00:00.000005+00"},
		{"range_in_array", `RANGE_START($1[OFFSET(0)])`, []string{`[RANGE<TIMESTAMP> '[2020-01-01, 2020-01-02)']`}, "2020-01-01 00:00:00+00"},
		{"range_not_distinct_utc", `$1 IS DISTINCT FROM $2`, []string{`RANGE<TIMESTAMP> '[2020-01-01, 2020-01-02)'`, `RANGE<TIMESTAMP> '[2020-01-01 00:00:00.000000+00, 2020-01-02)'`}, "false"},
		{"range_equal_utc", `$1 = $2`, []string{`RANGE<TIMESTAMP> '[2020-01-01, 2020-01-02)'`, `RANGE<TIMESTAMP> '[2020-01-01 00:00:00.000000+00, 2020-01-02)'`}, "true"},
		{"range_distinct_offset", `$1 IS DISTINCT FROM $2`, []string{`RANGE<TIMESTAMP> '[2020-01-01, 2020-01-02)'`, `RANGE<TIMESTAMP> '[2020-01-01 00:00:00.000000+02, 2020-01-02)'`}, "true"},
		{"range_offset_self_equal", `$1 = $2`, []string{`RANGE<TIMESTAMP> '[2020-01-01 12:00:00.000005+01, 2020-01-02)'`, `RANGE<TIMESTAMP> '[2020-01-01 12:00:00.000005+01, 2020-01-02)'`}, "true"},
		// CAST(DATE AS DATETIME) folds to 1970-01-01 in the analyzer
		// (flipto-dbt probe cast_as_datetime-2057.6, BigQuery answer).
		{"date_to_datetime", `CAST(CAST($1 AS DATETIME) AS STRING)`, []string{`DATE '2024-02-29'`}, "2024-02-29 00:00:00"},
		{"date_to_datetime_in_struct", `CAST($1.d AS STRING)`, []string{`STRUCT(CAST(DATE '2024-02-29' AS DATETIME) AS d)`}, "2024-02-29 00:00:00"},
		// Composite literals.
		{"range_ts_literal", `RANGE_START($1)`, []string{`RANGE<TIMESTAMP> '[2024-01-01 10:00:00, 2024-01-02 10:00:00)'`}, "2024-01-01 10:00:00+00"},
		{"array_ts_coerced", `$1[OFFSET(0)]`, []string{`ARRAY<TIMESTAMP>['2024-01-01 10:00:00']`}, "2024-01-01 10:00:00+00"},
		{"struct_ts_coerced", `$1.t`, []string{`STRUCT<t TIMESTAMP>('2024-01-01 10:00:00')`}, "2024-01-01 10:00:00+00"},
		{"struct_cast", `$1.a`, []string{`CAST(('2024-01-01 10:00:00', 1) AS STRUCT<a TIMESTAMP, b INT64>)`}, "2024-01-01 10:00:00+00"},
	}
	for _, c := range cases {
		t.Run(c.name+"/literal", func(t *testing.T) {
			expr := c.expr
			for i, in := range c.inputs {
				expr = strings.ReplaceAll(expr, "$"+string(rune('1'+i)), in)
			}
			checkUTCText(t, db, `SELECT FORMAT('%t', `+expr+`)`, c.want)
		})
		// coerce_ cases are implicit coercions of a STRING literal; a
		// STRING column does not coerce to TIMESTAMP.
		if strings.HasPrefix(c.name, "coerce_") {
			continue
		}
		t.Run(c.name+"/column", func(t *testing.T) {
			expr := c.expr
			fields := make([]string, len(c.inputs))
			for i, in := range c.inputs {
				name := "c" + string(rune('1'+i))
				expr = strings.ReplaceAll(expr, "$"+string(rune('1'+i)), "s."+name)
				fields[i] = in + " AS " + name
			}
			checkUTCText(t, db, `SELECT FORMAT('%t', `+expr+`) FROM UNNEST([STRUCT(`+strings.Join(fields, ", ")+`)]) s`, c.want)
		})
	}
}

// TestTimestampLiteralUTCMixedPrecision checks that folding stays exact
// for NUMERIC / BIGNUMERIC / FLOAT64 literals in the same statement as
// zone-dependent TIMESTAMP folds.
func TestTimestampLiteralUTCMixedPrecision(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("googlesqlite", ":memory:?_test=timestamp_literal_utc_mixed")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	query := `SELECT
		FORMAT('%t', CAST('2024-01-01 10:00:00' AS TIMESTAMP)),
		CAST(CAST(1.123456789012345678 AS NUMERIC) AS STRING),
		CAST(CAST('1.123456789012345678901234567890123456' AS BIGNUMERIC) AS STRING),
		CAST(CAST(TIMESTAMP '2024-01-01 03:00:00' AS DATE) AS STRING),
		CAST(1.5e300 AS STRING),
		FORMAT('%t', ARRAY<TIMESTAMP>['2024-01-01 10:00:00'][OFFSET(0)]),
		CAST(CAST(0.1 AS FLOAT64) AS STRING)`
	want := []string{
		"2024-01-01 10:00:00+00",
		// conversion_functions.md: a NUMERIC cast rounds half away
		// from zero to 9 fractional digits.
		"1.123456789",
		"1.123456789012345678901234567890123456",
		"2024-01-01",
		"1.5e+300",
		"2024-01-01 10:00:00+00",
		"0.1",
	}
	got := make([]string, len(want))
	ptrs := make([]any, len(want))
	for i := range got {
		ptrs[i] = &got[i]
	}
	if err := db.QueryRowContext(context.Background(), query).Scan(ptrs...); err != nil {
		t.Fatal(err)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("column %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func checkUTCText(t *testing.T, db *sql.DB, query, want string) {
	t.Helper()
	var got sql.NullString
	if err := db.QueryRowContext(context.Background(), query).Scan(&got); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	if !got.Valid || got.String != want {
		t.Errorf("%s = %v, want %q", query, got, want)
	}
}
