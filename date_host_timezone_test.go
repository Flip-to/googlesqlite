package googlesqlite_test

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

// TestDateIgnoresHostTimeZone pins DATE handling to civil dates. The
// driver used to build DATE values with time.Unix in the host's local
// zone, so on a host west of UTC every DATE literal came back one day
// early. DATE_FROM_UNIX_DATE also multiplied days into a time.Duration,
// which overflows outside roughly 1677..2262.
//
// Expected values come from the upstream docs examples
// (docs/third_party/googlesql-docs/date_functions.md: UNIX_DATE and
// DATE_FROM_UNIX_DATE) and the GoogleSQL compliance fixture
// compliance/testdata/date.test (date_from_unix_date_1, unix_date_1).
//
// The test swaps time.Local, so it must not run in parallel.
func TestDateIgnoresHostTimeZone(t *testing.T) {
	saved := time.Local
	time.Local = time.FixedZone("UTC-3", -3*60*60)
	t.Cleanup(func() { time.Local = saved })

	db, err := sql.Open("googlesqlite", ":memory:?_test=date_host_tz")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ctx := context.Background()
	cases := []struct {
		query string
		want  any
	}{
		{"SELECT UNIX_DATE(DATE '2008-12-25')", int64(14238)},
		{"SELECT CAST(DATE_FROM_UNIX_DATE(14238) AS STRING)", "2008-12-25"},
		{"SELECT UNIX_DATE(DATE '2014-01-01')", int64(16071)},
		{"SELECT CAST(DATE '2014-01-01' AS STRING)", "2014-01-01"},
		{"SELECT CAST(DATE_FROM_UNIX_DATE(-719162) AS STRING)", "0001-01-01"},
		{"SELECT CAST(DATE_FROM_UNIX_DATE(0) AS STRING)", "1970-01-01"},
		{"SELECT CAST(DATE_FROM_UNIX_DATE(2932896) AS STRING)", "9999-12-31"},
		// civil_time.test convert_timestamp_to_date and
		// default_timezone_utc.test cast_timestamp_to_date. The UNNEST
		// keeps the analyzer from constant-folding the conversion.
		{"SELECT CAST(DATE(ts) AS STRING) FROM UNNEST([TIMESTAMP '0001-01-01 00:00:00+00']) ts", "0001-01-01"},
		{"SELECT CAST(CAST(ts AS DATE) AS STRING) FROM UNNEST([TIMESTAMP '1970-01-01 00:00:01+00']) ts", "1970-01-01"},
		// civil_time.test current_datetime_2.
		{"SELECT current_datetime = datetime(current_timestamp)", true},
	}
	for _, c := range cases {
		var got any
		if err := db.QueryRowContext(ctx, c.query).Scan(&got); err != nil {
			t.Fatalf("%s: %v", c.query, err)
		}
		if got != c.want {
			t.Errorf("%s = %#v, want %#v", c.query, got, c.want)
		}
	}
}
