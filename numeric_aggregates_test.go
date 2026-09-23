package googlesqlite_test

import (
	"context"
	"database/sql"
	"math"
	"strings"
	"testing"
)

// TestNumericAggregateSemantics pins aggregate and window arithmetic to
// the GoogleSQL rules. Expected values are the GoogleSQL compliance
// fixtures named in each case (compliance/testdata/*.test), with the
// fixture's parameters inlined, or statements in
// docs/third_party/googlesql-docs where noted.
func TestNumericAggregateSemantics(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("googlesqlite", ":memory:?_test=numeric_aggregates")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	nan := math.NaN()
	inf := math.Inf(1)

	cases := []struct {
		name  string
		query string
		want  []any // one row
	}{
		// stat_aggregation.test: sample statistics of one row are NULL.
		{"stddev_samp_one_value", `SELECT STDDEV_SAMP(x) FROM UNNEST([1.0]) x`, []any{nil}},
		{"covar_one_pair", `SELECT COVAR_SAMP(y, x), COVAR_POP(y, x) FROM UNNEST([STRUCT(1.0 AS y, 1.0 AS x)])`, []any{nil, 0.0}},
		// stat_aggregation.test corr_double_extreme_negative_slope.
		{"corr_extreme", `SELECT CORR(y, x) FROM UNNEST([STRUCT(1.0 AS y, -1.0 AS x), (2.2e+154, -2.2e+154)])`, []any{-1.0}},
		// stat_aggregation.test stddev_samp_double_extreme: no overflow.
		{"stddev_extreme", `SELECT STDDEV_SAMP(x) FROM UNNEST([1.0, 2.2e+304, -2.2e+304]) x`, []any{2.2e+304}},
		// stat_aggregation.test var_samp_numeric_high_prec_values
		// (TableNumericHighPrecValues holds 0.1 and 0.2 as NUMERIC):
		// NUMERIC moments are exact.
		{"var_samp_numeric", `SELECT VAR_SAMP(x) FROM UNNEST([NUMERIC '0.1', NUMERIC '0.2']) x`, []any{0.005}},
		// aggregate-function-calls.md HAVING MAX / HAVING MIN: only rows
		// whose key equals the extreme are aggregated.
		{"having_max", `SELECT VAR_SAMP(x HAVING MAX y), SUM(x HAVING MIN y) FROM UNNEST([STRUCT(1.0 AS x, 1 AS y), (2.0, 3), (4.0, 3), (8.0, NULL)])`, []any{2.0, 1.0}},
		// analytic_sum.test analytic_sum_double_overflow_3: the exact sum
		// fits even though a running sum would overflow.
		{"window_sum_exact", `SELECT SUM(v) OVER (ORDER BY r ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING) FROM UNNEST([STRUCT(1 AS r, 1.79769e+308 AS v), (2, 1.79769e+307), (3, -1.79769e+308)]) ORDER BY r LIMIT 1`, []any{1.79769e+307}},
		// analytic_sum.test analytic_sum_double_inf_3: inf and -inf in one
		// frame give NaN; the frame then slides past +inf.
		{"window_sum_inf_slides", `SELECT ARRAY_TO_STRING(ARRAY_AGG(CAST(s AS STRING) ORDER BY r), ",") FROM (SELECT r, SUM(v) OVER (ORDER BY r ROWS BETWEEN 1 PRECEDING AND CURRENT ROW) s FROM UNNEST([STRUCT(1 AS r, CAST('inf' AS FLOAT64) AS v), (2, CAST('-inf' AS FLOAT64)), (3, CAST('-inf' AS FLOAT64))]))`, []any{"inf,nan,-inf"}},
		// analytic_sum_distinct_no_overflow_double_1: DOUBLE is not
		// truncated to INT64.
		{"window_sum_distinct_double", `SELECT SUM(DISTINCT v) OVER () FROM UNNEST([1.79769e+308, 1.79769e+308]) v LIMIT 1`, []any{1.79769e+308}},
		// aggregate_functions.md MAX: NaN in the input gives NaN.
		{"window_max_nan", `SELECT MAX(v) OVER () FROM UNNEST([1.0, CAST('nan' AS FLOAT64)]) v LIMIT 1`, []any{nan}},
		{"window_sum_inf", `SELECT SUM(v) OVER () FROM UNNEST([1.0, CAST('inf' AS FLOAT64)]) v LIMIT 1`, []any{inf}},
		// analytic_min_max.test analytic_min_bignumeric_*: BIGNUMERIC
		// compares as a number, not as encoded text.
		{"window_min_bignumeric", `SELECT CAST(MIN(v) OVER () AS STRING) FROM UNNEST([BIGNUMERIC '3.5', BIGNUMERIC '10', BIGNUMERIC '-1.25']) v LIMIT 1`, []any{"-1.25"}},
		// interval.test: negative time parts keep their sign.
		{"interval_negative_second", `SELECT CAST(i AS STRING) FROM UNNEST([INTERVAL -1 SECOND]) i`, []any{"0-0 0 -0:0:1"}},
		{"interval_negative_nanos", `SELECT CAST(i AS STRING) FROM UNNEST([INTERVAL '-0.123456789' SECOND]) i`, []any{"0-0 0 -0:0:0.123456789"}},
		{"interval_negative_months", `SELECT CAST(i AS STRING) FROM UNNEST([INTERVAL -3 MONTH]) i`, []any{"-0-3 0 0:0:0"}},
		// data-types.md interval type: 1 MONTH = 30 DAY = 720 HOUR.
		{"interval_count_distinct", `SELECT COUNT(DISTINCT i) FROM UNNEST([INTERVAL 1 MONTH, INTERVAL 30 DAY, INTERVAL 720 HOUR]) i`, []any{int64(1)}},
		// interval.test group_by_multiple_intervals: equal intervals group.
		{"interval_group_by", `SELECT COUNT(*) FROM (SELECT i FROM UNNEST([INTERVAL 29 DAY + INTERVAL 24 HOUR, INTERVAL 28 DAY + INTERVAL 48 HOUR]) i GROUP BY i)`, []any{int64(1)}},
		// query-syntax.md QUALIFY: the filter applies after window
		// functions are computed, so COUNT(*) OVER () sees all 4 rows.
		{"qualify_after_window", `SELECT COUNT(*) OVER (), SUM(x) OVER () FROM UNNEST([1, 2, 3, 3]) x QUALIFY x = 2`, []any{int64(4), int64(9)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := make([]any, len(c.want))
			ptrs := make([]any, len(got))
			for i := range got {
				ptrs[i] = &got[i]
			}
			if err := db.QueryRowContext(context.Background(), c.query).Scan(ptrs...); err != nil {
				t.Fatalf("%s: %v", c.query, err)
			}
			for i := range got {
				if !sameValue(got[i], c.want[i]) {
					t.Errorf("%s column %d = %#v, want %#v", c.query, i+1, got[i], c.want[i])
				}
			}
		})
	}

	// Overflow is an error, not a wrapped or zero result.
	for _, c := range []struct{ query, want string }{
		// analytic_sum_numeric_overflow_1.
		{`SELECT SUM(v) OVER () FROM UNNEST([NUMERIC '99999999999999999999999999999.999999999', NUMERIC '10']) v`, "numeric overflow"},
		// analytic_sum_distinct_overflow_int64_1.
		{`SELECT SUM(DISTINCT v) OVER () FROM UNNEST([9223372036854775807, 2]) v`, "int64 overflow"},
		// analytic_sum_double_overflow_1.
		{`SELECT SUM(v) OVER () FROM UNNEST([1.79769e+308, 1.79769e+308]) v`, "double overflow"},
	} {
		rows, err := db.QueryContext(context.Background(), c.query)
		if err == nil {
			for rows.Next() {
			}
			err = rows.Err()
			rows.Close()
		}
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", c.query, err, c.want)
		}
	}
}

func sameValue(got, want any) bool {
	if w, ok := want.(float64); ok {
		g, ok := got.(float64)
		if !ok {
			return false
		}
		if math.IsNaN(w) {
			return math.IsNaN(g)
		}
		return g == w || math.Abs(g-w) <= 1e-12*math.Abs(w)
	}
	return got == want
}
