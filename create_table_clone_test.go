package googlesqlite_test

import (
	"strings"
	"testing"
)

// TestCreateTableClone: CREATE TABLE ... CLONE <table> copies the rows and the column definitions
// of the source, the clone does not change when the source does, and the new table is reported
// as created (it used to fail with "CREATE TABLE CLONE is not supported").
func TestCreateTableClone(t *testing.T) {
	t.Parallel()
	db, ctx := openGapsDB(t)
	mustExecGaps(t, ctx, db,
		"CREATE TABLE `p.src.t` (id INT64 NOT NULL, d DATE, v STRING) PARTITION BY d",
		"INSERT INTO `p.src.t` (id, d, v) VALUES (1, DATE '2026-10-01', 'a'), (2, DATE '2026-10-02', 'b')",
	)

	cc := changedCatalogOf(t, ctx, db, "CREATE TABLE IF NOT EXISTS `p.dst.t_clone` CLONE `p.src.t`")
	if len(cc.Table.Added) != 1 || strings.Join(cc.Table.Added[0].NamePath, ".") != "p.dst.t_clone" {
		t.Fatalf("expected p.dst.t_clone reported as added, got %+v", cc.Table)
	}
	added := cc.Table.Added[0]
	if len(added.Columns) != 3 || !added.Columns[0].IsNotNull {
		t.Fatalf("clone must keep the source's column definitions, got %+v", added.Columns)
	}
	if added.PartitionBy != "d" {
		t.Fatalf("clone must keep the source's PARTITION BY expression, got %q", added.PartitionBy)
	}

	count := func(table string) (n int) {
		t.Helper()
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM `"+table+"`").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if got := count("p.dst.t_clone"); got != 2 {
		t.Fatalf("clone has %d rows, want 2", got)
	}

	// The clone is independent of the source from now on.
	mustExecGaps(t, ctx, db, "INSERT INTO `p.src.t` (id, d, v) VALUES (3, DATE '2026-10-03', 'c')")
	if got := count("p.dst.t_clone"); got != 2 {
		t.Fatalf("clone changed with its source: %d rows, want 2", got)
	}

	// IF NOT EXISTS on an existing clone changes nothing (BigQuery semantics), including the data.
	mustExecGaps(t, ctx, db, "CREATE TABLE IF NOT EXISTS `p.dst.t_clone` CLONE `p.src.t`")
	if got := count("p.dst.t_clone"); got != 2 {
		t.Fatalf("CLONE ... IF NOT EXISTS replaced an existing table: %d rows, want 2", got)
	}

	// The source can be dropped; the clone keeps its data.
	mustExecGaps(t, ctx, db, "DROP TABLE `p.src.t`")
	if got := count("p.dst.t_clone"); got != 2 {
		t.Fatalf("clone lost its data when the source was dropped: %d rows, want 2", got)
	}

	// CREATE OR REPLACE ... CLONE replaces the clone with the new source's rows.
	mustExecGaps(t, ctx, db,
		"CREATE TABLE `p.src.t2` (id INT64, d DATE, v STRING)",
		"INSERT INTO `p.src.t2` (id, d, v) VALUES (9, DATE '2026-11-01', 'z')",
		"CREATE OR REPLACE TABLE `p.dst.t_clone` CLONE `p.src.t2`",
	)
	if got := count("p.dst.t_clone"); got != 1 {
		t.Fatalf("CREATE OR REPLACE ... CLONE: %d rows, want 1", got)
	}
}

// TestCreateTableIfNotExistsAsSelect: CREATE TABLE IF NOT EXISTS ... AS SELECT on an existing table is
// a no-op (it used to fail with "table already exists").
func TestCreateTableIfNotExistsAsSelect(t *testing.T) {
	t.Parallel()
	db, ctx := openGapsDB(t)
	mustExecGaps(t, ctx, db,
		"CREATE TABLE `p.d.t` AS SELECT 1 AS x",
		"CREATE TABLE IF NOT EXISTS `p.d.t` AS SELECT 2 AS x",
	)
	var x int
	if err := db.QueryRowContext(ctx, "SELECT x FROM `p.d.t`").Scan(&x); err != nil {
		t.Fatal(err)
	}
	if x != 1 {
		t.Fatalf("IF NOT EXISTS replaced the existing table: x=%d, want 1", x)
	}
}
