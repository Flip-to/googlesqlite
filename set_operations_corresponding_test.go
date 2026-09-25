package googlesqlite_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// TestSetOperationsCorresponding covers name-based column matching
// (BY NAME, [STRICT] CORRESPONDING [BY], with INNER/FULL/LEFT modes) and
// bag semantics for INTERSECT ALL / EXCEPT ALL, which SQLite lacks
// natively.
//
// Expected values come from the Examples in
// docs/third_party/googlesql-docs/query-syntax.md (set operators) and
// from the GoogleSQL compliance fixtures under compliance/testdata
// (file and case name cited per case). Rows are compared as an
// unordered multiset unless ordered is set, and NULL is rendered as
// "NULL".
func TestSetOperationsCorresponding(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("googlesqlite", ":memory:?_test=setopscorresponding")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	for _, tc := range []struct {
		name string
		sql  string
		cols []string
		rows [][]string
	}{
		// query-syntax.md, UNION examples.
		{
			name: "docs_union_all_by_name",
			sql:  "SELECT 1 AS one_digit, 10 AS two_digit UNION ALL BY NAME SELECT 20 AS two_digit, 2 AS one_digit",
			cols: []string{"one_digit", "two_digit"},
			rows: [][]string{{"1", "10"}, {"2", "20"}},
		},
		{
			name: "docs_union_all_strict_corresponding",
			sql:  "SELECT 1 AS one_digit, 10 AS two_digit UNION ALL STRICT CORRESPONDING SELECT 20 AS two_digit, 2 AS one_digit",
			cols: []string{"one_digit", "two_digit"},
			rows: [][]string{{"1", "10"}, {"2", "20"}},
		},
		{
			name: "docs_inner_union_all_by_name",
			sql:  "SELECT 1 AS one_digit, 10 AS two_digit, 100 AS three_digit INNER UNION ALL BY NAME SELECT 20 AS two_digit, 2 AS one_digit, 1000 AS four_digit",
			cols: []string{"one_digit", "two_digit"},
			rows: [][]string{{"1", "10"}, {"2", "20"}},
		},
		{
			name: "docs_full_outer_union_all_by_name",
			sql:  "SELECT 1 AS one_digit, 10 AS two_digit, 100 AS three_digit FULL OUTER UNION ALL BY NAME SELECT 20 AS two_digit, 2 AS one_digit, 1000 AS four_digit",
			cols: []string{"one_digit", "two_digit", "three_digit", "four_digit"},
			rows: [][]string{{"1", "10", "100", "NULL"}, {"2", "20", "NULL", "1000"}},
		},
		{
			name: "docs_left_outer_union_all_by_name",
			sql:  "SELECT 1 AS one_digit, 10 AS two_digit, 100 AS three_digit LEFT OUTER UNION ALL BY NAME SELECT 20 AS two_digit, 2 AS one_digit, 1000 AS four_digit",
			cols: []string{"one_digit", "two_digit", "three_digit"},
			rows: [][]string{{"1", "10", "100"}, {"2", "20", "NULL"}},
		},
		{
			name: "docs_full_outer_union_all_by_name_on",
			sql:  "SELECT 1 AS one_digit, 10 AS two_digit, 100 AS three_digit FULL OUTER UNION ALL BY NAME ON (three_digit, two_digit) SELECT 20 AS two_digit, 2 AS one_digit, 1000 AS four_digit",
			cols: []string{"three_digit", "two_digit"},
			rows: [][]string{{"100", "10"}, {"NULL", "20"}},
		},
		// query-syntax.md, INTERSECT examples.
		{
			name: "docs_intersect_all",
			sql:  "SELECT * FROM UNNEST(ARRAY<INT64>[1, 2, 3, 3, 4]) AS number INTERSECT ALL SELECT * FROM UNNEST(ARRAY<INT64>[2, 3, 3, 5]) AS number",
			cols: []string{"number"},
			rows: [][]string{{"2"}, {"3"}, {"3"}},
		},
		{
			name: "docs_intersect_distinct_by_name",
			sql: `WITH NumbersTable AS (SELECT 1 AS one_digit, 10 AS two_digit UNION ALL SELECT 2, 20 UNION ALL SELECT 3, 30)
SELECT one_digit, two_digit FROM NumbersTable INTERSECT DISTINCT BY NAME SELECT 10 AS two_digit, 1 AS one_digit`,
			cols: []string{"one_digit", "two_digit"},
			rows: [][]string{{"1", "10"}},
		},
		{
			name: "docs_intersect_distinct_strict_corresponding",
			sql: `WITH NumbersTable AS (SELECT 1 AS one_digit, 10 AS two_digit UNION ALL SELECT 2, 20 UNION ALL SELECT 3, 30)
SELECT one_digit, two_digit FROM NumbersTable INTERSECT DISTINCT STRICT CORRESPONDING SELECT 10 AS two_digit, 1 AS one_digit`,
			cols: []string{"one_digit", "two_digit"},
			rows: [][]string{{"1", "10"}},
		},
		// query-syntax.md, EXCEPT examples.
		{
			name: "docs_except_all",
			sql:  "SELECT * FROM UNNEST(ARRAY<INT64>[1, 2, 3, 3, 4]) AS number EXCEPT ALL SELECT * FROM UNNEST(ARRAY<INT64>[1, 2]) AS number",
			cols: []string{"number"},
			rows: [][]string{{"3"}, {"3"}, {"4"}},
		},
		// except_intersect_queries.test.
		{
			name: "except_intersect_6",
			sql:  "(SELECT 1 UNION ALL SELECT 1) INTERSECT ALL SELECT 1",
			rows: [][]string{{"1"}},
		},
		{
			name: "except_intersect_8",
			sql:  "(SELECT 1 UNION ALL SELECT 1) EXCEPT ALL SELECT 1",
			rows: [][]string{{"1"}},
		},
		{
			name: "except_intersect_10",
			sql:  "(SELECT 1 UNION ALL SELECT 1) INTERSECT ALL (SELECT 1 UNION ALL SELECT 1)",
			rows: [][]string{{"1"}, {"1"}},
		},
		{
			name: "except_intersect_12",
			sql:  "(SELECT 1 UNION ALL SELECT 1) EXCEPT ALL (SELECT 1 UNION ALL SELECT 1)",
			rows: nil,
		},
		{
			name: "except_intersect_20",
			sql:  "SELECT NULL EXCEPT ALL SELECT 2",
			rows: [][]string{{"NULL"}},
		},
		// Three-input INTERSECT ALL / EXCEPT ALL chains. Expected rows
		// follow query-syntax.md: a row appearing m and n times appears
		// MIN(m, n) times after INTERSECT ALL and MAX(m - n, 0) times after
		// EXCEPT ALL, applied left to right. (BigQuery itself rejects ALL.)
		{
			name: "intersect_all_three_inputs",
			sql: "SELECT x FROM UNNEST([1, 1, 1, 2, 2, 3, NULL, NULL]) AS x INTERSECT ALL " +
				"SELECT x FROM UNNEST([1, 1, 2, NULL, NULL, NULL]) AS x INTERSECT ALL " +
				"SELECT x FROM UNNEST([1, 2, 2, NULL]) AS x",
			rows: [][]string{{"1"}, {"2"}, {"NULL"}},
		},
		{
			name: "except_all_three_inputs",
			sql: "SELECT x FROM UNNEST([1, 1, 1, 2, 2, 3, NULL, NULL]) AS x EXCEPT ALL " +
				"SELECT x FROM UNNEST([1, 2, NULL]) AS x EXCEPT ALL " +
				"SELECT x FROM UNNEST([1, 3]) AS x",
			rows: [][]string{{"1"}, {"2"}, {"NULL"}},
		},
		{
			name: "intersect_all_keeps_duplicates",
			sql: "SELECT x FROM UNNEST([4, 4, 4, 5]) AS x INTERSECT ALL " +
				"SELECT x FROM UNNEST([4, 4, 5, 5]) AS x INTERSECT ALL " +
				"SELECT x FROM UNNEST([4, 4, 4, 5]) AS x",
			rows: [][]string{{"4"}, {"4"}, {"5"}},
		},
		// set_operation_full_corresponding_by.test.
		{
			name: "full_corresponding_by_both_scans_have_padded_NULLs_union_all_basic",
			sql:  "SELECT 1 AS a FULL UNION ALL CORRESPONDING BY (b, a) SELECT 1 AS b",
			cols: []string{"b", "a"},
			rows: [][]string{{"NULL", "1"}, {"1", "NULL"}},
		},
		// set_operation_nested.test.
		{
			name: "nested_strict_by_position_left_corresponding",
			sql: `WITH Table1 AS (SELECT CAST(1 AS INT64) AS a, CAST(1 AS INT64) AS b UNION ALL SELECT 1, 1)
(SELECT a, b FROM Table1 EXCEPT ALL SELECT 1 AS b, 1 AS a)
LEFT UNION ALL CORRESPONDING
SELECT 2 AS c, 1 AS b`,
			cols: []string{"a", "b"},
			rows: [][]string{{"1", "1"}, {"NULL", "1"}},
		},
		{
			name: "strict_by_position_nested_left_corresponding",
			sql:  "SELECT 1 AS a, 1 AS b EXCEPT ALL (SELECT 1 AS a, 1 AS b LEFT UNION ALL CORRESPONDING SELECT 1 AS b)",
			cols: []string{"a", "b"},
			rows: nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := db.QueryContext(ctx, tc.sql)
			if err != nil {
				t.Fatalf("query: %v", err)
			}
			defer rows.Close()
			cols, err := rows.Columns()
			if err != nil {
				t.Fatalf("columns: %v", err)
			}
			if tc.cols != nil && !reflect.DeepEqual(cols, tc.cols) {
				t.Fatalf("columns = %v; want %v", cols, tc.cols)
			}
			var got []string
			for rows.Next() {
				vals := make([]any, len(cols))
				ptrs := make([]any, len(cols))
				for i := range vals {
					ptrs[i] = &vals[i]
				}
				if err := rows.Scan(ptrs...); err != nil {
					t.Fatalf("scan: %v", err)
				}
				cells := make([]string, len(vals))
				for i, v := range vals {
					if v == nil {
						cells[i] = "NULL"
					} else {
						cells[i] = fmt.Sprint(v)
					}
				}
				got = append(got, strings.Join(cells, "|"))
			}
			if err := rows.Err(); err != nil {
				t.Fatalf("rows: %v", err)
			}
			var want []string
			for _, r := range tc.rows {
				want = append(want, strings.Join(r, "|"))
			}
			sort.Strings(got)
			sort.Strings(want)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("rows = %v; want %v", got, want)
			}
		})
	}
}
