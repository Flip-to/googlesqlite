package googlesqlite_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	_ "github.com/goccy/googlesqlite"
)

// Regression cases taken from the GoogleSQL compliance suite
// (googlesql/compliance/testdata/*.test). Each case names the .test file
// and the [name=...] case it comes from; the expected rows are the ones
// recorded there. Tables that the .test files create in
// [prepare_database] blocks are inlined as WITH clauses.

const numericSpecialValuesTable = `AnalyticTableWithSpecialValues AS (
  SELECT CAST(1 AS INT64) as row_id, CAST(null AS numeric) as numeric_val UNION ALL
  SELECT 2, null UNION ALL
  SELECT 3, NUMERIC '-99999999999999999999999999999.999999999' UNION ALL
  SELECT 4, NUMERIC '-99999999999999999999999999999.999999999' UNION ALL
  SELECT 5, NUMERIC '-99999999999999999999999999998.999999999' UNION ALL
  SELECT 6, NUMERIC '-99999999999999999999999999994.999999999' UNION ALL
  SELECT 7, 10 UNION ALL
  SELECT 8, 11 UNION ALL
  SELECT 9, 11 UNION ALL
  SELECT 10, 12 UNION ALL
  SELECT 11, 13 UNION ALL
  SELECT 12, 13 UNION ALL
  SELECT 13, NUMERIC '99999999999999999999999999994.999999999' UNION ALL
  SELECT 14, NUMERIC '99999999999999999999999999998.999999999' UNION ALL
  SELECT 15, NUMERIC '99999999999999999999999999999.999999999' UNION ALL
  SELECT 16, NUMERIC '99999999999999999999999999999.999999999')`

const numericWithoutSpecialValuesTable = `AnalyticTableWithoutSpecialValues AS (
  SELECT CAST(1 AS INT64) as row_id, CAST(10 AS numeric) as numeric_val UNION ALL
  SELECT 2, 11 UNION ALL
  SELECT 3, 11 UNION ALL
  SELECT 4, 12 UNION ALL
  SELECT 5, 13 UNION ALL
  SELECT 6, 13)`

const hllSketch = `b"\010p\020\n\030\002\202\007-\020\n\030\017 \0312#\352\310\251\001\260\260T\363\303\037\366\315\201\006\302\335\n\313\267\332\001\273\343\246\001\206\342\035\320\260\232\002\325\305\0328\002"`

// hllSketch merged with itself: num_values 20, the legacy value type
// moved to AggregatorStateProto.value_type (8).
const hllSketchTwice = "087010141802200882072b100a180f20193223eac8a901b0b054f3c31ff6cd8106c2dd0acbb7da01bbe3a60186e21dd0b09a02d5c51a"

const percentileTestTable = `TestTable AS (
  SELECT cast(0 as int64) as row_id, cast(null as double) as value UNION ALL
  SELECT 1, cast('inf' as double) UNION ALL
  SELECT 2, cast('-inf' as double) UNION ALL
  SELECT 3, cast('nan' as double) UNION ALL
  SELECT 4, 1.0786158809173895e+308 UNION ALL
  SELECT 5, -1.0786158809173895e+308 UNION ALL
  SELECT 6, 4.94065645841247e-324 UNION ALL
  SELECT 7, 0 UNION ALL
  SELECT 8, -100 UNION ALL
  SELECT 9, -400)`

const percentileNumericTable = `TestNumericTable AS (
  SELECT 0 as row_id, null as key, cast(null as numeric) as value UNION ALL
  SELECT 1, null, 99999999999999999999999999999.999999999 UNION ALL
  SELECT 2, 0, -99999999999999999999999999999.999999999 UNION ALL
  SELECT 3, 0, -1e-9 UNION ALL
  SELECT 4, null, null UNION ALL
  SELECT 5, 0, 0 UNION ALL
  SELECT 6, 0, 1 UNION ALL
  SELECT 7, 0, -1e-9 UNION ALL
  SELECT 8, 0, -10000000000000000000000000000.000000001 UNION ALL
  SELECT 9, null, 99999999999999999999999999999.999999999 UNION ALL
  SELECT 10, null, -99999999999999999999999999999.999999999)`

