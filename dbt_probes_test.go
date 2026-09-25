package googlesqlite_test

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strings"
	"testing"
)

// TestDBTDifferentialProbes replays the one-statement repros that the
// Flip-to/flipto-dbt differential harness (PR 365,
// scripts/emulator_diff/minimal.py) ran against BigQuery and the
// emulator. Every want is the BigQuery answer recorded in
// analyses/emulator_differential_results.md; the ID in each name is its
// row there (S = silent, L = loud).
//
// Scalar probes run twice, as the harness did: once with literal
// arguments, which the analyzer may constant-fold, and once with the
// arguments read from a STRUCT field, which forces the driver's own
// runtime. Results that are not plain scalars are rendered through
// CAST / TO_HEX / ARRAY_TO_STRING so the comparison is exact.
func TestDBTDifferentialProbes(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("googlesqlite", ":memory:?_test=dbt_probes")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	type scalar struct {
		name string
		expr string   // uses $1, $2, ... for the arguments
		args []string // SQL literals
		want any      // nil means SQL NULL; errPrefix wants an error
	}
	nan := math.NaN()
	scalars := []scalar{
		// S1: default time zone is UTC.
		{"S1_cast_date", `CAST(CAST($1 AS DATE) AS STRING)`, []string{`TIMESTAMP '2024-01-01 03:00:00+00'`}, "2024-01-01"},
		{"S1_cast_string", `CAST($1 AS STRING)`, []string{`TIMESTAMP '2024-01-01 00:00:00+00'`}, "2024-01-01 00:00:00+00"},
		{"S1_cast_datetime", `CAST(CAST($1 AS DATETIME) AS STRING)`, []string{`TIMESTAMP '2024-01-01 03:00:00+00'`}, "2024-01-01 03:00:00"},
		{"S1_string_to_ts", `CAST(CAST($1 AS TIMESTAMP) AS STRING)`, []string{`'2024-01-01 10:00:00'`}, "2024-01-01 10:00:00+00"},
		{"S1_timestamp_diff_day", `TIMESTAMP_DIFF($1, $2, DAY)`, []string{`TIMESTAMP '2024-01-02 03:00:00+00'`, `TIMESTAMP '2024-01-01 09:00:00+00'`}, int64(0)},
		{"S1_extract_week_ts", `EXTRACT(WEEK FROM $1)`, []string{`TIMESTAMP '2017-01-02 12:00:00+00'`}, int64(1)},
		// S2: a time in a DST gap resolves forward.
		{"S2_dst_gap", `CAST(TIMESTAMP($1, 'America/New_York') AS STRING)`, []string{`'2024-03-10 02:30:00'`}, "2024-03-10 07:30:00+00"},
		// S3, S4, S5: three-valued logic.
		{"S3_in_null", `$1 IN (1, NULL)`, []string{`2`}, nil},
		{"S3_not_in_null", `$1 NOT IN (1, NULL)`, []string{`2`}, nil},
		{"S4_is_not_true_null", `$1 IS NOT TRUE`, []string{`CAST(NULL AS BOOL)`}, true},
		{"S5_between_null_bound", `$1 BETWEEN 5 AND CAST(NULL AS INT64)`, []string{`1`}, false},
		// S6, S7: FORMAT %T / %t.
		{"S6_format_t_null", `FORMAT('%T', $1)`, []string{`CAST(NULL AS INT64)`}, "NULL"},
		{"S7_format_t_float", `FORMAT('%T', $1)`, []string{`3.0`}, "3.0"},
		{"S7_format_t_struct", `FORMAT('%T', STRUCT('a' AS a, $1 AS b))`, []string{`3.0`}, `("a", 3.0)`},
		{"S7_format_small_t_float", `FORMAT('%t', $1)`, []string{`1.0`}, "1.0"},
		// S8, S9: TO_JSON_STRING.
		{"S8_to_json_int64_big", `TO_JSON_STRING($1)`, []string{`9223372036854775807`}, `"9223372036854775807"`},
		{"S8_to_json_int64_small", `TO_JSON_STRING($1)`, []string{`9007199254740992`}, `9007199254740992`},
		{"S8_to_json_float", `TO_JSON_STRING($1)`, []string{`123456789.125`}, `123456789.125`},
		{"S8_to_json_pretty", `TO_JSON_STRING(STRUCT($1 AS k), true)`, []string{`1`}, "{\n  \"k\": 1\n}"},
		{"S9_parse_json_roundtrip", `TO_JSON_STRING(SAFE.PARSE_JSON($1))`, []string{`'{"a": 1}'`}, `{"a":1}`},
		// S10, S11, L9: JSON_VALUE / JSON_QUERY.
		{"S10_json_value_float", `JSON_VALUE($1, '$.a')`, []string{`'{"a": 1.0}'`}, "1.0"},
		{"S11_json_query_json_null", `JSON_QUERY($1, '$.a') IS NULL`, []string{`JSON '{"a": null}'`}, false},
		{"L9_json_value_bad_json", `JSON_VALUE($1, '$.a')`, []string{`'not json'`}, nil},
		// S12: CAST ... FORMAT for dates.
		{"S12_cast_format_day", `CAST($1 AS STRING FORMAT 'DAY')`, []string{`DATE '2024-01-03'`}, "WEDNESDAY"},
		// S13, S14, S15: casts.
		{"S13_cast_nan_string", `CAST($1 AS STRING)`, []string{`CAST('NaN' AS FLOAT64)`}, "nan"},
		{"S14_cast_float_int_round", `CAST($1 AS INT64)`, []string{`2.5`}, int64(3)},
		{"S14_cast_float_int_round_neg", `CAST($1 AS INT64)`, []string{`-2.5`}, int64(-3)},
		{"S15_safe_cast_trim_float", `SAFE_CAST($1 AS FLOAT64)`, []string{`' 2.5 '`}, 2.5},
		{"S15_safe_cast_trim_numeric", `CAST(SAFE_CAST($1 AS NUMERIC) AS STRING)`, []string{`' 2.5 '`}, "2.5"},
		{"S15_safe_cast_trim_int", `SAFE_CAST($1 AS INT64)`, []string{`' 12 '`}, int64(12)},
		{"S15_safe_cast_numeric_nan", `SAFE_CAST($1 AS NUMERIC)`, []string{`'NaN'`}, nil},
		{"S15_safe_cast_bool_one", `SAFE_CAST($1 AS BOOL)`, []string{`'1'`}, nil},
		{"S15_safe_cast_date_loose", `CAST(SAFE_CAST($1 AS DATE) AS STRING)`, []string{`'2024-2-9'`}, "2024-02-09"},
		{"S15_safe_cast_date_with_time", `SAFE_CAST($1 AS DATE)`, []string{`'2024-02-29 10:00:00'`}, nil},
		{"S15_safe_cast_ts_zone_name", `CAST(SAFE_CAST($1 AS TIMESTAMP) AS STRING)`, []string{`'2024-01-01 10:00:00 America/New_York'`}, "2024-01-01 15:00:00+00"},
		// S16, S17, S18: integer and NUMERIC arithmetic.
		{"S16_shift_right_negative", `$1 >> 1`, []string{`-7`}, int64(9223372036854775804)},
		{"S17_abs_int64_max", `ABS($1)`, []string{`9223372036854775807`}, int64(9223372036854775807)},
		{"S18_floor_numeric_big", `CAST(FLOOR($1) AS STRING)`, []string{`NUMERIC '99999999999999999999999999999.9'`}, "99999999999999999999999999999"},
		{"S18_mod_numeric", `CAST(MOD($1, $2) AS STRING)`, []string{`NUMERIC '-1.005'`, `NUMERIC '0.000000001'`}, "0"},
		// S19: NaN in GREATEST.
		{"S19_greatest_nan", `GREATEST(1.0, $1)`, []string{`CAST('NaN' AS FLOAT64)`}, nan},
		// S21, S22, S23, S24: dates.
		{"S21_safe_date_overflow", `SAFE.DATE($1, 13, 1)`, []string{`2024`}, nil},
		{"S22_month_end_interval", `CAST($1 + INTERVAL 1 MONTH AS STRING)`, []string{`DATE '2024-01-31'`}, "2024-02-29 00:00:00"},
		{"S23_extract_week_date", `EXTRACT(WEEK FROM $1)`, []string{`DATE '2023-12-31'`}, int64(53)},
		{"S24_datetime_diff_minute", `DATETIME_DIFF($1, $2, MINUTE)`, []string{`DATETIME '2024-01-01 00:01:00'`, `DATETIME '2023-12-31 23:59:59.999999'`}, int64(2)},
		// S26 - S32: strings.
		{"S26_substr_unicode", `SUBSTR($1, 1)`, []string{`'Ünïcödé ß'`}, "Ünïcödé ß"},
		{"S27_upper_sharp_s", `UPPER($1)`, []string{`'ß'`}, "SS"},
		{"S28_initcap_newline", `INITCAP($1)`, []string{`'a\nb'`}, "A\nB"},
		{"S29_replace_empty", `REPLACE($1, '', 'x')`, []string{`'abc'`}, "abc"},
		{"S30_regexp_optional_group", `REGEXP_EXTRACT($1, '(z)?b')`, []string{`'ab'`}, nil},
		{"S31_contains_substr_nfkc", `CONTAINS_SUBSTR($1, 'IX')`, []string{`'Ⅸ'`}, true},
		{"S32_array_to_string_bytes", `TO_HEX(ARRAY_TO_STRING([$1, b'b'], b'--'))`, []string{`b'a'`}, "612d2d62"},
		// S34: HLL_COUNT.EXTRACT of NULL.
		{"S34_hll_extract_null", `HLL_COUNT.EXTRACT($1)`, []string{`CAST(NULL AS BYTES)`}, int64(0)},
		// L1, L2, L7, L8, L10.
		{"L1_pow_negated_column", `POW(2, -$1 / 7.0)`, []string{`7.0`}, 0.5},
		{"L2_nullif_null_arg", `NULLIF($1, CAST(NULL AS STRING))`, []string{`'a'`}, "a"},
		{"L7_format_width_minus", `FORMAT('%-5d', $1)`, []string{`7`}, "7    "},
		{"L8_format_hex_negative", `FORMAT('%x', $1)`, []string{`-7`}, "-7"},
		{"L10_extract_week_monday", `EXTRACT(WEEK(MONDAY) FROM $1)`, []string{`DATE '2017-11-05'`}, int64(44)},
		// L11: a zero step is an error.
		{"L11_generate_array_step0", `GENERATE_ARRAY(1, $1, 0)`, []string{`0`}, errPrefix("sequence step cannot be 0")},
		{"L11_generate_date_array_step0", `GENERATE_DATE_ARRAY($1, DATE '2017-01-01', INTERVAL 0 DAY)`, []string{`DATE '2016-01-01'`}, errPrefix("sequence step cannot be 0")},
		// L3: ARRAY_CONCAT with a NULL array is NULL.
		{"L3_array_concat_null", `ARRAY_CONCAT(CAST([] AS ARRAY<INT64>), $1)`, []string{`CAST(NULL AS ARRAY<INT64>)`}, nil},
	}

	type whole struct {
		name  string
		query string
		want  []any
	}
	wholes := []whole{
		// S17: overflow is an error; AVG is exact.
		{"S17_sum_int_overflow", `SELECT SUM(x) FROM UNNEST([9223372036854775807, 1]) AS x`, []any{errPrefix("int64 overflow")}},
		{"S17_avg_int_overflow", `SELECT AVG(x) FROM UNNEST([9223372036854775807, 1]) AS x`, []any{4.611686018427388e+18}},
		// S19: NaN in MIN / MAX.
		{"S19_max_min_nan", `SELECT MAX(x), MIN(x) FROM UNNEST([1.0, CAST('NaN' AS FLOAT64)]) AS x`, []any{nan, nan}},
		// S20: STDDEV of one row.
		{"S20_stddev_single", `SELECT STDDEV(x) FROM UNNEST([1]) AS x`, []any{nil}},
		// S22: GENERATE_DATE_ARRAY steps month by month from the clamped date.
		{"S22_date_array_month_step", `SELECT ARRAY_TO_STRING(ARRAY(SELECT CAST(d AS STRING) FROM UNNEST(GENERATE_DATE_ARRAY(DATE '2024-01-31', DATE '2024-04-30', INTERVAL 1 MONTH)) d WITH OFFSET o ORDER BY o), ',')`, []any{"2024-01-31,2024-02-29,2024-03-29,2024-04-29"}},
		// S25: CURRENT_TIMESTAMP is stable within a query.
		{"S25_current_ts_stable", `SELECT CURRENT_TIMESTAMP() = CURRENT_TIMESTAMP()`, []any{true}},
		// S33: APPROX_QUANTILES is ordered.
		{"S33_approx_quantiles_order", `SELECT ARRAY_TO_STRING(ARRAY(SELECT CAST(q AS STRING) FROM UNNEST(qs) q WITH OFFSET o ORDER BY o), ',') FROM (SELECT APPROX_QUANTILES(x, 2) qs FROM UNNEST([1, 2, -7]) AS x)`, []any{"-7,1,2"}},
		// L6: APPROX_QUANTILES ignores NULL inputs.
		{"L6_approx_quantiles_null_input", `SELECT ARRAY_TO_STRING(ARRAY(SELECT CAST(q AS STRING) FROM UNNEST(qs) q WITH OFFSET o ORDER BY o), ',') FROM (SELECT APPROX_QUANTILES(x, 2) qs FROM UNNEST([1.0, NULL]) AS x)`, []any{"1,1,1"}},
		// S35: FIRST_VALUE / LAST_VALUE IGNORE NULLS.
		{"S35_first_value_ignore_nulls", `SELECT STRING_AGG(v, ',' ORDER BY x) FROM (SELECT x, FIRST_VALUE(y IGNORE NULLS) OVER (ORDER BY x ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING) v FROM UNNEST([STRUCT(1 AS x, CAST(NULL AS STRING) AS y), (2, 'b')]))`, []any{"b,b"}},
		{"S35_last_value_ignore_nulls", `SELECT STRING_AGG(v, ',' ORDER BY x) FROM (SELECT x, LAST_VALUE(y IGNORE NULLS) OVER (ORDER BY x ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING) v FROM UNNEST([STRUCT(1 AS x, 'a' AS y), (2, CAST(NULL AS STRING))]))`, []any{"a,a"}},
		// S36: anonymous STRUCT fields keep both values.
		{"S36_anon_struct_fields", `SELECT FORMAT('%T', ARRAY(SELECT AS STRUCT 1, 2))`, []any{"[(1, 2)]"}},
	}

	check := func(t *testing.T, query string, want []any) {
		t.Helper()
		rows, err := db.QueryContext(context.Background(), query)
		if err == nil {
			defer rows.Close()
		}
		var got []any
		if err == nil && rows.Next() {
			got = make([]any, len(want))
			ptrs := make([]any, len(got))
			for i := range got {
				ptrs[i] = &got[i]
			}
			err = rows.Scan(ptrs...)
		}
		if err == nil && rows != nil {
			err = rows.Err()
		}
		if p, ok := want[0].(errPrefix); ok {
			if err == nil || !strings.Contains(err.Error(), string(p)) {
				t.Errorf("%s: err = %v, want error containing %q", query, err, string(p))
			}
			return
		}
		if err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		if got == nil {
			t.Fatalf("%s: no rows", query)
		}
		for i := range want {
			if !sameValue(got[i], want[i]) {
				t.Errorf("%s: column %d = %#v, want %#v", query, i+1, got[i], want[i])
			}
		}
	}

	for _, c := range scalars {
		direct := c.expr
		field := c.expr
		var fields []string
		for i, a := range c.args {
			ph := fmt.Sprintf("$%d", i+1)
			direct = strings.ReplaceAll(direct, ph, a)
			field = strings.ReplaceAll(field, ph, fmt.Sprintf("s.a%d", i+1))
			fields = append(fields, fmt.Sprintf("%s AS a%d", a, i+1))
		}
		c := c
		t.Run(c.name+"/literal", func(t *testing.T) {
			check(t, "SELECT "+direct, []any{c.want})
		})
		t.Run(c.name+"/column", func(t *testing.T) {
			check(t, fmt.Sprintf("SELECT %s FROM (SELECT STRUCT(%s) AS s)", field, strings.Join(fields, ", ")), []any{c.want})
		})
	}
	for _, c := range wholes {
		c := c
		t.Run(c.name, func(t *testing.T) { check(t, c.query, c.want) })
	}
}

// errPrefix marks an expected error; the test checks the message
// contains it.
type errPrefix string
