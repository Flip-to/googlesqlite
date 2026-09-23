package googlesqlite_test

import (
	"database/sql"
	"testing"

	_ "github.com/goccy/googlesqlite"
)

// TestFormatFloat64 covers FORMAT %t and %T on FLOAT64, per
// https://cloud.google.com/bigquery/docs/reference/standard-sql/string_functions#format_string
// (%T for FLOAT64 is "123.0 CAST("nan" AS FLOAT64)"). The expected values
// were measured on BigQuery: an integral value keeps its ".0", up to 15
// significant digits print without an exponent unless the exponent reaches
// 15 or drops below -4, and more digits are printed only when 15 do not
// round-trip. NaN and the infinities are covered in internal/value: a
// CAST('nan' AS FLOAT64) literal fails in the literal encoder before FORMAT
// runs. Integral FLOAT64 elements of ARRAY and STRUCT reach FORMAT as INT64
// on main; with #66 applied, [1.0, 2.5] and (3.0, "a") print as BigQuery
// does through this Format.
func TestFormatFloat64(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("googlesqlite", ":memory:?_test=format_float64")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	for _, tc := range []struct {
		expr string
		want string
	}{
		{"FORMAT('%T', 10.0)", "10.0"},
		{"FORMAT('%t', 10.0)", "10.0"},
		{"FORMAT('%T', 1.5)", "1.5"},
		{"FORMAT('%T', 0.1)", "0.1"},
		{"FORMAT('%T', 0.0001)", "0.0001"},
		{"FORMAT('%T', 0.00001)", "1e-05"},
		{"FORMAT('%T', 1000000.0)", "1000000.0"},
		{"FORMAT('%T', 123456789.0)", "123456789.0"},
		{"FORMAT('%T', 12345678901234.5)", "12345678901234.5"},
		{"FORMAT('%T', 1e15)", "1e+15"},
		{"FORMAT('%T', 1e100)", "1e+100"},
		{"FORMAT('%T', 123456789012345678.0)", "1.2345678901234568e+17"},
	} {
		var got string
		if err := db.QueryRow("SELECT " + tc.expr).Scan(&got); err != nil {
			t.Errorf("%s: %v", tc.expr, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.expr, got, tc.want)
		}
	}
}
