package googlesqlite_test

import (
	"database/sql"
	"testing"

	_ "github.com/goccy/googlesqlite"
)

// TestIsTrueIsFalseNeverNull covers IS [NOT] TRUE and IS [NOT] FALSE, per
// https://cloud.google.com/bigquery/docs/reference/standard-sql/operators#is_operators
// ("X IS TRUE: Evaluates to TRUE if X evaluates to TRUE. Otherwise,
// evaluates to FALSE."): none of the four ever returns NULL. IS TRUE and
// IS FALSE are FALSE on NULL, IS NOT TRUE and IS NOT FALSE are TRUE on NULL.
// NULL used to propagate, so WHERE x IS NOT TRUE dropped the NULL rows.
func TestIsTrueIsFalseNeverNull(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("googlesqlite", ":memory:?_test=is_true_null")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	for _, tc := range []struct {
		expr string
		want bool
	}{
		{"CAST(NULL AS BOOL) IS TRUE", false},
		{"CAST(NULL AS BOOL) IS NOT TRUE", true},
		{"CAST(NULL AS BOOL) IS FALSE", false},
		{"CAST(NULL AS BOOL) IS NOT FALSE", true},
		{"TRUE IS TRUE", true},
		{"TRUE IS NOT TRUE", false},
		{"TRUE IS FALSE", false},
		{"TRUE IS NOT FALSE", true},
		{"FALSE IS TRUE", false},
		{"FALSE IS NOT TRUE", true},
		{"FALSE IS FALSE", true},
		{"FALSE IS NOT FALSE", false},
	} {
		var got sql.NullBool
		if err := db.QueryRow("SELECT " + tc.expr).Scan(&got); err != nil {
			t.Errorf("%s: %v", tc.expr, err)
			continue
		}
		if !got.Valid || got.Bool != tc.want {
			t.Errorf("%s: got %v (valid=%v), want %v", tc.expr, got.Bool, got.Valid, tc.want)
		}
	}

	var n int
	if err := db.QueryRow(
		"SELECT COUNT(*) FROM UNNEST([TRUE, FALSE, NULL]) AS x WHERE x IS NOT TRUE",
	).Scan(&n); err != nil {
		t.Fatalf("WHERE x IS NOT TRUE: %v", err)
	}
	if n != 2 {
		t.Errorf("WHERE x IS NOT TRUE over [TRUE, FALSE, NULL]: got %d rows, want 2", n)
	}
}
