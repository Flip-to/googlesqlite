package googlesqlite_test

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	_ "github.com/goccy/googlesqlite"
)

// Regression cases for LIKE ANY/SOME/ALL (array and subquery forms),
// COLLATE 'und:ci' collation, and JSON query functions. Expected values
// come from the GoogleSQL compliance fixtures (compliance/testdata/
// like_any.test, like_all.test, collation.test, json_queries.test; the
// case name is cited per entry) and docs/third_party/googlesql-docs
// (operators.md, collation-concepts.md, json_functions.md).
func TestLikeAnyCollationJSON(t *testing.T) {
	db, err := sql.Open("googlesqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	for _, q := range []string{
		// collation.test prepare_database (subset of StringTable).
		`CREATE TABLE StringTable(primary_Key INT64, col_ci STRING COLLATE 'und:ci', col_binary STRING COLLATE 'binary') AS
SELECT 2 primary_Key, "hello" col_ci, "a" col_binary UNION ALL
SELECT 4, "@", "A" UNION ALL
SELECT 14, "Hello", "a" UNION ALL
SELECT 16, "hel۝lo", "a" UNION ALL
SELECT 17, "h܏ello", "a"`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}

	for _, tc := range []struct {
		name  string
		query string
		want  []any
		err   string
	}{
		{
			// like_any.test like_any_array_with_array_agg_function (first two columns).
			name:  "like_any_array_with_array_agg_function",
			query: `SELECT 'a' LIKE ANY UNNEST((SELECT ARRAY_AGG(x) FROM UNNEST(['b', NULL]) x)), 'a' LIKE ANY UNNEST((SELECT ARRAY_AGG(x) FROM UNNEST(['a', NULL]) x))`,
			want:  []any{nil, true},
		},
		{
			// like_all.test like_all_array_with_array_agg_function (first two columns).
			name:  "like_all_array_with_array_agg_function",
			query: `SELECT 'a' LIKE ALL UNNEST((SELECT ARRAY_AGG(x) FROM UNNEST(['b', NULL]) x)), 'a' LIKE ALL UNNEST((SELECT ARRAY_AGG(x) FROM UNNEST(['a', NULL]) x))`,
			want:  []any{false, nil},
		},
		{
			// like_any.test like_any_collation_ci_with_underscore_error.
			name:  "like_any_collation_ci_with_underscore_error",
			query: `SELECT collate('Google', 'und:ci') LIKE ANY ('G__gle')`,
			err:   "LIKE pattern has '_' which is not allowed when its operands have collation",
		},
		{
			// collation.test collate_function_with_valid_second_argument.
			name: "collate_function_with_valid_second_argument",
			query: `SELECT collate('abc', 'und:ci'), collate('abc', 'und:ci') = 'ABC', collate('abc', 'binary') = 'ABC',
  replace(collate('hello', 'und:ci'), 'HELLO', 'hi')`,
			want: []any{"abc", true, false, "hi"},
		},
		{
			// collation.test strpos_with_collation_ci / starts_with_with_collation_ci.
			name:  "strpos_starts_with_collation_ci",
			query: `SELECT strpos(collate(',,A܏bc,..', 'und:ci'), 'ABC'), starts_with(collate('A܏bc,..', 'und:ci'), 'ABC')`,
			want:  []any{int64(3), true},
		},
		{
			// collation.test like_with_collation_ci (subset).
			name:  "like_with_collation_ci",
			query: `SELECT collate('abc', 'und:ci') LIKE 'ABC', collate('abc', 'und:ci') LIKE '%B%'`,
			want:  []any{true, true},
		},
		{
			// collation.test min_max_with_column_collation_ci.
			name:  "min_max_with_column_collation_ci",
			query: `WITH TestTable AS (SELECT 'B' col_ci UNION ALL SELECT 'a' UNION ALL SELECT 'aBc' UNION ALL SELECT 'Hello') SELECT min(collate(col_ci,'und:ci')), max(collate(col_ci,'und:ci')) FROM TestTable`,
			want:  []any{"a", "Hello"},
		},
		{
			// collation.test count_distinct_with_collation_ci.
			name:  "count_distinct_with_collation_ci",
			query: `WITH TestTable AS (SELECT 'aB' col UNION ALL SELECT 'AB' UNION ALL SELECT 'ab' UNION ALL SELECT 'Hello' UNION ALL SELECT 'heLLo' UNION ALL SELECT 'hel۝lo') SELECT count(DISTINCT collate(col, 'und:ci')) FROM TestTable`,
			want:  []any{int64(2)},
		},
		{
			// collation.test groupby_with_column_collation_ci (column collation from CREATE TABLE).
			name:  "groupby_with_column_collation_ci",
			query: `SELECT count(col_ci) FROM StringTable WHERE primary_Key IN (2, 14, 16, 17) GROUP BY col_ci`,
			want:  []any{int64(4)},
		},
		{
			// collation.test comparison_with_column_collation_ci_eq (subset of rows).
			name:  "comparison_with_column_collation_ci_eq",
			query: `SELECT count(*) FROM StringTable WHERE col_ci = 'HELLO'`,
			want:  []any{int64(4)},
		},
		{
			// json_queries.test json_extract.
			name:  "json_extract",
			query: `SELECT TO_JSON_STRING(JSON_EXTRACT(JSON '{"a": [1, {"b.c": [10, 20]}]}', "$[a][1]['b.c']"))`,
			want:  []any{"[10,20]"},
		},
		{
			// json_queries.test json_object_basic.
			name:  "json_object_basic",
			query: `SELECT TO_JSON_STRING(JSON_OBJECT("a", "foo", "b", true, "c", NULL))`,
			want:  []any{`{"a":"foo","b":true,"c":null}`},
		},
		{
			// json_queries.test json_object_arrays_basic.
			name:  "json_object_arrays_basic",
			query: `SELECT TO_JSON_STRING(JSON_OBJECT(["a", "b", "c"], [10, 15, -20]))`,
			want:  []any{`{"a":10,"b":15,"c":-20}`},
		},
		{
			// json_queries.test json_set_create_if_missing_false.
			name:  "json_set_create_if_missing_false",
			query: `SELECT TO_JSON_STRING(JSON_SET(JSON '{"a": 10}', "$.a", "foo", "$.b", 20, create_if_missing=>false))`,
			want:  []any{`{"a":"foo"}`},
		},
		{
			// json_queries.test json_int64_double_without_fractional_part.
			name:  "json_int64_double_without_fractional_part",
			query: `SELECT int64(JSON '10.0')`,
			want:  []any{int64(10)},
		},
		{
			// json_queries.test json_string_num_input.
			name:  "json_string_num_input",
			query: `SELECT string(JSON '123')`,
			err:   "The provided JSON input is not a string",
		},
		{
			// json_queries.test json_keys_basic_with_invalid_depth_and_mode.
			name:  "json_keys_basic_with_invalid_depth_and_mode",
			query: `SELECT JSON_KEYS(JSON '[[{"a":{"b":1}}]]', -1, mode => "invalid")`,
			err:   "max_depth must be positive",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := db.Query(tc.query)
			if err == nil {
				defer rows.Close()
			}
			var got []any
			if err == nil {
				cols, _ := rows.Columns()
				if !rows.Next() {
					err = rows.Err()
				} else {
					vals := make([]any, len(cols))
					ptrs := make([]any, len(cols))
					for i := range vals {
						ptrs[i] = &vals[i]
					}
					err = rows.Scan(ptrs...)
					got = vals
				}
			}
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("expected error containing %q, got %v", tc.err, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Fatalf("(-want +got):\n%s", diff)
			}
		})
	}
}
