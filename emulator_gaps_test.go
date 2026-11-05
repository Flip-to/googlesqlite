package googlesqlite_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/goccy/googlesqlite"
)

var gapsDBSeq atomic.Int64

func openGapsDB(t *testing.T) (*sql.DB, context.Context) {
	t.Helper()
	db, err := sql.Open("googlesqlite", fmt.Sprintf(":memory:?_test=emulator_gaps_%d", gapsDBSeq.Add(1)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db, context.Background()
}

func mustExecGaps(t *testing.T, ctx context.Context, db *sql.DB, queries ...string) {
	t.Helper()
	for _, q := range queries {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

// changedCatalogOf runs a statement through QueryContext and returns the
// catalog changes it reported.
func changedCatalogOf(t *testing.T, ctx context.Context, db *sql.DB, query string) *googlesqlite.ChangedCatalog {
	t.Helper()
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	defer rows.Close()
	cc, err := googlesqlite.ChangedCatalogFromRows(rows)
	if err != nil {
		t.Fatal(err)
	}
	return cc
}

// TestAlterTableAddColumn: ALTER TABLE ... ADD COLUMN [IF NOT EXISTS] adds
// the column to the table, so it can be selected, and reports the table as
// updated (it used to succeed without doing anything).
func TestAlterTableAddColumn(t *testing.T) {
	t.Parallel()
	db, ctx := openGapsDB(t)
	mustExecGaps(t, ctx, db,
		"CREATE TABLE `p.d.t` (id INT64)",
		"INSERT INTO `p.d.t` (id) VALUES (1)",
	)

	cc := changedCatalogOf(t, ctx, db, "ALTER TABLE `p`.`d`.`t` ADD COLUMN IF NOT EXISTS schema_key STRING")
	if len(cc.Table.Updated) != 1 || strings.Join(cc.Table.Updated[0].NamePath, ".") != "p.d.t" {
		t.Fatalf("expected p.d.t reported as updated, got %+v", cc.Table)
	}
	if got := len(cc.Table.Updated[0].Columns); got != 2 {
		t.Fatalf("updated spec has %d columns, want 2", got)
	}

	// The new column exists, is NULL for the existing row and can be written.
	var key sql.NullString
	if err := db.QueryRowContext(ctx, "SELECT schema_key FROM `p.d.t`").Scan(&key); err != nil {
		t.Fatalf("select new column: %v", err)
	}
	if key.Valid {
		t.Fatalf("existing row should have NULL schema_key, got %q", key.String)
	}
	mustExecGaps(t, ctx, db, "INSERT INTO `p.d.t` (id, schema_key) VALUES (2, 'x')")
	var n int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM `p.d.t` WHERE schema_key = 'x'").Scan(&n); err != nil || n != 1 {
		t.Fatalf("filter on new column: n=%d err=%v", n, err)
	}

	// Without IF NOT EXISTS, a new column is added and an existing one is an error.
	mustExecGaps(t, ctx, db, "ALTER TABLE `p`.`d`.`t` ADD COLUMN extra INT64")
	if _, err := db.ExecContext(ctx, "ALTER TABLE `p`.`d`.`t` ADD COLUMN extra INT64"); err == nil {
		t.Fatal("adding an existing column without IF NOT EXISTS must fail")
	}
	// IF NOT EXISTS on an existing column is a no-op and reports no change.
	cc = changedCatalogOf(t, ctx, db, "ALTER TABLE `p`.`d`.`t` ADD COLUMN IF NOT EXISTS extra INT64")
	if len(cc.Table.Updated) != 0 {
		t.Fatalf("no-op ALTER reported %d updated tables", len(cc.Table.Updated))
	}

	// INFORMATION_SCHEMA.COLUMNS lists the new columns.
	rows, err := db.QueryContext(ctx, "SELECT column_name FROM `p.d.INFORMATION_SCHEMA.COLUMNS` WHERE table_name = 't' ORDER BY ordinal_position")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			t.Fatal(err)
		}
		cols = append(cols, c)
	}
	if got := strings.Join(cols, ","); got != "id,schema_key,extra" {
		t.Fatalf("COLUMNS = %s", got)
	}
}

// TestCreateSchemaReported: CREATE SCHEMA [IF NOT EXISTS] ... OPTIONS(...)
// is reported in the ChangedCatalog so the embedding layer can create the
// dataset with its options (it used to be a silent no-op).
func TestCreateSchemaReported(t *testing.T) {
	t.Parallel()
	db, ctx := openGapsDB(t)

	cc := changedCatalogOf(t, ctx, db, "CREATE SCHEMA IF NOT EXISTS `p`.`reg_eu` OPTIONS (location='EU')")
	if len(cc.Schema.Added) != 1 {
		t.Fatalf("expected one schema, got %+v", cc.Schema)
	}
	s := cc.Schema.Added[0]
	if strings.Join(s.NamePath, ".") != "p.reg_eu" || !s.IfNotExists {
		t.Fatalf("unexpected schema spec: %+v", s)
	}
	if loc, ok := s.Option("location"); !ok || loc != "EU" {
		t.Fatalf("location = %q, %v", loc, ok)
	}

	cc = changedCatalogOf(t, ctx, db, "CREATE SCHEMA `p`.`plain`")
	if len(cc.Schema.Added) != 1 || cc.Schema.Added[0].IfNotExists {
		t.Fatalf("plain CREATE SCHEMA: %+v", cc.Schema.Added)
	}
	if _, ok := cc.Schema.Added[0].Option("location"); ok {
		t.Fatal("no location option expected")
	}
}

// TestInformationSchemaRoutines: INFORMATION_SCHEMA.ROUTINES lists the
// functions and table functions of a dataset, and CREATE / DROP of both are
// reported in the ChangedCatalog.
func TestInformationSchemaRoutines(t *testing.T) {
	t.Parallel()
	db, ctx := openGapsDB(t)
	// The dataset has a table, so its INFORMATION_SCHEMA stays resolvable once
	// the routines are dropped again.
	mustExecGaps(t, ctx, db, "CREATE TABLE `p.ds.anchor` (id INT64)")

	cc := changedCatalogOf(t, ctx, db, "CREATE FUNCTION `p`.`ds`.`add_one`(x INT64) AS (x + 1)")
	if len(cc.Function.Added) != 1 {
		t.Fatalf("function not reported: %+v", cc.Function)
	}
	cc = changedCatalogOf(t, ctx, db, "CREATE TABLE FUNCTION `p`.`ds`.`two_rows`(n INT64) AS SELECT n AS v UNION ALL SELECT n + 1")
	if len(cc.TableFunction.Added) != 1 {
		t.Fatalf("table function not reported: %+v", cc.TableFunction)
	}

	type routine struct{ name, typ, schema, body, dataType string }
	read := func() map[string]routine {
		rows, err := db.QueryContext(ctx, "SELECT routine_name, routine_type, routine_schema, routine_body, IFNULL(data_type, '') FROM `p`.`ds`.INFORMATION_SCHEMA.ROUTINES")
		if err != nil {
			t.Fatalf("ROUTINES: %v", err)
		}
		defer rows.Close()
		out := map[string]routine{}
		for rows.Next() {
			var r routine
			if err := rows.Scan(&r.name, &r.typ, &r.schema, &r.body, &r.dataType); err != nil {
				t.Fatal(err)
			}
			out[r.name] = r
		}
		return out
	}
	got := read()
	if r := got["add_one"]; r.typ != "FUNCTION" || r.schema != "ds" || r.body != "SQL" || r.dataType != "INT64" {
		t.Fatalf("add_one = %+v (all: %+v)", r, got)
	}
	if r := got["two_rows"]; r.typ != "TABLE FUNCTION" || r.schema != "ds" {
		t.Fatalf("two_rows = %+v (all: %+v)", r, got)
	}

	// A different dataset's routines are not listed.
	mustExecGaps(t, ctx, db, "CREATE FUNCTION `p`.`other`.`f`() AS (1)")
	if got := read(); len(got) != 2 {
		t.Fatalf("expected the two routines of ds, got %+v", got)
	}

	cc = changedCatalogOf(t, ctx, db, "DROP FUNCTION `p`.`ds`.`add_one`")
	if len(cc.Function.Deleted) != 1 {
		t.Fatalf("dropped function not reported: %+v", cc.Function)
	}
	cc = changedCatalogOf(t, ctx, db, "DROP TABLE FUNCTION `p`.`ds`.`two_rows`")
	if len(cc.TableFunction.Deleted) != 1 {
		t.Fatalf("dropped table function not reported: %+v", cc.TableFunction)
	}
	if got := read(); len(got) != 0 {
		t.Fatalf("routines after drop: %+v", got)
	}
}

// TestInformationSchemaPartitionsAndLegacyTables: INFORMATION_SCHEMA.PARTITIONS
// and `dataset.__TABLES__` report the row count of every table.
func TestInformationSchemaPartitionsAndLegacyTables(t *testing.T) {
	t.Parallel()
	db, ctx := openGapsDB(t)
	mustExecGaps(t, ctx, db,
		"CREATE TABLE `p.ds.t` (id INT64)",
		"INSERT INTO `p.ds.t` (id) VALUES (1), (2), (3)",
		"CREATE TABLE `p.ds.empty` (id INT64)",
		"CREATE VIEW `p.ds.v` AS SELECT id FROM `p.ds.t`",
		"CREATE TABLE `p.other.t` (id INT64)",
	)

	rows, err := db.QueryContext(ctx, "SELECT table_name, total_rows FROM `p`.`ds`.INFORMATION_SCHEMA.PARTITIONS ORDER BY table_name")
	if err != nil {
		t.Fatalf("PARTITIONS: %v", err)
	}
	defer rows.Close()
	var parts []string
	for rows.Next() {
		var name string
		var total int64
		if err := rows.Scan(&name, &total); err != nil {
			t.Fatal(err)
		}
		parts = append(parts, fmt.Sprintf("%s=%d", name, total))
	}
	if got := strings.Join(parts, ","); got != "empty=0,t=3" {
		t.Fatalf("PARTITIONS = %s (views and other datasets must not appear)", got)
	}

	rows2, err := db.QueryContext(ctx, "SELECT project_id, dataset_id, table_id, row_count, type, creation_time FROM `p`.`ds`.__TABLES__ ORDER BY table_id")
	if err != nil {
		t.Fatalf("__TABLES__: %v", err)
	}
	defer rows2.Close()
	var legacy []string
	for rows2.Next() {
		var project, dataset, table string
		var count, typ, created int64
		if err := rows2.Scan(&project, &dataset, &table, &count, &typ, &created); err != nil {
			t.Fatal(err)
		}
		if time.Since(time.UnixMilli(created)) > time.Hour || created <= 0 {
			t.Fatalf("creation_time %d is not a recent epoch in milliseconds", created)
		}
		legacy = append(legacy, fmt.Sprintf("%s.%s.%s=%d/%d", project, dataset, table, count, typ))
	}
	if got := strings.Join(legacy, ","); got != "p.ds.empty=0/1,p.ds.t=3/1,p.ds.v=0/2" {
		t.Fatalf("__TABLES__ = %s", got)
	}

	// The dataset-qualified form works too.
	var n int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM `ds.__TABLES__`").Scan(&n); err != nil || n != 3 {
		t.Fatalf("COUNT(*) FROM ds.__TABLES__: n=%d err=%v", n, err)
	}
}

