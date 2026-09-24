package googlesqlite_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

// TestScalarCompliance pins scalar-function semantics taken from the
// GoogleSQL compliance fixtures under compliance/testdata (file and case
// name cited per case). Values are compared through fmt.Sprint of the
// scanned columns ("NULL" for NULL); NUMERIC results are rendered with
// CAST(... AS STRING).
func TestScalarCompliance(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("googlesqlite", ":memory:?_test=scalar_compliance")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()

	// iferror.test / iserror.test / conditional_evaluation.test
	// [prepare_database] table T.
	if _, err := db.ExecContext(ctx, `CREATE TABLE T AS
SELECT 0 AS A,  0 AS B,  0.0 AS C UNION ALL
SELECT 1,      10,      100.0     UNION ALL
SELECT 2,      20,      200.0`); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name  string
		query string
		want  [][]string
	}{
		// round.test
		{"round_numeric_with_round_half_even", `SELECT CAST(ROUND(NUMERIC "2.45", 1, "ROUND_HALF_EVEN") AS STRING)`, [][]string{{"2.4"}}},
		{"round_numeric_with_round_half_away_from_zero", `SELECT CAST(ROUND(NUMERIC "2.45", 1, "ROUND_HALF_AWAY_FROM_ZERO") AS STRING)`, [][]string{{"2.5"}}},
		{"round_numeric_passing_rounding_mode_as_cast_expression", `SELECT CAST(ROUND(NUMERIC "2.45", 1, CAST(concat("ROUND_", "HALF_AWAY_FROM_ZERO") as ROUNDING_MODE)) AS STRING)`, [][]string{{"2.5"}}},
		{"round_numeric_null_rounding_mode_via_casting", `SELECT ROUND(NUMERIC "2.45", 1, CAST(NULL as ROUNDING_MODE))`, [][]string{{"NULL"}}},
		{"round_bignumeric_with_no_rounding_mode", `SELECT CAST(ROUND(BIGNUMERIC "1321232.41325", 4) AS STRING)`, [][]string{{"1321232.4133"}}},
		{"round_bignumeric_with_round_half_even", `SELECT CAST(ROUND(BIGNUMERIC "1321232.41325", 4, "ROUND_HALF_EVEN") AS STRING)`, [][]string{{"1321232.4132"}}},

		// safe_function.test
		{"csc_safe_zero_resolve_to_null", `SELECT safe.csc(0), safe.cot(0), safe.csch(0), safe.coth(0)`, [][]string{{"NULL", "NULL", "NULL", "NULL"}}},
		{"safe_ascii", `select safe.ascii("\xFF")`, [][]string{{"NULL"}}},
		{"json_safe_bool_resolve_to_null", `SELECT safe.bool(JSON '4326')`, [][]string{{"NULL"}}},
		{"safe_parse_numeric", `SELECT safe.parse_numeric(' - 1 2.34 '), safe.parse_numeric('1e100'), safe.parse_numeric('abc')`, [][]string{{"NULL", "NULL", "NULL"}}},
		{"safe_sum_double_overflow", `select safe.sum(value) from (SELECT 1.7e+308 AS value UNION ALL SELECT 1.7e+308)`, [][]string{{"NULL"}}},
		{"safe_analytic_agg_func", `select safe.sum(v) over (order by o) from unnest([9223372036854775807, 1]) v with offset o order by o`,
			[][]string{{"9223372036854775807"}, {"NULL"}}},

		// iferror.test / iserror.test / conditional_evaluation.test
		{"try_expr_has_constant_folding_error", `SELECT IFERROR(CAST('a' AS DATE), NULL), ISERROR(CAST('a' AS DATE))`, [][]string{{"NULL", "true"}}},
		{"nested_try_expr_has_constant_folding_error", `SELECT ISERROR(ISERROR(CAST('inner' AS DATE)))`, [][]string{{"false"}}},
		{"iferror_on_aggregate_expressions_error_in_try_with_literal_fallback", `SELECT IFERROR(SUM(A/0), -1) FROM T`, [][]string{{"-1"}}},
		{"aggregate_handle_expr_error_not_invoked_if_try_expr_is_good", `SELECT IFERROR(SUM(A), SUM(A/0)) FROM T`, [][]string{{"3"}}},
		{"aggregate_expr_error_implicit_grouping", `SELECT ISERROR(SUM(A/0)) FROM T`, [][]string{{"true"}}},
		{"nested_iserror_absorbs_errors", `SELECT ISERROR(IF(ISERROR(A/0), NULL, NULL)) FROM T`, [][]string{{"false"}, {"false"}, {"false"}}},
		{"safe_mode", `SELECT SAFE.ISERROR(A/0) FROM T`, [][]string{{"true"}, {"true"}, {"true"}}},
		{"aggregate_in_if", `SELECT IF(1 > 0, sum(a), sum(a/0)) FROM T`, [][]string{{"3"}}},
		{"aggregate_in_case_with_grouping_column_in_condition", `SELECT CASE WHEN b >= 0 THEN sum(a) ELSE sum(a/0) END FROM T GROUP BY b ORDER BY 1`,
			[][]string{{"0"}, {"1"}, {"2"}}},
		{"iserror_in_where_clause_handles_scalar_subquery_returning_multiple_rows", `SELECT C FROM T WHERE ISERROR(B = (SELECT 10*A FROM T)) ORDER BY C`,
			[][]string{{"0"}, {"100"}, {"200"}}},

		// strings.test
		{"strings_function_regexp_match", `SELECT REGEXP_MATCH("abc", "a|db?")`, [][]string{{"false"}}},
		{"diff_strings_and_bytes_9", `SELECT STRPOS("€", "€") = 1, STRPOS("\xE2\x82\xAC", "€") = 0, STRPOS("\xE2\x82\xAC", "\x82") = 2`, [][]string{{"true", "true", "true"}}},
		{"strings_function_lcase", `SELECT lcase("AbCdE"), ucase("aBcDe")`, [][]string{{"abcde", "ABCDE"}}},
		{"string_literal_concat", `SELECT 'abc' '123'`, [][]string{{"abc123"}}},
		{"split_substr", `SELECT split_substr("www.abc.com", ".", 1), split_substr("www.abc.com", ".", 1, 2), split_substr("www.abc.com", ".", 2, 2), split_substr("www.abc.com", ".", 3, 2), split_substr("www.abc.com", ".", 4, 1)`,
			[][]string{{"www.abc.com", "www.abc", "abc.com", "com", ""}}},
		{"split_substr_with_negative_position", `SELECT split_substr("www.abc.com", ".", -1, 2), split_substr("www.abc.com", ".", -2, 2), split_substr("www.abc.com", ".", -5, 1)`,
			[][]string{{"com", "abc.com", "www"}}},
		{"split_substr_with_null_arguments", `SELECT split_substr("foo", ".", 1, null)`, [][]string{{"NULL"}}},
		{"code_points_to_string_bytes_null_element", `select code_points_to_string([68, 69, NULL, 70]), code_points_to_bytes([68, 69, NULL, 70])`, [][]string{{"NULL", "NULL"}}},
		{"to_json_string_with_escaped_field_names", "SELECT TO_JSON_STRING(STRUCT<`a\\\"in\\\"between\\\\slashes\\\\\\\\` INT64, `abca\\x00\\x01\\x1A\\x1F` STRING>(1, 'foo'))",
			[][]string{{`{"a\"in\"between\\slashes\\\\":1,"abca\u0000\u0001\u001a\u001f":"foo"}`}}},
		{"to_json_string_with_json_extract", `SELECT TO_JSON_STRING(t) FROM (SELECT STRUCT(STRUCT(ARRAY<INT64>[] AS c, true AS d) AS b) AS a, 3 AS x) AS t`,
			[][]string{{`{"a":{"b":{"c":[],"d":true}},"x":3}`}}},
		{"json_query_array", `select TO_JSON_STRING(json_query_array('{"a.b": [1, "foo", null, {"c": 2}]}', '$."a.b"'))`,
			[][]string{{`["1","\"foo\"","null","{\"c\":2}"]`}}},
		{"format_with_non_const_arg", `select format('%*i', 6, 13), format('%0.*d', 4, 12), format('%*d', null, 12)`, [][]string{{"    13", "0012", "NULL"}}},

		// bytes.test
		{"function_regexp_match", `SELECT REGEXP_MATCH(b"abc", b""), REGEXP_MATCH(b"abc", b"a.c")`, [][]string{{"false", "true"}}},
		{"function_regexp_extract_all", `SELECT ARRAY_LENGTH(REGEXP_EXTRACT_ALL(b"abc", b""))`, [][]string{{"3"}}},

		// cast_function.test
		{"cast_struct_type_parameters_valid_input", `select CAST(cast(BIGNUMERIC '0.12345' as BIGNUMERIC(2, 2)) AS STRING), CAST(cast(NUMERIC '0.55' as NUMERIC(2, 1)) AS STRING)`,
			[][]string{{"0.12", "0.6"}}},

		// date.test
		{"date_constructor", `select string(date '1234-01-02')`, [][]string{{"1234-01-02"}}},
		{"current_date_with_null_timezone", `select current_date(cast(null as string))`, [][]string{{"NULL"}}},
		{"weekofyear_case3", `SELECT CAST(PARSE_DATE('%W%y', '092') AS STRING), CAST(PARSE_DATE('%W%y', '0902') AS STRING)`, [][]string{{"2002-03-04", "2002-03-04"}}},

		// range_constructors.test / range_functions.test
		{"range_of_dates_constructor_function_null_start_and_end", `SELECT CAST(RANGE(CAST(NULL AS DATE), NULL) AS STRING)`, [][]string{{"[UNBOUNDED, UNBOUNDED)"}}},
		{"safe_range_constructor", `SELECT SAFE.RANGE(DATE '2020-01-01', DATE '2020-01-01'), SAFE.RANGE(DATE '2030-01-01', DATE '2020-01-01')`, [][]string{{"NULL", "NULL"}}},
		{"array_reverse_range_date_null", `SELECT ARRAY_REVERSE(CAST(NULL AS ARRAY<RANGE<DATE>>)) IS NULL`, [][]string{{"true"}}},

		// array_functions.test
		{"euclidian_distance_shuffled_input_strkey", `SELECT EUCLIDEAN_DISTANCE(ARRAY[('a', 10.0), ('b', 30.0), ('d', 40.0)], ARRAY(SELECT e FROM UNNEST(ARRAY[('a', 10.0), ('b', 30.0), ('d', 40.0)]) AS e))`,
			[][]string{{"0"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := scalarComplianceRows(ctx, db, tc.query)
			if err != nil {
				t.Fatal(err)
			}
			if fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}

	errCases := []struct {
		name, query, wantErr string
	}{
		// safe_function.test (non-SAFE counterparts must still fail).
		{"cot_zero", `SELECT cot(0)`, "division by zero"},
		// range_constructors.test
		{"range_of_dates_constructor_function_start_equals_end", `SELECT RANGE(DATE '2014-01-01', DATE '2014-01-01')`, "Range start element must be smaller than range end element"},
		// range_functions.test
		{"range_intersect_throws_error_when_range_of_dates_do_not_overlap", `SELECT RANGE_INTERSECT(RANGE(DATE '2014-01-01', DATE '2020-01-01'), RANGE(DATE '2021-01-01', DATE '2022-01-01'))`, "do not overlap"},
		// cast_function.test
		{"cast_numeric_type_parameters_invalid_input", `select cast(123456 as NUMERIC(5))`, "NUMERIC(5) has precision 5 and scale 0 but got a value that is not in range of [-99999, 99999]"},
		{"cast_time_to_string_invalid_format", `SELECT CAST(TIME'01:02:03' as string FORMAT 'yyyy' || 'mi')`, "TIME does not support 'YYYY'"},
		// cast_format_validation.test
		{"cast_date_to_string_format_invalid_literal_hh", `select cast(date'1234-05-06' as string format 'HH')`, "DATE does not support 'HH'"},
		// An aggregate error whose value is used still fails, including
		// when the value is written to a table.
		{"aggregate_error_used", `SELECT SUM(A/0) FROM T`, "zero divided"},
		{"aggregate_error_inserted", `CREATE TABLE U AS SELECT SUM(A/0) AS s FROM T`, "zero divided"},
		// date.test weekofyear_case1.
		{"weekofyear_case1", `SELECT PARSE_DATE('%W%y', '92')`, "92"},
	}
	for _, tc := range errCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := scalarComplianceRows(ctx, db, tc.query)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("got err %v, want error containing %q", err, tc.wantErr)
			}
		})
	}
}

func scalarComplianceRows(ctx context.Context, db *sql.DB, query string) ([][]string, error) {
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var got [][]string
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make([]string, len(vals))
		for i, v := range vals {
			if v == nil {
				row[i] = "NULL"
			} else {
				row[i] = fmt.Sprint(v)
			}
		}
		got = append(got, row)
	}
	return got, rows.Err()
}
