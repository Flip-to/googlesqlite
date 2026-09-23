package googlesqlite_test

import (
	"context"
	"database/sql"
	"testing"
)

// TestTimestampNumericLiteralPrecision covers statements that mention
// TIMESTAMP, where the driver disables the analyzer's literal-cast
// folding so that TIMESTAMP casts are evaluated in UTC. Floating-point
// literals cast to NUMERIC/BIGNUMERIC in those statements must still be
// converted from their source image rather than through DOUBLE.
//
// Expected values were verified against BigQuery.
func TestTimestampNumericLiteralPrecision(t *testing.T) {
	db, err := sql.Open("googlesqlite", ":memory:?_test=ts_numeric_literal")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()

	const ts = "CAST(CAST(TIMESTAMP '2024-01-01 03:00:00+00' AS DATE) AS STRING)"
	cases := []struct {
		expr string
		want string
	}{
		{"CAST(CAST(12345678901234567890.123456789 AS NUMERIC) AS STRING)", "12345678901234567890.123456789"},
		{"CAST(SAFE_CAST(12345678901234567890.123456789 AS NUMERIC) AS STRING)", "12345678901234567890.123456789"},
		{"CAST(CAST(-1.5e-9 AS NUMERIC) AS STRING)", "-0.000000002"},
		{"CAST(CAST(1.0000000005 AS NUMERIC) AS STRING)", "1.000000001"},
		{"CAST(CAST(123456789012345678.123456789 AS BIGNUMERIC) AS STRING)", "123456789012345678.123456789"},
		{"CAST(NUMERIC '12345678901234567890.123456789' AS STRING)", "12345678901234567890.123456789"},
		{"CAST(CAST('1.23456789012345678901234567890123456789' AS BIGNUMERIC) AS STRING)", "1.23456789012345678901234567890123456789"},
		{"(SELECT CAST(x AS STRING) FROM UNNEST([CAST(12345678901234567890.123456789 AS NUMERIC)]) x)", "12345678901234567890.123456789"},
		{"(SELECT CAST(x * 1.000000001 AS STRING) FROM UNNEST([NUMERIC '12345678901234567890.123456789']) x)", "12345678913580246791.358024679"},
		{"(SELECT CAST(x + CAST(0.000000001 AS NUMERIC) AS STRING) FROM UNNEST([NUMERIC '99999999999999999999.999999998']) x)", "99999999999999999999.999999999"},
	}
	for _, c := range cases {
		q := "SELECT " + c.expr + ", " + ts
		var got, date string
		if err := db.QueryRowContext(ctx, q).Scan(&got, &date); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		if got != c.want || date != "2024-01-01" {
			t.Errorf("%s = (%q, %q), want (%q, %q)", q, got, date, c.want, "2024-01-01")
		}
	}

	// Column-input form: values written in a statement that mentions
	// TIMESTAMP and read back from a table.
	for _, s := range []string{
		"CREATE TABLE ts_num (n NUMERIC, b BIGNUMERIC, ts TIMESTAMP)",
		"INSERT INTO ts_num VALUES (CAST(12345678901234567890.123456789 AS NUMERIC), CAST(123456789012345678.123456789 AS BIGNUMERIC), TIMESTAMP '2024-01-01 03:00:00+00')",
	} {
		if _, err := db.ExecContext(ctx, s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	var n, b, d string
	if err := db.QueryRowContext(ctx, "SELECT CAST(n AS STRING), CAST(b AS STRING), CAST(DATE(ts) AS STRING) FROM ts_num").Scan(&n, &b, &d); err != nil {
		t.Fatal(err)
	}
	if n != "12345678901234567890.123456789" || b != "123456789012345678.123456789" || d != "2024-01-01" {
		t.Errorf("ts_num = (%q, %q, %q)", n, b, d)
	}
}
