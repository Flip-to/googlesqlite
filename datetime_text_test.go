package googlesqlite_test

import (
	"context"
	"database/sql"
	"testing"
)

// TestDateTimeTextFraction checks that DATETIME, TIME and TIMESTAMP text
// prints fractional seconds in groups of three digits, as BigQuery does.
// Expected values were returned by real BigQuery.
func TestDateTimeTextFraction(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("googlesqlite", ":memory:?_test=datetime_text")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, c := range []struct{ expr, want string }{
		{`CAST(DATETIME '2024-01-01 12:34:06' AS STRING)`, "2024-01-01 12:34:06"},
		{`CAST(DATETIME '2024-01-01 12:34:06.5' AS STRING)`, "2024-01-01 12:34:06.500"},
		{`CAST(DATETIME '2024-01-01 12:34:06.7891' AS STRING)`, "2024-01-01 12:34:06.789100"},
		{`CAST(TIME '12:34:06.5' AS STRING)`, "12:34:06.500"},
		{`CAST(TIME '12:34:06' AS STRING)`, "12:34:06"},
		{`CAST(TIMESTAMP '2024-01-01 12:34:06.5+00' AS STRING)`, "2024-01-01 12:34:06.500+00"},
		{`FORMAT('%t', DATETIME '2024-01-01 12:34:06.5')`, "2024-01-01 12:34:06.500"},
		{`FORMAT('%T', TIME '12:34:06.5')`, `TIME "12:34:06.500"`},
		{`TO_JSON_STRING(DATETIME '2024-01-01 12:34:06.5')`, `"2024-01-01T12:34:06.500"`},
		{`STRING(TIMESTAMP '2008-12-25 00:00:00+00', 'Asia/Kolkata')`, "2008-12-25 05:30:00+05:30"},
		{`STRING(TIMESTAMP '2008-12-25 00:00:00+00', 'America/Los_Angeles')`, "2008-12-24 16:00:00-08"},
	} {
		for _, q := range []string{"SELECT " + c.expr} {
			var got string
			if err := db.QueryRowContext(context.Background(), q).Scan(&got); err != nil {
				t.Errorf("%s: %v", q, err)
			} else if got != c.want {
				t.Errorf("%s = %q, want %q", q, got, c.want)
			}
		}
	}
}
