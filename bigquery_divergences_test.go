package googlesqlite_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

// TestBigQueryDivergences pins fixes for divergences measured against
// real BigQuery by the differential probe harness in Flip-to/flipto-dbt
// (PR 365, analyses/emulator_differential_results.md). Each expected
// value is the BigQuery answer recorded there; the ID in each case name
// is the row in that document. Inputs come from UNNEST or a STRUCT field
// where the probe did, so the analyzer cannot constant-fold them.
func TestBigQueryDivergences(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("googlesqlite", ":memory:?_test=bigquery_divergences")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cases := []struct {
		name  string
		query string
		want  any
	}{
		// S1: the default time zone is UTC, also for folded literals.
		{"S1_cast_timestamp_to_date", `SELECT CAST(CAST(TIMESTAMP '2024-01-01 03:00:00+00' AS DATE) AS STRING)`, "2024-01-01"},
		{"S1_cast_timestamp_to_string", `SELECT CAST(TIMESTAMP '2024-01-01 00:00:00+00' AS STRING)`, "2024-01-01 00:00:00+00"},
		{"S1_cast_timestamp_to_datetime", `SELECT CAST(CAST(TIMESTAMP '2024-01-01 03:00:00+00' AS DATETIME) AS STRING)`, "2024-01-01 03:00:00"},
		{"S1_cast_string_to_timestamp", `SELECT CAST(CAST('2024-01-01 10:00:00' AS TIMESTAMP) AS STRING)`, "2024-01-01 10:00:00+00"},
		{"S1_timestamp_diff_day", `SELECT TIMESTAMP_DIFF(TIMESTAMP '2024-01-02 03:00:00+00', TIMESTAMP '2024-01-01 09:00:00+00', DAY)`, int64(0)},
		{"S1_extract_week_timestamp", `SELECT EXTRACT(WEEK FROM TIMESTAMP '2017-01-02 12:00:00+00')`, int64(1)},
		// S5: FALSE AND NULL is FALSE.
		{"S5_between_null_bound", `SELECT 1 BETWEEN 5 AND CAST(NULL AS INT64)`, false},
		// S14: FLOAT64 to INT64 rounds half away from zero.
		{"S14_float_to_int_rounds", `SELECT CAST(s.x AS INT64) FROM (SELECT STRUCT(2.5 AS x) AS s)`, int64(3)},
		// S15: string casts trim whitespace and follow BigQuery's literal rules.
		{"S15_string_to_float_trim", `SELECT SAFE_CAST(s.x AS FLOAT64) FROM (SELECT STRUCT(' 2.5 ' AS x) AS s)`, 2.5},
		{"S15_string_to_numeric_trim", `SELECT CAST(SAFE_CAST(s.x AS NUMERIC) AS STRING) FROM (SELECT STRUCT(' 2.5 ' AS x) AS s)`, "2.5"},
		{"S15_string_to_int_trim", `SELECT SAFE_CAST(s.x AS INT64) FROM (SELECT STRUCT(' 12 ' AS x) AS s)`, int64(12)},
		{"S15_nan_to_numeric", `SELECT SAFE_CAST(s.x AS NUMERIC) FROM (SELECT STRUCT('NaN' AS x) AS s)`, nil},
		{"S15_one_to_bool", `SELECT SAFE_CAST(s.x AS BOOL) FROM (SELECT STRUCT('1' AS x) AS s)`, nil},
		{"S15_loose_date", `SELECT CAST(SAFE_CAST(s.x AS DATE) AS STRING) FROM (SELECT STRUCT('2024-2-9' AS x) AS s)`, "2024-02-09"},
		{"S15_date_with_time_rejected", `SELECT SAFE_CAST(s.x AS DATE) FROM (SELECT STRUCT('2024-02-29 10:00:00' AS x) AS s)`, nil},
		{"S15_timestamp_zone_name", `SELECT CAST(SAFE_CAST(s.x AS TIMESTAMP) AS STRING) FROM (SELECT STRUCT('2024-01-01 10:00:00 America/New_York' AS x) AS s)`, "2024-01-01 15:00:00+00"},
		// S17: ABS keeps INT64.
		{"S17_abs_max_int64", `SELECT ABS(x) FROM UNNEST([9223372036854775807]) x`, int64(9223372036854775807)},
		// S23: WEEK is Sunday-based.
		{"S23_extract_week_date", `SELECT EXTRACT(WEEK FROM DATE '2023-12-31')`, int64(53)},
		// S26: SUBSTR counts characters.
		{"S26_substr_non_ascii", `SELECT SUBSTR(x, 1) FROM UNNEST(['Ünïcödé ß']) x`, "Ünïcödé ß"},
		// L1: unary minus on a FLOAT64 column.
		{"L1_unary_minus_column", `SELECT POW(2, -s.d / 7.0) FROM (SELECT STRUCT(7.0 AS d) AS s)`, 0.5},
		// L2: NULLIF with a NULL second argument returns the first.
		{"L2_nullif_null", `SELECT NULLIF(x, CAST(NULL AS STRING)) FROM UNNEST(['a']) x`, "a"},
		// L10: EXTRACT(WEEK(<weekday>)).
		{"L10_extract_week_monday", `SELECT EXTRACT(WEEK(MONDAY) FROM DATE '2017-11-05')`, int64(44)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got any
			if err := db.QueryRowContext(context.Background(), c.query).Scan(&got); err != nil {
				t.Fatalf("%s: %v", c.query, err)
			}
			if got != c.want {
				t.Errorf("%s = %#v, want %#v", c.query, got, c.want)
			}
		})
	}

	// L11: a zero step is an error, not an endless loop. GENERATE_DATE_ARRAY
	// raises the same error (compliance array_functions.test).
	for _, q := range []string{
		`SELECT GENERATE_ARRAY(1, 0, 0)`,
		`SELECT GENERATE_DATE_ARRAY(DATE '2016-01-01', DATE '2017-01-01', INTERVAL 0 DAY)`,
	} {
		rows, err := db.QueryContext(context.Background(), q)
		if err == nil {
			for rows.Next() {
			}
			err = rows.Err()
			rows.Close()
		}
		if err == nil || !strings.Contains(err.Error(), "step cannot be 0") {
			t.Errorf("%s: err = %v, want step cannot be 0", q, err)
		}
	}
}
