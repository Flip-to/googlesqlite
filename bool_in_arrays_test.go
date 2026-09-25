package googlesqlite_test

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/goccy/googlesqlite"
)

// TestBoolInArrays covers BOOL column values collected into ARRAY and
// STRUCT values (ARRAY_AGG, ARRAY subqueries, array and struct
// constructors, ARRAY_CONCAT_AGG, windowed ARRAY_AGG). SQLite stores
// BOOL as INTEGER, so these used to render as 1/0. Every expected value
// was measured on BigQuery.
func TestBoolInArrays(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("googlesqlite", ":memory:?_test=bool_in_arrays")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	for _, tc := range []struct {
		name  string
		query string
		want  string
	}{
		{"array_agg", `SELECT FORMAT('%T', ARRAY_AGG(b)) FROM UNNEST([true, false]) b`, `[true, false]`},
		{"array subquery", `SELECT FORMAT('%T', ARRAY(SELECT b FROM UNNEST([true, false]) b))`, `[true, false]`},
		{"to_json_string array_agg", `SELECT TO_JSON_STRING(ARRAY_AGG(b)) FROM UNNEST([true, false]) b`, `[true,false]`},
		{"struct of array_agg", `SELECT FORMAT('%T', STRUCT(ARRAY_AGG(b) AS x)) FROM UNNEST([true, false]) b`, `STRUCT([true, false])`},
		{"array_concat_agg", `SELECT FORMAT('%T', ARRAY_CONCAT_AGG([b])) FROM UNNEST([true, false]) b`, `[true, false]`},
		{"array_concat_agg two elems", `SELECT FORMAT('%T', ARRAY_CONCAT_AGG([b, NOT b])) FROM UNNEST([true, false]) b`, `[true, false, false, true]`},
		{"array_agg of structs", `SELECT FORMAT('%T', ARRAY_AGG(STRUCT(b AS x, 1 AS y))) FROM UNNEST([true, false]) b`, `[(true, 1), (false, 1)]`},
		{"array constructor", `SELECT FORMAT('%T', [b, NOT b]) FROM UNNEST([true]) b`, `[true, false]`},
		{"struct constructor", `SELECT FORMAT('%T', STRUCT(b AS x)) FROM UNNEST([true]) b`, `STRUCT(true)`},
		{"array subquery as struct", `SELECT FORMAT('%T', ARRAY(SELECT AS STRUCT b FROM UNNEST([true]) b))`, `[STRUCT(true)]`},
		{"array_agg of expression", `SELECT FORMAT('%T', ARRAY_AGG(b > 0)) FROM UNNEST([1]) b`, `[true]`},
		{"array_agg ignore nulls", `SELECT FORMAT('%T', ARRAY_AGG(b IGNORE NULLS)) FROM UNNEST([true, NULL]) b`, `[true]`},
		{"format bool column", `SELECT FORMAT('%T %t', b, b) FROM UNNEST([true]) b`, `true true`},
		{"literal array", `SELECT FORMAT('%T', [true, false])`, `[true, false]`},
		{"array_length", `SELECT CAST(ARRAY_LENGTH(ARRAY_AGG(b)) AS STRING) FROM UNNEST([true, false]) b`, `2`},
		{"element comparison", `SELECT FORMAT('%T', ARRAY_AGG(b ORDER BY b)[OFFSET(1)] = true) FROM UNNEST([true, false]) b`, `true`},
		{"unnest back to bool", `SELECT STRING_AGG(FORMAT('%T', NOT x), ',' ORDER BY o) FROM UNNEST((SELECT ARRAY_AGG(b ORDER BY b) FROM UNNEST([true, false]) b)) x WITH OFFSET o`, `true,false`},
		{"countif over unnest", `SELECT CAST(COUNTIF(x) AS STRING) FROM UNNEST((SELECT ARRAY_AGG(b) FROM UNNEST([true, false, true]) b)) x`, `2`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			if err := db.QueryRowContext(ctx, tc.query).Scan(&got); err != nil {
				t.Fatalf("query %q: %v", tc.query, err)
			}
			if got != tc.want {
				t.Errorf("query %q: got %q, want %q", tc.query, got, tc.want)
			}
		})
	}

	t.Run("windowed array_agg", func(t *testing.T) {
		rows, err := db.QueryContext(ctx, `SELECT FORMAT('%T', ARRAY_AGG(b) OVER (ORDER BY b)) FROM UNNEST([true, false]) b ORDER BY b`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var got []string
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				t.Fatal(err)
			}
			got = append(got, s)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		want := []string{`[false]`, `[false, true]`}
		if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}