// TestInformationSchemaCreationTimeIsTimestamp: TABLES.creation_time is a
// TIMESTAMP, so it compares with TIMESTAMP_SUB(CURRENT_TIMESTAMP(), ...)
// (it used to be a STRING and the comparison failed to analyze).
func TestInformationSchemaCreationTimeIsTimestamp(t *testing.T) {
	t.Parallel()
	db, ctx := openGapsDB(t)
	mustExecGaps(t, ctx, db, "CREATE TABLE `p.ds.t` (id INT64)")

	var name string
	err := db.QueryRowContext(ctx,
		"SELECT table_name FROM `p`.`ds`.INFORMATION_SCHEMA.TABLES WHERE creation_time > TIMESTAMP_SUB(CURRENT_TIMESTAMP(), INTERVAL 1 HOUR)").Scan(&name)
	if err != nil {
		t.Fatalf("recent tables: %v", err)
	}
	if name != "t" {
		t.Fatalf("table_name = %q", name)
	}
	err = db.QueryRowContext(ctx,
		"SELECT table_name FROM `p`.`ds`.INFORMATION_SCHEMA.TABLES WHERE creation_time < TIMESTAMP_SUB(CURRENT_TIMESTAMP(), INTERVAL 1 HOUR)").Scan(&name)
	if err != sql.ErrNoRows {
		t.Fatalf("a table created now is not older than an hour: %v", err)
	}

	var createdText string
	if err := db.QueryRowContext(ctx, "SELECT CAST(creation_time AS STRING) FROM `p`.`ds`.INFORMATION_SCHEMA.TABLES").Scan(&createdText); err != nil {
		t.Fatal(err)
	}
	created, err := time.Parse("2006-01-02 15:04:05.999999-07", createdText)
	if err != nil {
		t.Fatalf("creation_time %q: %v", createdText, err)
	}
	if time.Since(created) > time.Hour || created.After(time.Now().Add(time.Minute)) {
		t.Fatalf("creation_time = %v", created)
	}
	var typ string
	if err := db.QueryRowContext(ctx, "SELECT FORMAT('%T', creation_time) FROM `p`.`ds`.INFORMATION_SCHEMA.TABLES").Scan(&typ); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(typ, "TIMESTAMP") {
		t.Fatalf("creation_time is not a TIMESTAMP: %s", typ)
	}
}
