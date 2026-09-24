package googlesqlite_test

import (
	"database/sql"
	"strings"
	"testing"
)

// TestComplianceTailMisc pins fixes for GoogleSQL compliance cases.
// Every expected value is taken from the cited fixture in
// googlesql/compliance/testdata (file and case name in each entry).
func TestComplianceTailMisc(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("googlesqlite", ":memory:?_test=compliance_tail_misc")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	const doubles = `(SELECT CAST(0 AS DOUBLE) as double_val UNION ALL
      SELECT CAST(1 AS DOUBLE)               UNION ALL
      SELECT CAST(null AS DOUBLE)            UNION ALL
      SELECT CAST(null AS DOUBLE)            UNION ALL
      SELECT CAST("NaN" AS DOUBLE)           UNION ALL
      SELECT CAST("inf" AS DOUBLE)           UNION ALL
      SELECT CAST("inf" AS DOUBLE)           UNION ALL
      SELECT CAST("NaN" AS DOUBLE)           UNION ALL
      SELECT CAST("-inf" AS DOUBLE)          UNION ALL
      SELECT CAST("-inf" AS DOUBLE))`

	cases := []struct {
		name  string
		query string
		want  []string // rows, columns joined by "|", sorted query output
	}{
		// analytic_cume_dist.test cume_dist_double.
		{"cume_dist_double", `SELECT FORMAT('%s|%s', IFNULL(CAST(double_val AS STRING), 'NULL'), CAST(CUME_DIST() OVER (ORDER BY double_val) AS STRING)) FROM ` + doubles + ` ORDER BY 1`,
			[]string{"-inf|0.6", "-inf|0.6", "0|0.7", "1|0.8", "NULL|0.2", "NULL|0.2", "inf|1", "inf|1", "nan|0.4", "nan|0.4"}},
		// analytic_partitionby_orderby.test rank_orderby_double.
		{"rank_orderby_double", `SELECT FORMAT('%s|%d', IFNULL(CAST(double_val AS STRING), 'NULL'), RANK() OVER (ORDER BY double_val DESC)) FROM ` + doubles + ` ORDER BY 1`,
			[]string{"-inf|5", "-inf|5", "0|4", "1|3", "NULL|9", "NULL|9", "inf|1", "inf|1", "nan|7", "nan|7"}},
		// analytic_avg.test analytic_avg_range_current_and_following_double_asc
		// (the 1.79769e+308 row of the last column).
		{"avg_range_overflow", `SELECT FORMAT('%T', AVG(val) OVER (ORDER BY row_id RANGE BETWEEN CURRENT ROW AND (1.79769e+308) FOLLOWING)) FROM (
			SELECT CAST("+inf" AS DOUBLE) row_id, 5 val UNION ALL SELECT CAST("+inf" AS DOUBLE), 7 UNION ALL SELECT (1.79769e+308), 17) ORDER BY 1`,
			[]string{"17.0", "6.0", "6.0"}},
		// analytic_percentile_disc.test analytic_percentile_disc_bignumeric_percentile.
		{"percentile_disc_bignumeric", `SELECT DISTINCT FORMAT('%T|%T', PERCENTILE_DISC(value, bignumeric "0.4") OVER(),
			PERCENTILE_DISC(value, bignumeric "0.40000000000000000000000000000000000001") OVER())
			FROM (SELECT 1 row_id, CAST(NULL as STRING) value UNION ALL SELECT 2, "A1" UNION ALL SELECT 3, "a"
			UNION ALL SELECT 4, "A" UNION ALL SELECT 5, "z" UNION ALL SELECT 6, "")`,
			[]string{`"A"|"A1"`}},
		// analytic_percentile_cont.test analytic_percentile_cont_partition
		// (a UNION ALL whose first branch outputs one column twice).
		{"union_duplicate_first_branch_column", `WITH TestTable AS (SELECT 1 row_id, 1.0 value UNION ALL SELECT 2, 2.0)
			SELECT FORMAT('%T|%T|%T', x, y, z) FROM (
			  SELECT t1.value x, t2.value y, t1.value z FROM TestTable t1, TestTable t2
			  UNION ALL
			  SELECT t1.value x, t2.value y, t2.value z FROM TestTable t1, TestTable t2) ORDER BY 1`,
			[]string{"1.0|1.0|1.0", "1.0|1.0|1.0", "1.0|2.0|1.0", "1.0|2.0|2.0", "2.0|1.0|1.0", "2.0|1.0|2.0", "2.0|2.0|2.0", "2.0|2.0|2.0"}},
		// arithmetic_functions.test arithmetic_functions_14_safe_divide.
		{"safe_divide_overflow", `SELECT FORMAT('%T', safe_divide(1e300, 1e-300))`, []string{"NULL"}},
		// math_functions.test math_abs_zero.
		{"ieee_divide_negative_zero", `SELECT FORMAT('%s|%s', CAST(IEEE_DIVIDE(1, -0.0) AS STRING), CAST(IEEE_DIVIDE(1, ABS(-0.0)) AS STRING))`, []string{"-inf|inf"}},
		// additional_date_time_functions.test last_day_date.
		{"last_day_date", `SELECT FORMAT('%T|%T|%T|%T|%T|%T|%T', last_day(date '2020-07-10', YEAR), last_day(date '2020-06-10', MONTH),
			last_day(date '2020-06-10'), last_day(date '2020-07-10', QUARTER), last_day(date '2020-07-24', WEEK),
			last_day(date '2020-07-24', WEEK(MONDAY)), last_day(date '0001-01-01', ISOYEAR))`,
			[]string{`DATE "2020-12-31"|DATE "2020-06-30"|DATE "2020-06-30"|DATE "2020-09-30"|DATE "2020-07-25"|DATE "2020-07-26"|DATE "0001-12-30"`}},
		// group_by_all.test group_by_all_no_agg_no_grouping_keys_regression_test.
		{"group_by_all_no_keys", `SELECT 'a' AS x FROM (SELECT 1 AS id UNION ALL SELECT 2 AS id) GROUP BY ALL`, []string{"a"}},
		// group_by_all.test group_by_all_no_agg_no_grouping_keys_having_regression_test.
		{"group_by_all_no_keys_having", `SELECT 'a' AS x FROM (SELECT 1 AS id UNION ALL SELECT 2 AS id) GROUP BY ALL HAVING TRUE`, []string{"a"}},
		// groupby_queries_2.test group_by_empty_columns_select_literals.
		{"group_by_empty", `SELECT "a" FROM (SELECT 1 AS id UNION ALL SELECT 2 AS id) GROUP BY ()`, []string{"a"}},
		// analytic_approx_count_distinct.test analytic_approx_count_distinct_basic.
		{"approx_count_distinct_over", `SELECT FORMAT('%T', APPROX_COUNT_DISTINCT(x) OVER ()) FROM UNNEST([0, 1, 1, 2, 3, 5]) as x`,
			[]string{"5", "5", "5", "5", "5", "5"}},
		// analytic_approx_top_count.test analytic_approx_top_count_basic.
		{"approx_top_count_over", `SELECT DISTINCT FORMAT('%T', APPROX_TOP_COUNT(x, 2) OVER ()) FROM UNNEST(["apple", "apple", "pear", "pear", "pear", "banana"]) as x`,
			[]string{`[("pear", 3), ("apple", 2)]`}},
	}
	for _, tc := range cases {
		rows, err := db.Query(tc.query)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		var got []string
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				t.Errorf("%s: scan: %v", tc.name, err)
				break
			}
			got = append(got, s)
		}
		if err := rows.Err(); err != nil {
			t.Errorf("%s: %v", tc.name, err)
		}
		rows.Close()
		if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got, tc.want)
		}
	}

	errCases := []struct {
		name  string
		setup []string
		query string
		want  string
	}{
		// arithmetic_functions.test arithmetic_functions_14.
		{"divide_overflow", nil, `SELECT 1e300/1e-300`, "double overflow: 1e+300 / 1e-300"},
		// hll_count.test hll_count_merge_incompatible_types.
		{"hll_merge_incompatible", nil, `SELECT HLL_COUNT.MERGE(x) FROM (
			SELECT HLL_COUNT.INIT(x) AS x FROM UNNEST([1, 2, 3]) AS x
			UNION ALL SELECT HLL_COUNT.INIT(x) AS x FROM UNNEST(["foo", "bar"]) AS x)`,
			"Invalid or incompatible sketch in HLL_COUNT.MERGE"},
		// hll_count.test hll_count_merge_partial_incompatible_types.
		{"hll_merge_partial_incompatible", nil, `SELECT HLL_COUNT.MERGE_PARTIAL(x) FROM (
			SELECT HLL_COUNT.INIT(x) AS x FROM UNNEST([1, 2, 3]) AS x
			UNION ALL SELECT HLL_COUNT.INIT(x) AS x FROM UNNEST(["foo", "bar"]) AS x)`,
			"Invalid or incompatible sketch in HLL_COUNT.MERGE_PARTIAL"},
		// orderby_collate_queries.test orderby_collate_binary_cs_is_an_error.
		{"collate_binary_suffix", nil, `SELECT col FROM (SELECT "Ca" col UNION ALL SELECT "ca") ORDER BY col COLLATE "binary:cs"`,
			"COLLATE has invalid collation name 'binary:cs':binary cannot be combined with a suffix"},
		// dml_update_struct.test assign_struct_field_in_null_struct.
		{"set_field_of_null_struct", []string{
			`CREATE TABLE TailMiscStructs (id INT64, struct_value STRUCT<int64_value INT64, array_value ARRAY<INT64>>)`,
			`INSERT INTO TailMiscStructs VALUES (1, STRUCT(1, [1])), (2, NULL)`,
		}, `UPDATE TailMiscStructs SET struct_value.int64_value = 100 WHERE True`,
			"Cannot set field of NULL STRUCT<int64_value INT64, array_value ARRAY<INT64>>"},
	}
	for _, tc := range errCases {
		for _, s := range tc.setup {
			if _, err := db.Exec(s); err != nil {
				t.Fatalf("%s: setup: %v", tc.name, err)
			}
		}
		err := func() error {
			rows, err := db.Query(tc.query)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
			}
			return rows.Err()
		}()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got error %v, want %q", tc.name, err, tc.want)
		}
	}
}
