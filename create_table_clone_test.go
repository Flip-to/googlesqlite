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

// TestTableAndViewOptionsReported: the OPTIONS of CREATE VIEW (they were dropped) and of CREATE TABLE ... AS SELECT
// reach the table spec the embedding layer receives, with accessors for the description and the labels.
func TestTableAndViewOptionsReported(t *testing.T) {
	t.Parallel()
	db, ctx := openGapsDB(t)
	mustExecGaps(t, ctx, db, "CREATE TABLE `p.d.t` (x INT64)")

	view := changedCatalogOf(t, ctx, db,
		"CREATE OR REPLACE VIEW `p.d.v` OPTIONS(description='it\\'s \"quoted\"; fine', labels=[('team','data'),('tier','gold')]) AS SELECT * FROM `p.d.t`")
	if len(view.Table.Added) != 1 {
		t.Fatalf("view not reported as added: %+v", view.Table)
	}
	spec := view.Table.Added[0]
	if got, _ := spec.Option("description"); got != "it's \"quoted\"; fine" {
		t.Fatalf("view description = %q", got)
	}
	if got := spec.Labels(); len(got) != 2 || got["team"] != "data" || got["tier"] != "gold" {
		t.Fatalf("view labels = %v", got)
	}
	if _, ok := spec.Option("friendly_name"); ok {
		t.Fatal("an option that was not set must not be reported")
	}

	ctas := changedCatalogOf(t, ctx, db,
		"CREATE TABLE `p.d.t2` OPTIONS(description=\"table doc\", labels=[('a','b')]) AS SELECT 1 AS x")
	spec = ctas.Table.Added[0]
	if got, _ := spec.Option("description"); got != "table doc" {
		t.Fatalf("table description = %q", got)
	}
	if got := spec.Labels(); len(got) != 1 || got["a"] != "b" {
		t.Fatalf("table labels = %v", got)
	}
}
