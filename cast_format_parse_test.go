package googlesqlite_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestCastFormatStringToDateTime covers CAST(STRING AS DATE / DATETIME /
// TIME / TIMESTAMP FORMAT ...). The cases are the Examples of the
// "Format string as date and time" section of
// docs/third_party/googlesql-docs/format-elements.md and the
// CAST ... AS TIMESTAMP FORMAT examples of conversion_functions.md.
//
// Several upstream examples are internally inconsistent with the rules
// in the same table; those cases follow the rule text, which is what
// BigQuery returns:
//   - MM-DD-YYYY '03-12-2018' is 2018-03-12 (the doc prints 2018-12-03).
//   - YYYY-MMDD '10000-1203' is outside the supported DATE range and
//     errors.
//   - YYYY '18' sets the year to 18 with the current month and day 1
//     (the doc prints 2018-03-01).
//   - Y, YYY, YY and RR depend on the current year; the expected values
//     are derived from the rule text using the current year.
//   - Y,YYY '2,018-12-03' is 2018-12-03 (the doc prints 2008-12-03).
//   - FF1 matches exactly one digit, so '01:05:07.16' with
//     HH24:MI:SS.FF1 has trailing data and errors (the doc prints
//     01:05:07.2). The FF3 example drops its stray 'FF3: ' prefix.
//   - The TZM example's format is written as HH24:MI:SSTZH.TZM to
//     match its input.
//   - 'YYYY.MM.DD HH:MI:SSTZH' uses HH without a meridian indicator,
//     which the format model rules forbid, so it errors; the HH24
//     variant gives the documented instant.
//
// The docs assume America/Los_Angeles as the default time zone; this
// driver's default is UTC (as in BigQuery).
func TestCastFormatStringToDateTime(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("googlesqlite", ":memory:?_test=cast_format_parse")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Now().UTC()
	year, month := now.Year(), int(now.Month())
	lastDigits := func(v, mod int) int { return year/mod*mod + v }
	rr := func(v int) int {
		century, cur := year/100*100, year%100
		switch {
		case v < 50 && cur >= 50:
			century += 100
		case v >= 50 && cur < 50:
			century -= 100
		}
		return century + v
	}
	date := func(y, m, d int) string { return fmt.Sprintf("DATE '%04d-%02d-%02d'", y, m, d) }

	// want is a literal the result must equal; an empty want means the
	// cast must fail (and SAFE_CAST must return NULL).
	cases := []struct{ expr, want string }{
		// Year part.
		{`'03-12-2018' AS DATE FORMAT 'MM-DD-YYYY'`, `DATE '2018-03-12'`},
		{`'10000-1203' AS DATE FORMAT 'YYYY-MMDD'`, ``},
		{`'18' AS DATE FORMAT 'YYYY'`, date(18, month, 1)},
		{`'018-12-03' AS DATE FORMAT 'YYY-MM-DD'`, date(lastDigits(18, 1000), 12, 3)},
		{`'038-12-03' AS DATE FORMAT 'YYY-MM-DD'`, date(lastDigits(38, 1000), 12, 3)},
		{`'18-12-03' AS DATE FORMAT 'YY-MM-DD'`, date(lastDigits(18, 100), 12, 3)},
		{`'38-12-03' AS DATE FORMAT 'YY-MM-DD'`, date(lastDigits(38, 100), 12, 3)},
		{`'8-12-03' AS DATE FORMAT 'Y-MM-DD'`, date(lastDigits(8, 10), 12, 3)},
		{`'2,018-12-03' AS DATE FORMAT 'Y,YYY-MM-DD'`, `DATE '2018-12-03'`},
		{`'18-12-03' AS DATE FORMAT 'RR-MM-DD'`, date(rr(18), 12, 3)},
		{`'50-12-03' AS DATE FORMAT 'RR-MM-DD'`, date(rr(50), 12, 3)},
		{`'18-12-03' AS DATE FORMAT 'YY-MM-DD'`, date(lastDigits(18, 100), 12, 3)},
		// Month part.
		{`'DEC 03, 2018' AS DATE FORMAT 'MON DD, YYYY'`, `DATE '2018-12-03'`},
		{`'DECEMBER 03, 2018' AS DATE FORMAT 'MONTH DD, YYYY'`, `DATE '2018-12-03'`},
		// Hour part.
		{`'03:30 P.M.' AS TIME FORMAT 'HH:MI P.M.'`, `TIME '15:30:00'`},
		{`'15:30' AS TIME FORMAT 'HH24:MI'`, `TIME '15:30:00'`},
		// Second part.
		{`'03:30:02 P.M.' AS TIME FORMAT 'HH:MI:SS P.M.'`, `TIME '15:30:02'`},
		{`'03723' AS TIME FORMAT 'SSSSS'`, `TIME '01:02:03'`},
		{`'01:05:07.16' AS TIME FORMAT 'HH24:MI:SS.FF1'`, ``},
		{`'01:05:07.16' AS TIME FORMAT 'HH24:MI:SS.FF2'`, `TIME '01:05:07.16'`},
		{`'01:05:07.16' AS TIME FORMAT 'HH24:MI:SS.FF3'`, `TIME '01:05:07.160'`},
		// Meridian indicator part.
		{`'03:30 A.M.' AS TIME FORMAT 'HH:MI A.M.'`, `TIME '03:30:00'`},
		{`'03:30 P.M.' AS TIME FORMAT 'HH:MI P.M.'`, `TIME '15:30:00'`},
		{`'03:30 A.M.' AS TIME FORMAT 'HH:MI P.M.'`, `TIME '03:30:00'`},
		{`'03:30 P.M.' AS TIME FORMAT 'HH:MI A.M.'`, `TIME '15:30:00'`},
		{`'03:30 a.m.' AS TIME FORMAT 'HH:MI a.m.'`, `TIME '03:30:00'`},
		// Time zone part.
		{`'2008-12-25 05:30:00-08' AS TIMESTAMP FORMAT 'YYYY-MM-DD HH24:MI:SSTZH'`, `TIMESTAMP '2008-12-25 05:30:00-08'`},
		{`'2008-12-25 05:30:00+05.30' AS TIMESTAMP FORMAT 'YYYY-MM-DD HH24:MI:SSTZH.TZM'`, `TIMESTAMP '2008-12-25 05:30:00+05:30'`},
		{`'2020.06.03 00:00:53+00' AS TIMESTAMP FORMAT 'YYYY.MM.DD HH:MI:SSTZH'`, ``},
		{`'2020.06.03 00:00:53+00' AS TIMESTAMP FORMAT 'YYYY.MM.DD HH24:MI:SSTZH'`, `TIMESTAMP '2020-06-03 00:00:53+00'`},
		// conversion_functions.md.
		{`'06/02/2020 17:00:53.110' AS TIMESTAMP FORMAT 'MM/DD/YYYY HH24:MI:SS.FF3' AT TIME ZONE 'UTC'`, `TIMESTAMP '2020-06-02 17:00:53.110+00'`},
		{`'06/02/2020 17:00:53.110' AS TIMESTAMP FORMAT 'MM/DD/YYYY HH24:MI:SS.FF3' AT TIME ZONE '+00'`, `TIMESTAMP '2020-06-02 17:00:53.110+00'`},
		{`'06/02/2020 17:00:53.110 +00' AS TIMESTAMP FORMAT 'MM/DD/YYYY HH24:MI:SS.FF3 TZH'`, `TIMESTAMP '2020-06-02 17:00:53.110+00'`},
		// Format model rules.
		{`'2018' AS DATE FORMAT 'YYYY HH24'`, ``},
		{`'12:30' AS TIME FORMAT 'HH12:MI'`, ``},
		{`'2018-12-03 2018' AS DATE FORMAT 'YYYY-MM-DD YYYY'`, ``},
		{`'2018-12-03 12:00:00' AS DATETIME FORMAT 'YYYY-MM-DD SSSSS'`, ``},
		{`'2018-045' AS DATE FORMAT 'YYYY-DDD'`, ``},
		{`'03:30 PM' AS TIME FORMAT 'HH:MI AM'`, ``},
		// Missing parts and literals.
		{`'2018-12' AS DATE FORMAT 'YYYY-MM'`, `DATE '2018-12-01'`},
		{`'2018-12-03' AS DATETIME FORMAT 'YYYY-MM-DD'`, `DATETIME '2018-12-03 00:00:00'`},
		{`'2018-12-03T10' AS DATETIME FORMAT 'YYYY-MM-DD"T"HH24'`, `DATETIME '2018-12-03 10:00:00'`},
		{`'2018  12 03' AS DATE FORMAT 'YYYY MM DD'`, `DATE '2018-12-03'`},
		{`'2018/12/03' AS DATE FORMAT 'YYYY-MM-DD'`, ``},
		{`'2018-02-30' AS DATE FORMAT 'YYYY-MM-DD'`, ``},
		{`'2018-12-03 10' AS TIMESTAMP FORMAT 'YYYY-MM-DD HH24' AT TIME ZONE 'America/Los_Angeles'`, `TIMESTAMP '2018-12-03 18:00:00+00'`},
	}
	ctx := context.Background()
	for _, c := range cases {
		if c.want == "" {
			var got sql.NullString
			q := "SELECT CAST(CAST(" + c.expr + ") AS STRING)"
			if err := db.QueryRowContext(ctx, q).Scan(&got); err == nil {
				t.Errorf("%s: expected error, got %v", q, got)
			}
			q = "SELECT CAST(SAFE_CAST(" + c.expr + ") AS STRING)"
			if err := db.QueryRowContext(ctx, q).Scan(&got); err != nil {
				t.Errorf("%s: %v", q, err)
			} else if got.Valid {
				t.Errorf("%s = %q, want NULL", q, got.String)
			}
			continue
		}
		q := "SELECT CAST(CAST(" + c.expr + ") AS STRING), CAST(" + c.expr + ") = " + c.want
		var got string
		var eq bool
		if err := db.QueryRowContext(ctx, q).Scan(&got, &eq); err != nil {
			t.Errorf("%s: %v", q, err)
			continue
		}
		if !eq {
			t.Errorf("%s = %q, want %s", strings.SplitN(q, ",", 2)[0], got, c.want)
		}
	}
}
