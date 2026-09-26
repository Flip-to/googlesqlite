package googlesqlite_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

// TestIntervalCompliance pins INTERVAL semantics. Expected values come
// from the GoogleSQL compliance fixture compliance/testdata/interval.test
// (case names cited) and the Examples in
// docs/third_party/googlesql-docs/interval_functions.md. INTERVAL
// results are rendered with CAST(... AS STRING), the canonical
// Y-M D H:M:S[.F] form the fixtures use.
func TestIntervalCompliance(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("googlesqlite", ":memory:?_test=interval_compliance")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	const intervals = `(SELECT 1 id, CAST(NULL AS INTERVAL) value UNION ALL
SELECT 2, interval 0 year UNION ALL
SELECT 3, interval '0.000001' second UNION ALL
SELECT 4, interval -1 second UNION ALL
SELECT 5, interval 1 month UNION ALL
SELECT 6, interval 30 day UNION ALL
SELECT 7, interval 720 hour UNION ALL
SELECT 8, interval 10000 year UNION ALL
SELECT 9, interval '1-2 3 4:5:6.789' year to second UNION ALL
SELECT 10, interval 2 hour UNION ALL
SELECT 11, interval '1:59:59.999999' hour to second UNION ALL
SELECT 12, interval '1:00:00.000001' hour to second UNION ALL
SELECT 13, interval '-0.000001' second UNION ALL
SELECT 14, interval '-4:5:6.789' hour to second)`
	s := func(expr string) string { return "CAST(" + expr + " AS STRING)" }
	list := func(exprs ...string) string {
		out := make([]string, len(exprs))
		for i, e := range exprs {
			out[i] = s(e)
		}
		return strings.Join(out, ", ")
	}

	cases := []struct {
		name  string
		query string
		want  [][]string // rows of fmt.Sprint'ed columns; "NULL" for NULL
	}{
		// interval.test multiply_divide.
		{"multiply_divide", `SELECT ` + list(`interval '1' month * 20`, `interval '2' hour / 4`),
			[][]string{{"1-8 0 0:0:0", "0-0 0 0:30:0"}}},
		// interval.test divide_fractional_micros_without_nanos_feature.
		{"divide_fractional_micros", `SELECT ` + list(
			`(interval '0.000008' SECOND) / 3`, `(interval '0.000008' SECOND) / 4`, `(interval '0.000008' SECOND) / 6`,
			`(interval '0.000008' SECOND) / -3`, `(interval '0.000008' SECOND) / -4`, `(interval '0.000008' SECOND) / -6`),
			[][]string{{"0-0 0 0:0:0.000002", "0-0 0 0:0:0.000002", "0-0 0 0:0:0.000001", "0-0 0 -0:0:0.000002", "0-0 0 -0:0:0.000002", "0-0 0 -0:0:0.000001"}}},
		// interval.test constructor.
		{"constructor", `SELECT ` + list(`INTERVAL n YEAR`, `INTERVAL 10 * n + 5 DAY`, `INTERVAL 10001 * n SECOND`) + ` FROM UNNEST([NULL, 0, 1, -2]) n ORDER BY n`,
			[][]string{{"NULL", "NULL", "NULL"}, {"-2-0 0 0:0:0", "0-0 -15 0:0:0", "0-0 0 -5:33:22"}, {"0-0 0 0:0:0", "0-0 5 0:0:0", "0-0 0 0:0:0"}, {"1-0 0 0:0:0", "0-0 15 0:0:0", "0-0 0 2:46:41"}}},
		// interval.test parametrized_constructor (parameters inlined).
		{"parametrized_constructor", `SELECT ` + list(`INTERVAL (-10) QUARTER`, `INTERVAL (NULL) WEEK`),
			[][]string{{"-2-6 0 0:0:0", "NULL"}}},
		// interval.test add_interval, DATE and DATETIME columns (the
		// TIMESTAMP columns assume the fixture driver's
		// America/Los_Angeles default time zone).
		{"add_interval", `SELECT ` + list(`DATE '2010-10-10' + INTERVAL '10 20:20:20' DAY TO SECOND`, `INTERVAL '24' HOUR + DATE '1999-12-31'`,
			`INTERVAL 1 YEAR + DATETIME '0201-02-02 02:02:02'`) +
			`, DATETIME '1970-01-02 03:04:05.678' + INTERVAL '1-1 1 1:1:1.1111' YEAR TO SECOND = DATETIME '1971-02-03 04:05:06.789100'`,
			[][]string{{"2010-10-20 20:20:20", "2000-01-01 00:00:00", "0202-02-02 02:02:02", "true"}}},
		// interval.test subtract_interval, DATE and DATETIME columns.
		{"subtract_interval", `SELECT ` + list(`DATE '2010-10-10' - INTERVAL '10 1:1:1' DAY TO SECOND`, `DATETIME '1970-01-02 03:04:05.678' - INTERVAL '1-1 1 1:1:1.111' YEAR TO SECOND`),
			[][]string{{"2010-09-29 22:58:59", "1968-12-01 02:03:04.567"}}},
		// interval.test sum.
		{"sum", `SELECT ` + s(`SUM(i)`) + ` FROM UNNEST([interval 1 year, interval 2 month, interval 3 day, NULL, interval 4 hour, interval 5 minute, interval '6.789' second, NULL]) i`,
			[][]string{{"1-2 3 4:5:6.789"}}},
		// interval.test sum_handles_intermediate_overflow.
		{"sum_intermediate_overflow", `SELECT ` + s(`SUM(i)`) + ` FROM UNNEST([interval '10000' year, interval '10000' year, interval '10000' year, interval '-120000' month, interval '-120000' month]) i`,
			[][]string{{"10000-0 0 0:0:0"}}},
		// interval.test avg: the fixture prints 5:0:0.200 (the fraction
		// in groups of three digits, as BigQuery does).
		{"avg", `SELECT ` + s(`AVG(i)`) + ` FROM UNNEST([interval 1 year, interval 1 month, interval 1 day, NULL, interval 1 hour, interval 1 second, NULL]) i`,
			[][]string{{"0-2 18 5:0:0.200"}}},
		// interval.test avg_1_element.
		{"avg_1_element", `SELECT ` + s(`AVG(i)`) + ` FROM UNNEST([interval '1-2 3 4:5:6.789' year to second]) i`,
			[][]string{{"1-2 3 4:5:6.789"}}},
		// interval.test avg_handles_intermediate_overflow.
		{"avg_intermediate_overflow", `SELECT ` + s(`AVG(i)`) + ` FROM UNNEST([interval 10000 year, interval 8000 year, interval 3660000 day, interval 366000 day]) i`,
			[][]string{{"4500-0 1006500 0:0:0"}}},
		// interval.test avg_positive_negative_cancel_each_other.
		{"avg_cancel", `SELECT ` + s(`AVG(i)`) + ` FROM UNNEST([interval -1 year, interval -5 day, interval -1 hour, interval -1 minute, interval 12 month, interval 5 day, interval 60 minute, interval 60 second]) i`,
			[][]string{{"0-0 0 0:0:0"}}},
		// interval.test avg_nano_truncation, avg_nano_truncation2_micros.
		{"avg_nano_truncation", `SELECT ` + s(`AVG(i)`) + ` FROM UNNEST([interval 0 second, interval '0.000000001' second]) i`,
			[][]string{{"0-0 0 0:0:0"}}},
		{"avg_nano_truncation2_micros", `SELECT ` + s(`AVG(i)`) + ` FROM UNNEST([interval 0 second, interval '-0.000000003' second]) i`,
			[][]string{{"0-0 0 0:0:0"}}},
		// interval.test avg_nano_truncation3_micros: time parts beyond
		// the int64 nanosecond range.
		{"avg_nano_truncation3_micros", `SELECT ` + s(`AVG(e)`) + ` FROM UNNEST([INTERVAL "-7959-1 -153930 54938167:22:41.712081" YEAR TO SECOND,
			INTERVAL "8630-9 3512412 62113018:54:19.721996" YEAR TO SECOND,
			INTERVAL "675-8 -224302 -58850294:15:14.439522" YEAR TO SECOND,
			INTERVAL "2580-1 -200995 40403849:16:44.554610" YEAR TO SECOND]) AS e`,
			[][]string{{"981-10 733303 24651203:19:37.887291"}}},
		// interval.test sum_distinct_analytic.
		{"sum_distinct_analytic", `SELECT ` + s(`SUM(DISTINCT value) OVER()`) + ` FROM ` + intervals + ` WHERE value >= INTERVAL 29 DAY AND value <= Interval 31 DAY`,
			[][]string{{"0-1 0 0:0:0"}, {"0-1 0 0:0:0"}, {"0-1 0 0:0:0"}}},
		// interval.test analytic_running_sum, restricted to the values
		// below one hour.
		{"analytic_running_sum", `SELECT ` + s(`SUM(value) OVER(ORDER BY value ASC, id ASC ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW)`) + ` FROM ` + intervals + ` WHERE value < INTERVAL 1 hour ORDER BY value, id`,
			[][]string{{"0-0 0 -4:5:6.789"}, {"0-0 0 -4:5:7.789"}, {"0-0 0 -4:5:7.789001"}, {"0-0 0 -4:5:7.789001"}, {"0-0 0 -4:5:7.789"}}},
		// interval.test approx_count_distinct: equal intervals written
		// differently count once.
		{"approx_count_distinct", `SELECT APPROX_COUNT_DISTINCT(value) FROM ` + intervals,
			[][]string{{"11"}}},
		// interval.test approx_top_count (the value compared by equality).
		{"approx_top_count", `SELECT r.count, r.value = INTERVAL 30 DAY FROM UNNEST((SELECT APPROX_TOP_COUNT(value, 1) FROM ` + intervals + `)) r`,
			[][]string{{"3", "true"}}},
		// interval.test analytic_partitionby: 1 MONTH, 30 DAY and 720
		// HOUR share one partition.
		{"analytic_partitionby", `SELECT id, cnt FROM (SELECT id, COUNT(*) OVER(PARTITION BY value) cnt FROM ` + intervals + `) WHERE id IN (5, 6, 7, 10) ORDER BY id`,
			[][]string{{"5", "3"}, {"6", "3"}, {"7", "3"}, {"10", "1"}}},
		// interval.test in_semijoin.
		{"in_semijoin", `SELECT id FROM ` + intervals + ` WHERE value IN (SELECT i FROM UNNEST([interval -1 second, interval 31 day, interval 30 day]) i) ORDER BY id`,
			[][]string{{"4"}, {"5"}, {"6"}, {"7"}}},
		// interval.test justify_days.
		{"justify_days", `SELECT ` + s(`JUSTIFY_DAYS(i)`) + ` FROM UNNEST([INTERVAL '1-2 3 4:5:6.789' YEAR TO SECOND, INTERVAL '1-2 35 48:5:6.789' YEAR TO SECOND, INTERVAL '1-2 -35 -48:5:6.789' YEAR TO SECOND]) i WITH OFFSET o ORDER BY o`,
			[][]string{{"1-2 3 4:5:6.789"}, {"1-3 5 48:5:6.789"}, {"1-0 25 -48:5:6.789"}}},
		// interval.test justify_interval.
		{"justify_interval", `SELECT ` + s(`JUSTIFY_INTERVAL(i)`) + ` FROM UNNEST([INTERVAL '1-2 3 4:5:6.789' YEAR TO SECOND,
			INTERVAL '1-2 35 4:5:6.789' YEAR TO SECOND, INTERVAL '1-2 -35 4:5:6.789' YEAR TO SECOND,
			INTERVAL '1-2 12 40:5:6.789' YEAR TO SECOND, INTERVAL '1-2 -12 -40:5:6.789' YEAR TO SECOND,
			INTERVAL '1-2 48 75:7:4.657' YEAR TO SECOND, INTERVAL '1-2 -48 -75:7:4.657' YEAR TO SECOND]) i WITH OFFSET o ORDER BY o`,
			[][]string{{"1-2 3 4:5:6.789"}, {"1-3 5 4:5:6.789"}, {"1-0 25 4:5:6.789"}, {"1-2 13 16:5:6.789"}, {"1-1 16 7:54:53.211"}, {"1-3 21 3:7:4.657"}, {"1-0 8 20:52:55.343"}}},
		// interval_functions.md EXTRACT examples.
		{"doc_extract_1", `SELECT EXTRACT(YEAR FROM i), EXTRACT(MONTH FROM i), EXTRACT(DAY FROM i), EXTRACT(HOUR FROM i), EXTRACT(MINUTE FROM i), EXTRACT(SECOND FROM i), EXTRACT(MILLISECOND FROM i), EXTRACT(MICROSECOND FROM i)
			FROM UNNEST([INTERVAL '1-2 3 4:5:6.789999' YEAR TO SECOND, INTERVAL '0-13 370 48:61:61' YEAR TO SECOND]) AS i WITH OFFSET o ORDER BY o`,
			[][]string{{"1", "2", "3", "4", "5", "6", "789", "789999"}, {"1", "1", "370", "49", "2", "1", "0", "0"}}},
		{"doc_extract_2", `SELECT EXTRACT(HOUR FROM i), EXTRACT(MINUTE FROM i) FROM UNNEST([INTERVAL '10 -12:30' DAY TO MINUTE]) AS i`,
			[][]string{{"-12", "-30"}}},
		{"doc_extract_3", `SELECT EXTRACT(YEAR FROM i), EXTRACT(MONTH FROM i) FROM UNNEST([INTERVAL '-22-6 10 -12:30' YEAR TO MINUTE]) AS i`,
			[][]string{{"-22", "-6"}}},
		// interval_functions.md JUSTIFY_DAYS example.
		{"doc_justify_days", `SELECT ` + list(`JUSTIFY_DAYS(INTERVAL 29 DAY)`, `JUSTIFY_DAYS(INTERVAL -30 DAY)`, `JUSTIFY_DAYS(INTERVAL 31 DAY)`, `JUSTIFY_DAYS(INTERVAL -65 DAY)`, `JUSTIFY_DAYS(INTERVAL 370 DAY)`),
			[][]string{{"0-0 29 0:0:0", "-0-1 0 0:0:0", "0-1 1 0:0:0", "-0-2 -5 0:0:0", "1-0 10 0:0:0"}}},
		// interval_functions.md JUSTIFY_HOURS example.
		{"doc_justify_hours", `SELECT ` + list(`JUSTIFY_HOURS(INTERVAL 23 HOUR)`, `JUSTIFY_HOURS(INTERVAL -24 HOUR)`, `JUSTIFY_HOURS(INTERVAL 47 HOUR)`, `JUSTIFY_HOURS(INTERVAL -12345 MINUTE)`),
			[][]string{{"0-0 0 23:0:0", "0-0 -1 0:0:0", "0-0 1 23:0:0", "0-0 -8 -13:45:0"}}},
		// interval_functions.md JUSTIFY_INTERVAL example.
		{"doc_justify_interval", `SELECT ` + s(`JUSTIFY_INTERVAL(INTERVAL '29 49:00:00' DAY TO SECOND)`),
			[][]string{{"0-1 1 1:0:0"}}},
		// interval_functions.md MAKE_INTERVAL example.
		{"doc_make_interval", `SELECT ` + list(`MAKE_INTERVAL(1, 6, 15)`, `MAKE_INTERVAL(hour => 10, second => 20)`, `MAKE_INTERVAL(1, minute => 5, day => 2)`),
			[][]string{{"1-6 15 0:0:0", "0-0 0 10:0:20", "1-0 2 0:5:0"}}},
		// interval.test make_interval.
		{"make_interval", `SELECT ` + list(`make_interval(year => 1)`, `make_interval(second => -6)`, `make_interval()`,
			`make_interval(1,-2,3,-4,5,-6)`, `make_interval(second=>-6, minute=>5, hour=>-4, day=>3, month=>-2, year=>1)`,
			`make_interval(1,-2,3)`, `make_interval(hour=>-4, minute=>5)`, `make_interval(second=>-6, month=>-2)`),
			[][]string{{"1-0 0 0:0:0", "0-0 0 -0:0:6", "0-0 0 0:0:0", "0-10 3 -3:55:6", "0-10 3 -3:55:6", "0-10 3 0:0:0", "0-0 0 -3:55:0", "-0-2 0 -0:0:6"}}},
	}
	ctx := context.Background()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows, err := db.QueryContext(ctx, tc.query)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			cols, err := rows.Columns()
			if err != nil {
				t.Fatal(err)
			}
			var got [][]string
			for rows.Next() {
				vals := make([]any, len(cols))
				ptrs := make([]any, len(cols))
				for i := range vals {
					ptrs[i] = &vals[i]
				}
				if err := rows.Scan(ptrs...); err != nil {
					t.Fatal(err)
				}
				row := make([]string, len(vals))
				for i, v := range vals {
					if v == nil {
						row[i] = "NULL"
					} else {
						row[i] = fmt.Sprint(v)
					}
				}
				got = append(got, row)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}

	// interval.test make_interval_overflow and sum_overflow.
	for _, q := range []string{
		`SELECT make_interval(year => 10000, month => 1)`,
		`SELECT SUM(i) FROM UNNEST([interval 10000 year, interval '1' month]) i`,
	} {
		var v any
		err := db.QueryRowContext(ctx, q).Scan(&v)
		if err == nil || !strings.Contains(err.Error(), "Interval field months '120001' is out of range -120000 to 120000") {
			t.Errorf("%s: got err %v, want months out of range", q, err)
		}
	}
}
