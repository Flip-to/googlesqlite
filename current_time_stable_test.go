package googlesqlite_test

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

// TestCurrentTimeStablePerStatement checks that every CURRENT_* call in
// one statement sees the same instant. BigQuery documents this for
// CURRENT_TIMESTAMP ("the current time is the same for all calls within
// a statement"), and CURRENT_DATETIME / CURRENT_DATE / CURRENT_TIME
// derive from that same instant in the default (UTC) time zone.
// Evaluating the clock once per call used to make these comparisons
// flip whenever two calls straddled a microsecond boundary.
func TestCurrentTimeStablePerStatement(t *testing.T) {
	db, err := sql.Open("googlesqlite", ":memory:?_test=current_time_stable")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	queries := []string{
		// A zone argument that depends on the row keeps SQLite from
		// evaluating the call once per statement, so every row reads
		// the clock.
		`SELECT COUNT(DISTINCT CURRENT_DATETIME(IF(x > 0, 'UTC', 'UTC'))) = 1 FROM UNNEST(GENERATE_ARRAY(1, 200000)) AS x`,
		`SELECT LOGICAL_AND(CURRENT_DATETIME(IF(x > 0, 'UTC', 'UTC')) = CURRENT_DATETIME()) FROM UNNEST(GENERATE_ARRAY(1, 200000)) AS x`,
		`SELECT COUNT(DISTINCT CURRENT_TIMESTAMP()) = 1 FROM UNNEST(GENERATE_ARRAY(1, 20000))`,
		`SELECT LOGICAL_AND(CURRENT_TIMESTAMP() = CURRENT_TIMESTAMP()) FROM UNNEST(GENERATE_ARRAY(1, 20000))`,
		`SELECT LOGICAL_AND(CURRENT_DATETIME() = DATETIME(CURRENT_TIMESTAMP())) FROM UNNEST(GENERATE_ARRAY(1, 20000))`,
		`SELECT LOGICAL_AND(CURRENT_DATE() = DATE(CURRENT_TIMESTAMP())) FROM UNNEST(GENERATE_ARRAY(1, 20000))`,
		`SELECT LOGICAL_AND(CURRENT_TIME() = TIME(CURRENT_TIMESTAMP())) FROM UNNEST(GENERATE_ARRAY(1, 20000))`,
		`SELECT CURRENT_DATE('America/Los_Angeles') = DATE(CURRENT_TIMESTAMP(), 'America/Los_Angeles')`,
		`SELECT CURRENT_DATETIME('Asia/Tokyo') = DATETIME(CURRENT_TIMESTAMP(), 'Asia/Tokyo')`,
		`SELECT CURRENT_TIME('+05:30') = TIME(CURRENT_TIMESTAMP(), '+05:30')`,
		`SELECT CURRENT_TIMESTAMP() = CURRENT_TIMESTAMP()`,
	}
	// Two call sites straddle a clock tick only occasionally on coarse
	// clocks (Windows), so repeat the plain comparison.
	for i := 0; i < 500; i++ {
		queries = append(queries, `SELECT CURRENT_TIMESTAMP() = CURRENT_TIMESTAMP() AND CURRENT_DATETIME() = DATETIME(CURRENT_TIMESTAMP())`)
	}
	ctx := context.Background()
	for _, q := range queries {
		for _, viaTx := range []bool{false, true} {
			var got bool
			if viaTx {
				tx, err := db.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				err = tx.QueryRowContext(ctx, q).Scan(&got)
				_ = tx.Rollback()
				if err != nil {
					t.Fatalf("%s (tx): %v", q, err)
				}
			} else if err := db.QueryRowContext(ctx, q).Scan(&got); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
			if !got {
				t.Errorf("%s (tx=%v) = false, want true", q, viaTx)
			}
		}
	}
}

// TestCurrentTimeInStoredBodiesIsNotFrozen checks that a view, a SQL
// function or a column DEFAULT that calls CURRENT_TIMESTAMP reads the
// time of the statement that uses it, not the time it was created.
func TestCurrentTimeInStoredBodiesIsNotFrozen(t *testing.T) {
	db, err := sql.Open("googlesqlite", ":memory:?_test=current_time_bodies")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	for _, q := range []string{
		`CREATE VIEW v AS SELECT CURRENT_TIMESTAMP() AS ts`,
		`CREATE FUNCTION f() AS (CURRENT_TIMESTAMP())`,
		`CREATE TABLE d (id INT64, ts TIMESTAMP DEFAULT CURRENT_TIMESTAMP())`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	time.Sleep(60 * time.Millisecond)
	for _, q := range []string{
		`SELECT ts >= TIMESTAMP_SUB(CURRENT_TIMESTAMP(), INTERVAL 30 MILLISECOND) FROM v`,
		`SELECT f() >= TIMESTAMP_SUB(CURRENT_TIMESTAMP(), INTERVAL 30 MILLISECOND)`,
	} {
		var fresh bool
		if err := db.QueryRow(q).Scan(&fresh); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		if !fresh {
			t.Errorf("%s = false: the body kept the time it was created at", q)
		}
	}
	if _, err := db.Exec(`INSERT d (id) VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	time.Sleep(60 * time.Millisecond)
	if _, err := db.Exec(`INSERT d (id) VALUES (2)`); err != nil {
		t.Fatal(err)
	}
	var diff int64
	if err := db.QueryRow(`SELECT TIMESTAMP_DIFF(MAX(ts), MIN(ts), MILLISECOND) FROM d`).Scan(&diff); err != nil {
		t.Fatal(err)
	}
	if diff < 30 {
		t.Errorf("DEFAULT CURRENT_TIMESTAMP() of two inserts 60 ms apart differ by %d ms", diff)
	}
}
