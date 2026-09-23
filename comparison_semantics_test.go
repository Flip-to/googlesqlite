package googlesqlite_test

import (
	"context"
	"database/sql"
	"testing"
)

// TestComparisonSemantics pins NULL and NaN handling in comparisons and
// ordering. Expected values come from docs/third_party/googlesql-docs
// (operators.md: IN, comparison and IS DISTINCT FROM; data-types.md:
// floating point ordering) and the compliance fixtures named per case.
func TestComparisonSemantics(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("googlesqlite", ":memory:?_test=comparison_semantics")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cases := []struct {
		name  string
		query string
		want  any
	}{
		// in_queries.test in_clause_in_select / in_clause_not_in_select.
		{"in_with_null", `SELECT 3 IN (1, 2, NULL)`, nil},
		{"not_in_with_null", `SELECT 3 NOT IN (1, 2, NULL)`, nil},
		{"in_match_with_null", `SELECT 2 IN (1, 2, NULL)`, true},
		// operators.md IN: FALSE for an empty set, NULL for a NULL
		// search value otherwise.
		{"in_unnest_empty", `SELECT CAST(NULL AS INT64) IN UNNEST(CAST([] AS ARRAY<INT64>))`, false},
		{"in_unnest_null_array", `SELECT 1 IN UNNEST(CAST(NULL AS ARRAY<INT64>))`, false},
		{"in_unnest_null_element", `SELECT 3 IN UNNEST([1, NULL])`, nil},
		{"in_unnest_null_search", `SELECT CAST(NULL AS INT64) IN UNNEST([1])`, nil},
		// interval.test cmp_struct: a NULL field makes STRUCT = NULL,
		// unless another field already differs.
		{"struct_eq_null_field", `SELECT STRUCT(1, 2) = STRUCT(1, NULL)`, nil},
		{"struct_eq_null_but_differs", `SELECT STRUCT(1, 2) = STRUCT(2, NULL)`, false},
		// comparison_functions.test is_distinct_nan: NaN is not distinct
		// from NaN, also inside a STRUCT.
		{"nan_not_distinct", `SELECT IEEE_DIVIDE(0, 0) IS NOT DISTINCT FROM IEEE_DIVIDE(0, 0)`, true},
		{"nan_distinct", `SELECT IEEE_DIVIDE(0, 0) IS DISTINCT FROM IEEE_DIVIDE(0, 0)`, false},
		{"struct_nan_not_distinct", `SELECT STRUCT(IEEE_DIVIDE(0, 0)) IS NOT DISTINCT FROM STRUCT(IEEE_DIVIDE(0, 0))`, true},
		// data-types.md: ascending order is NULL, NaN, -inf, ..., +inf.
		{"order_nan_asc", `SELECT STRING_AGG(IFNULL(CAST(x AS STRING), 'NULL'), ',' ORDER BY x) FROM UNNEST([1.0, CAST('nan' AS FLOAT64), NULL, CAST('-inf' AS FLOAT64)]) x`, "NULL,nan,-inf,1"},
		{"order_by_nan_asc", `SELECT ARRAY_TO_STRING(ARRAY(SELECT IFNULL(CAST(x AS STRING), 'NULL') FROM UNNEST([1.0, CAST('nan' AS FLOAT64), NULL, CAST('-inf' AS FLOAT64)]) x ORDER BY x), ',')`, "NULL,nan,-inf,1"},
		{"order_by_nan_desc", `SELECT ARRAY_TO_STRING(ARRAY(SELECT IFNULL(CAST(x AS STRING), 'NULL') FROM UNNEST([1.0, CAST('nan' AS FLOAT64), NULL, CAST('-inf' AS FLOAT64)]) x ORDER BY x DESC), ',')`, "1,-inf,nan,NULL"},
		{"window_order_nan", `SELECT ARRAY_TO_STRING(ARRAY(SELECT CAST(ROW_NUMBER() OVER (ORDER BY x) AS STRING) FROM UNNEST([CAST('nan' AS FLOAT64), 1.0]) x ORDER BY x), ',')`, "1,2"},
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
}
