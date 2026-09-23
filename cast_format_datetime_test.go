package googlesqlite_test

import (
	"context"
	"database/sql"
	"testing"
)

// TestCastFormatDateTimeToString covers CAST(... AS STRING FORMAT ...)
// for DATE, DATETIME, TIME and TIMESTAMP. Expected values come from the
// Examples in docs/third_party/googlesql-docs/format-elements.md and
// conversion_functions.md. The docs render timestamps in
// America/Los_Angeles; this driver's default zone is UTC (as in
// BigQuery), so the zone-less TZH/TZM cases expect the UTC offset.
func TestCastFormatDateTimeToString(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("googlesqlite", ":memory:?_test=cast_format_datetime")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cases := []struct{ expr, want string }{
		{`CAST(DATE '2018-01-30' AS STRING FORMAT 'YYYY')`, "2018"},
		{`CAST(DATE '2018-01-30' AS STRING FORMAT 'MONTH')`, "JANUARY"},
		{`CAST(DATE '2018-02-15' AS STRING FORMAT 'DD')`, "15"},
		{`CAST(DATE '2024-01-03' AS STRING FORMAT 'DAY')`, "WEDNESDAY"},
		{`CAST(DATE '2024-01-03' AS STRING FORMAT 'Day')`, "Wednesday"},
		{`CAST(DATE '2024-01-03' AS STRING FORMAT 'Mon DD, YYYY')`, "Jan 03, 2024"},
		{`CAST(TIME '21:30:00' AS STRING FORMAT 'HH24')`, "21"},
		{`CAST(TIME '21:30:00' AS STRING FORMAT 'HH12')`, "09"},
		{`CAST(TIME '21:30:00' AS STRING FORMAT 'MI')`, "30"},
		{`CAST(TIME '21:30:25.16' AS STRING FORMAT 'SS')`, "25"},
		{`CAST(TIME '21:30:25.16' AS STRING FORMAT 'FF2')`, "16"},
		{`CAST(TIME '21:30:00' AS STRING FORMAT 'AM')`, "PM"},
		{`CAST(TIME '21:30:00' AS STRING FORMAT 'PM')`, "PM"},
		{`CAST(TIME '01:30:00' AS STRING FORMAT 'AM')`, "AM"},
		{`CAST(TIME '01:30:00' AS STRING FORMAT 'PM')`, "AM"},
		{`CAST(DATETIME '2008-12-25 15:30:00' AS STRING FORMAT 'YYYY-MM-DD HH24:MI:SS')`, "2008-12-25 15:30:00"},
		{`CAST(TIMESTAMP '2008-12-25 00:00:00+00:00' AS STRING FORMAT 'TZH')`, "+00"},
		{`CAST(TIMESTAMP '2008-12-25 00:00:00+00:00' AS STRING FORMAT 'TZH' AT TIME ZONE 'Asia/Kolkata')`, "+05"},
		{`CAST(TIMESTAMP '2008-12-25 00:00:00+00:00' AS STRING FORMAT 'TZM')`, "00"},
		{`CAST(TIMESTAMP '2008-12-25 00:00:00+00:00' AS STRING FORMAT 'TZM' AT TIME ZONE 'Asia/Kolkata')`, "30"},
		{`CAST(TIMESTAMP '2008-12-25 00:00:00+00:00' AS STRING FORMAT 'YYYY-MM-DD HH24:MI:SS TZH:TZM' AT TIME ZONE 'Asia/Kolkata')`, "2008-12-25 05:30:00 +05:30"},
	}
	for _, c := range cases {
		for _, q := range []string{"SELECT " + c.expr} {
			var got string
			if err := db.QueryRowContext(context.Background(), q).Scan(&got); err != nil {
				t.Errorf("%s: %v", q, err)
				continue
			}
			if got != c.want {
				t.Errorf("%s = %q, want %q", q, got, c.want)
			}
		}
	}
}
