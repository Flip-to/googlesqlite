package googlesqlite_test

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/goccy/googlesqlite"
)

// TestDropProcedure guards the dbt-bigquery table-function deploy, which runs
// `BEGIN DROP PROCEDURE IF EXISTS x; EXCEPTION WHEN ERROR THEN ... END` before
// CREATE TABLE FUNCTION. CREATE PROCEDURE is a metadata-only no-op, so DROP PROCEDURE
// used to fail at statement preparation with "currently unsupported DROP PROCEDURE
// statement", an error the script's EXCEPTION block cannot catch.
func TestDropProcedure(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("googlesqlite", ":memory:?_test=drop_procedure")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	for _, stmt := range []string{
		"DROP PROCEDURE IF EXISTS never_created",
		"CREATE OR REPLACE PROCEDURE pr(x INT64) BEGIN SELECT x; END",
		"DROP PROCEDURE pr",
		"DROP PROCEDURE IF EXISTS pr",
		"BEGIN DROP PROCEDURE IF EXISTS pr; EXCEPTION WHEN ERROR THEN SELECT 1; END",
	} {
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("%q: %v", stmt, err)
		}
	}

	// The deploy sequence works: drop the (absent) procedure, then create the table function.
	if _, err := conn.ExecContext(ctx, "CREATE OR REPLACE TABLE FUNCTION tvf(k INT64) AS (SELECT k + 1 AS n)"); err != nil {
		t.Fatalf("CREATE TABLE FUNCTION after DROP PROCEDURE: %v", err)
	}
	var n int64
	if err := conn.QueryRowContext(ctx, "SELECT n FROM tvf(1)").Scan(&n); err != nil || n != 2 {
		t.Fatalf("tvf(1): n=%d err=%v", n, err)
	}
}