const groupingSimpleTable = `simple_table AS (
  SELECT 1 AS key, 10 AS a, 'foo' AS b, true AS c, 12.0 AS d
  UNION ALL SELECT 2, 123, 'foo', false, CAST("NAN" as FLOAT64)
  UNION ALL SELECT 3, 123, 'bar', true, 123.456e-67
  UNION ALL SELECT 4, CAST(NULL AS int64), CAST(NULL AS STRING), CAST(NULL AS BOOL), CAST(NULL AS FLOAT64)
  UNION ALL SELECT 5, 10, 'bar', CAST(NULL AS BOOL), CAST(NULL AS FLOAT64))`

func TestAnalyticCompliance(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("googlesqlite", ":memory:?_test=analytic_compliance")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	// Each want lists the rows in the query's (ORDER BY) order, one
	// row per line, columns separated by " | ".
	for _, tc := range []struct {
		source string
		query  string
		want   []string
	}{
		{
			source: "analytic_numeric_range_window_frames.test: numeric_range_window_asc_with_special_values_1",
			query: `WITH ` + numericSpecialValuesTable + `
SELECT row_id, MIN(row_id) OVER w, MAX(row_id) OVER w
FROM AnalyticTableWithSpecialValues
WINDOW w AS (ORDER BY numeric_val ASC RANGE BETWEEN UNBOUNDED PRECEDING and 2 PRECEDING)
ORDER BY row_id`,
			want: []string{
				"1 | 1 | 2", "2 | 1 | 2", "3 | 1 | 2", "4 | 1 | 2", "5 | 1 | 2", "6 | 1 | 5",
				"7 | 1 | 6", "8 | 1 | 6", "9 | 1 | 6", "10 | 1 | 7", "11 | 1 | 9", "12 | 1 | 9",
				"13 | 1 | 12", "14 | 1 | 13", "15 | 1 | 13", "16 | 1 | 13",
			},
		},
		{
			source: "analytic_numeric_range_window_frames.test: numeric_range_window_desc_with_special_values_1",
			query: `WITH ` + numericSpecialValuesTable + `
SELECT row_id, MAX(row_id) OVER w, MIN(row_id) OVER w
FROM AnalyticTableWithSpecialValues
WINDOW w AS (ORDER BY numeric_val DESC RANGE BETWEEN UNBOUNDED PRECEDING and 2 PRECEDING)
ORDER BY row_id`,
			want: []string{
				"1 | 16 | 1", "2 | 16 | 1", "3 | 16 | 6", "4 | 16 | 6", "5 | 16 | 6", "6 | 16 | 7",
				"7 | 16 | 10", "8 | 16 | 11", "9 | 16 | 11", "10 | 16 | 13", "11 | 16 | 13", "12 | 16 | 13",
				"13 | 16 | 14", "14 | NULL | NULL", "15 | NULL | NULL", "16 | NULL | NULL",
			},
		},
		{
			source: "analytic_numeric_range_window_frames.test: numeric_range_window_asc_without_special_values_1",
			query: `WITH ` + numericWithoutSpecialValuesTable + `
SELECT row_id, MIN(row_id) OVER w, MAX(row_id) OVER w
FROM AnalyticTableWithoutSpecialValues
WINDOW w AS (ORDER BY numeric_val ASC RANGE BETWEEN UNBOUNDED PRECEDING and NUMERIC '2' PRECEDING)
ORDER BY row_id`,
			want: []string{"1 | NULL | NULL", "2 | NULL | NULL", "3 | NULL | NULL", "4 | 1 | 1", "5 | 1 | 3", "6 | 1 | 3"},
		},
		{
			source: "analytic_bignumeric_range_window_frames.test: bignumeric_range_window_desc_without_special_values_1",
			query: `WITH AnalyticTableWithoutSpecialValues AS (
  SELECT CAST(1 AS INT64) as row_id, CAST(10 AS bignumeric) as bignumeric_val UNION ALL
  SELECT 2, 11 UNION ALL SELECT 3, 11 UNION ALL SELECT 4, 12 UNION ALL SELECT 5, 13 UNION ALL SELECT 6, 13)
SELECT row_id, MAX(row_id) OVER w, MIN(row_id) OVER w
FROM AnalyticTableWithoutSpecialValues
WINDOW w AS (ORDER BY bignumeric_val DESC RANGE BETWEEN UNBOUNDED PRECEDING and BIGNUMERIC '2' PRECEDING)
ORDER BY row_id`,
			want: []string{"1 | 6 | 4", "2 | 6 | 5", "3 | 6 | 5", "4 | NULL | NULL", "5 | NULL | NULL", "6 | NULL | NULL"},
		},
		{
			source: "analytic_sum.test: analytic_sum_range_unbounded_and_preceding_single_row_partition_double_asc",
			query: `SELECT p, o,
       SUM(a) OVER (PARTITION BY p ORDER BY o
                    RANGE BETWEEN UNBOUNDED PRECEDING AND 2 PRECEDING)
FROM (SELECT 1 a, 1 p, 3.0 o
      UNION ALL SELECT 2 a, 2 p, null o
      UNION ALL SELECT 3 a, 3 p, CAST("+INF" AS double) o
      UNION ALL SELECT 4 a, 4 p, CAST("-INF" AS double) o
      UNION ALL SELECT 5 a, 5 p, CAST("NaN" AS double) o)
ORDER BY p`,
			want: []string{"1 | 3 | NULL", "2 | NULL | 2", "3 | +Inf | 3", "4 | -Inf | 4", "5 | NaN | 5"},
		},
		{
			source: "analytic_sum.test: analytic_sum_range_following_and_following_orderby_desc_special_doubles_with_large_offset",
			query: `SELECT val,
       SUM(val) OVER (ORDER BY row_id DESC
                      RANGE BETWEEN 1 FOLLOWING AND 9223372036854775807 FOLLOWING),
       SUM(val) OVER (ORDER BY row_id DESC
                      RANGE BETWEEN 9223372036854775806 FOLLOWING AND 9223372036854775807 FOLLOWING)
FROM (SELECT null row_id, 1 val UNION ALL
      SELECT 2, 2 UNION ALL
      SELECT 1, 3 UNION ALL
      SELECT (cast("NaN" as double)), 4 UNION ALL
      SELECT (cast("+inf" as double)), 5 UNION ALL
      SELECT (cast("NaN" as double)), 6 UNION ALL
      SELECT (cast("+inf" as double)), 7 UNION ALL
      SELECT 2, 8 UNION ALL
      SELECT (cast("-inf" as double)), 9 UNION ALL
      SELECT null, 10 UNION ALL
      SELECT 1, 11 UNION ALL
      SELECT (cast("NaN" as double)), 12 UNION ALL
      SELECT 3, 13 UNION ALL
      SELECT (cast("-inf" as double)), 14)
ORDER BY val`,
			want: []string{
				"1 | 11 | 11", "2 | 14 | NULL", "3 | NULL | NULL", "4 | 22 | 22", "5 | 12 | 12",
				"6 | 22 | 22", "7 | 12 | 12", "8 | 14 | NULL", "9 | 23 | 23", "10 | 11 | 11",
				"11 | NULL | NULL", "12 | 22 | 22", "13 | 24 | NULL", "14 | 23 | 23",
			},
		},
		{
			source: "analytic_sum.test: analytic_moving_sum_int64_no_overflow_2",
			query: `SELECT range_val, SUM(val) OVER (ORDER BY range_val
                                      RANGE BETWEEN 3 PRECEDING AND 1 PRECEDING)
FROM (SELECT -1 range_val, (-9223372036854775808) val UNION ALL
      SELECT 1, (9223372036854775807) UNION ALL
      SELECT 2, -5 UNION ALL
      SELECT 4, (-9223372036854775808) UNION ALL
      SELECT 4, (9223372036854775807) UNION ALL
      SELECT 8, (9223372036854775807) UNION ALL
      SELECT 9, (9223372036854775807) UNION ALL
      SELECT 12, (-9223372036854775808))
ORDER BY range_val`,
			// The .test case has eight rows at range_val 4; two keep the
			// frames of the other rows unchanged.
			want: []string{
				"-1 | NULL", "1 | -9223372036854775808", "2 | -1", "4 | 9223372036854775802",
				"4 | 9223372036854775802", "8 | NULL", "9 | 9223372036854775807", "12 | 9223372036854775807",
			},
		},
		{
			source: "analytic_sum.test: analytic_sum_int64_overflow_3",
			query: `SELECT row_id, SUM(val) OVER (ORDER BY row_id
                                   ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING)
FROM (SELECT 1 row_id, (9223372036854775807) val UNION ALL
      SELECT 2, 1 UNION ALL
      SELECT 3, (-9223372036854775808))
ORDER BY row_id`,
			want: []string{"1 | 0", "2 | 0", "3 | 0"},
		},
		{
			source: "analytic_hll_count.test: analytic_hll_count_merge_partial_over_unbounded_window_1",
			query: `SELECT row_id, TO_HEX(HLL_COUNT.MERGE_PARTIAL(sketch) OVER ())
FROM (
  SELECT cast(1 as int64) as row_id, cast(NULL as BYTES) as sketch UNION ALL
  SELECT 2, ` + hllSketch + ` UNION ALL
  SELECT 3, ` + hllSketch + `)
ORDER BY row_id`,
			want: []string{"1 | " + hllSketchTwice, "2 | " + hllSketchTwice, "3 | " + hllSketchTwice},
		},
		{
			source: "analytic_hll_count.test: analytic_hll_count_merge_partial_over_range_unbounded_and_following",
			query: `SELECT row_id, TO_HEX(HLL_COUNT.MERGE_PARTIAL(sketch) OVER (ORDER BY row_id RANGE BETWEEN UNBOUNDED PRECEDING AND 1 FOLLOWING))
FROM (
  SELECT cast(1 as int64) as row_id, ` + hllSketch + ` as sketch UNION ALL
  SELECT 2, ` + hllSketch + ` UNION ALL
  SELECT 3, ` + hllSketch + `)
ORDER BY row_id`,
			want: []string{
				"1 | " + hllSketchTwice,
				"2 | 0870101e1802200882072b100a180f20193223eac8a901b0b054f3c31ff6cd8106c2dd0acbb7da01bbe3a60186e21dd0b09a02d5c51a",
				"3 | 0870101e1802200882072b100a180f20193223eac8a901b0b054f3c31ff6cd8106c2dd0acbb7da01bbe3a60186e21dd0b09a02d5c51a",
			},
		},
		{
			source: "grouping_sets_queries.test: grouping_func_with_single_column_rollup",
			query: `WITH ` + groupingSimpleTable + `
SELECT a, UPPER(b), GROUPING(a), GROUPING(UPPER(b))
FROM simple_table
GROUP BY ROLLUP(a, UPPER(b))
ORDER BY 3, 4, 1, 2`,
			want: []string{
				"NULL | NULL | 0 | 0", "10 | BAR | 0 | 0", "10 | FOO | 0 | 0", "123 | BAR | 0 | 0", "123 | FOO | 0 | 0",
				"NULL | NULL | 0 | 1", "10 | NULL | 0 | 1", "123 | NULL | 0 | 1", "NULL | NULL | 1 | 1",
			},
		},
		{
			source: "grouping_sets_queries.test: grouping_sets_with_alias",
			query: `WITH ` + groupingSimpleTable + `
SELECT a+d AS x, b AS y, c AS z, COUNT(*)
FROM simple_table
GROUP BY GROUPING SETS(x, (y, z), z)
ORDER BY 1, 2, 3, COUNT(*)`,
			want: []string{
				"NULL | NULL | NULL | 1", "NULL | NULL | NULL | 2", "NULL | NULL | NULL | 2",
				"NULL | NULL | false | 1", "NULL | NULL | true | 2", "NULL | bar | NULL | 1",
				"NULL | bar | true | 1", "NULL | foo | false | 1", "NULL | foo | true | 1",
				"NaN | NULL | NULL | 1", "22 | NULL | NULL | 1", "123 | NULL | NULL | 1",
			},
		},
		{
			source: "grouping_sets_queries.test: empty_grouping_sets_select_literal",
			query: `WITH ` + groupingSimpleTable + `
SELECT 1, "a", 1.23 FROM simple_table GROUP BY GROUPING SETS(())`,
			want: []string{"1 | a | 1.23"},
		},
		{
			source: "grouping_sets_queries.test: grouping_func_with_regular_group_by_query",
			query: `WITH ` + groupingSimpleTable + `
SELECT a, UPPER(b), GROUPING(a), GROUPING(UPPER(b))
FROM simple_table
GROUP BY a, UPPER(b)
ORDER BY 3, 4, 1, 2`,
			want: []string{"NULL | NULL | 0 | 0", "10 | BAR | 0 | 0", "10 | FOO | 0 | 0", "123 | BAR | 0 | 0", "123 | FOO | 0 | 0"},
		},
		{
			source: "array_aggregation.test: array_agg_with_nulls",
			query: `SELECT FORMAT('%T', ARRAY(SELECT e FROM UNNEST(a) e ORDER BY e)) FROM (
  SELECT ARRAY_AGG(elem) a FROM (
    SELECT 1 AS elem UNION ALL
    SELECT NULL UNION ALL
    SELECT 3))`,
			want: []string{"[NULL, 1, 3]"},
		},
		{
			source: "array_aggregation.test: array_agg_with_having",
			query: `SELECT FORMAT('%T', ARRAY(SELECT e FROM UNNEST(b) e ORDER BY e)) FROM (
  SELECT ARRAY_AGG(a) b FROM (
    SELECT 1 AS a UNION ALL
    SELECT 2 UNION ALL
    SELECT 3)
  HAVING ARRAY_LENGTH(b) > 1)`,
			want: []string{"[1, 2, 3]"},
		},
		{
			source: "aggregate_percentile_cont.test: aggregate_percentile_cont_interpolation",
			query: `WITH ` + percentileTestTable + `
SELECT PERCENTILE_CONT(value, 4.94065645841247e-324),
       PERCENTILE_CONT(value, 0.1249999999999999),
       PERCENTILE_CONT(value, 0.2499999999999999),
       PERCENTILE_CONT(value, 0.3),
       PERCENTILE_CONT(value, 0.4375),
       PERCENTILE_CONT(value, 0.5625),
       PERCENTILE_CONT(value, 0.65),
       PERCENTILE_CONT(value, 0.7),
       PERCENTILE_CONT(value, 0.8125),
       PERCENTILE_CONT(value, 0.8750000000000001)
FROM TestTable`,
			// Floats are printed in Go's shortest form, so the .test
			// file's 5.3930794045869475e+307 goes through fmt.Sprint.
			want: []string{"NaN | NaN | -Inf | -6.471695285504338e+307 | -250 | -50 | 0 | 5e-324 | " + fmt.Sprint(5.3930794045869475e+307) + " | +Inf"},
		},
		{
			source: "aggregate_percentile_cont.test: aggregate_percentile_cont_numeric",
			query: `WITH ` + percentileNumericTable + `
SELECT CAST(PERCENTILE_CONT(value, NUMERIC "0.2") AS STRING),
       CAST(PERCENTILE_CONT(value, NUMERIC "0.699999999") AS STRING),
       CAST(PERCENTILE_CONT(value, NUMERIC "0.8") AS STRING)
FROM TestNumericTable`,
			want: []string{"-46000000000000000000000000000 | 0.599999992 | 40000000000000000000000000000.6"},
		},
	} {
		t.Run(tc.source, func(t *testing.T) {
			got, err := queryRowStrings(db, tc.query)
			if err != nil {
				t.Fatalf("%s\n%s: %v", tc.source, tc.query, err)
			}
			if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
				t.Errorf("%s\ngot:\n%s\nwant:\n%s", tc.source, strings.Join(got, "\n"), strings.Join(tc.want, "\n"))
			}
		})
	}
}

func queryRowStrings(db *sql.DB, query string) ([]string, error) {
	rows, err := db.QueryContext(context.Background(), query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out []string
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		parts := make([]string, len(vals))
		for i, v := range vals {
			if v == nil {
				parts[i] = "NULL"
			} else {
				parts[i] = fmt.Sprint(v)
			}
		}
		out = append(out, strings.Join(parts, " | "))
	}
	return out, rows.Err()
}
